package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/geoip"
	"github.com/devleesch001/cauri/internal/geoip/geoiptest"
	"github.com/devleesch001/cauri/internal/ingest"
	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/internal/ttystore"
	"github.com/devleesch001/cauri/pkg/client"
	"github.com/devleesch001/cauri/pkg/protocol"
)

type env struct {
	srv    *httptest.Server
	db     *bun.DB
	tokens *auth.Tokens
	token  string // valid token of hp-01
	client *client.Client
}

func setup(t *testing.T, db *bun.DB, mod func(*api.Config)) *env {
	t.Helper()
	tty, err := ttystore.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tokens := auth.NewTokens(db)
	cfg := api.Config{
		DB: db, Ingest: ingest.New(db, tty, 1<<16), TTY: tty, Tokens: tokens,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if mod != nil {
		mod(&cfg)
	}
	srv := httptest.NewServer(api.New(cfg))
	t.Cleanup(srv.Close)

	_, token, err := tokens.Create(context.Background(), "hp-01", "test")
	if err != nil {
		t.Fatal(err)
	}
	c, err := client.New(srv.URL, token, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	return &env{srv: srv, db: db, tokens: tokens, token: token, client: c}
}

const ttyContent = "\x1b[0mroot@svr04:~# "

func ttySHA() string {
	s := sha256.Sum256([]byte(ttyContent))
	return hex.EncodeToString(s[:])
}

func events(sensor string) protocol.EventBatch {
	ts := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	b := protocol.EventBatch{SensorID: sensor}
	for i, e := range []map[string]any{
		{"eventid": "cowrie.session.connect", "src_port": 4242},
		{"eventid": "cowrie.login.success", "username": "root", "password": "root"},
		{"eventid": "cowrie.command.input", "input": "id"},
		{"eventid": "cowrie.log.closed", "shasum": ttySHA(), "size": len(ttyContent)},
	} {
		e["session"] = "a1b2c3d4e5f6"
		e["src_ip"] = "203.0.113.9"
		e["timestamp"] = ts.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		raw, _ := json.Marshal(e)
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID(sensor, raw), Raw: raw})
	}
	return b
}

func statusOf(err error) int {
	var se *client.StatusError
	if errors.As(err, &se) {
		return se.Code
	}
	return 0
}

func ttyMeta() protocol.TTYMeta {
	return protocol.TTYMeta{SensorID: "hp-01", Session: "a1b2c3d4e5f6", SHA256: ttySHA(), Size: int64(len(ttyContent))}
}

func TestIngestFlow(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		ctx := context.Background()

		// The recording before its session: retryable 404.
		_, err := e.client.SendTTY(ctx, ttyMeta(), strings.NewReader(ttyContent))
		if statusOf(err) != http.StatusNotFound || !client.Retryable(err) {
			t.Fatalf("tty before events: %v", err)
		}

		res, err := e.client.SendEvents(ctx, events("hp-01"))
		if err != nil || res.Accepted != 4 {
			t.Fatalf("events = %+v, %v", res, err)
		}
		res, err = e.client.SendEvents(ctx, events("hp-01"))
		if err != nil || res.Duplicates != 4 {
			t.Fatalf("resend = %+v, %v", res, err)
		}

		tr, err := e.client.SendTTY(ctx, ttyMeta(), strings.NewReader(ttyContent))
		if err != nil || tr.Status != protocol.TTYStored {
			t.Fatalf("tty = %+v, %v", tr, err)
		}
		tr, err = e.client.SendTTY(ctx, ttyMeta(), strings.NewReader(ttyContent))
		if err != nil || tr.Status != protocol.TTYExists {
			t.Fatalf("tty again = %+v, %v", tr, err)
		}
	})
}

func TestIngestAuth(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	ctx := context.Background()

	// Another sensor's identity with hp-01's token.
	if _, err := e.client.SendEvents(ctx, events("hp-02")); statusOf(err) != http.StatusForbidden {
		t.Errorf("sensor mismatch: %v", err)
	}
	m := ttyMeta()
	m.SensorID = "hp-02"
	if _, err := e.client.SendTTY(ctx, m, strings.NewReader(ttyContent)); statusOf(err) != http.StatusForbidden {
		t.Errorf("tty sensor mismatch: %v", err)
	}

	bad, _ := client.New(e.srv.URL, "cauri_wrong", e.srv.Client())
	if _, err := bad.SendEvents(ctx, events("hp-01")); statusOf(err) != http.StatusUnauthorized {
		t.Errorf("wrong token: %v", err)
	}

	list, _ := e.tokens.List(ctx)
	if err := e.tokens.Revoke(ctx, list[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.SendEvents(ctx, events("hp-01")); statusOf(err) != http.StatusUnauthorized || client.Retryable(err) {
		t.Errorf("revoked token: %v", err)
	}

	resp, err := e.srv.Client().Post(e.srv.URL+protocol.EventsPath, "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no token: %d", resp.StatusCode)
	}
}

func TestTTYErrors(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	ctx := context.Background()
	if _, err := e.client.SendEvents(ctx, events("hp-01")); err != nil {
		t.Fatal(err)
	}

	// Body differs from the announced digest.
	_, err := e.client.SendTTY(ctx, ttyMeta(), strings.NewReader(strings.Repeat("x", len(ttyContent))))
	if statusOf(err) != http.StatusUnprocessableEntity {
		t.Errorf("digest mismatch: %v", err)
	}

	// Larger than the limit of the test service (64 KiB).
	big := strings.Repeat("x", 1<<17)
	sum := sha256.Sum256([]byte(big))
	m := protocol.TTYMeta{SensorID: "hp-01", Session: "a1b2c3d4e5f6", SHA256: hex.EncodeToString(sum[:]), Size: 1 << 17}
	if _, err := e.client.SendTTY(ctx, m, strings.NewReader(big)); statusOf(err) != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %v", err)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, e.srv.URL+protocol.TTYPath("a1b2c3d4e5f6"), strings.NewReader("x"))
	req.Header.Set("Authorization", "Bearer "+e.token)
	req.Header.Set(protocol.HeaderSensor, "hp-01")
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("missing digest: %d", resp.StatusCode)
	}

	// The session is now linked to the right recording: another one conflicts.
	if _, err := e.client.SendTTY(ctx, ttyMeta(), strings.NewReader(ttyContent)); err != nil {
		t.Fatal(err)
	}
	other := sha256.Sum256([]byte("other"))
	m = protocol.TTYMeta{SensorID: "hp-01", Session: "a1b2c3d4e5f6", SHA256: hex.EncodeToString(other[:]), Size: 5}
	if _, err := e.client.SendTTY(ctx, m, strings.NewReader("other")); statusOf(err) != http.StatusConflict {
		t.Errorf("conflict: %v", err)
	}
}

func get(t *testing.T, e *env, path string, wantCode int, out any) {
	t.Helper()
	resp, err := e.srv.Client().Get(e.srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != wantCode {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s = %d %s, want %d", path, resp.StatusCode, body, wantCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
	}
}

func TestReadEndpoints(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		if _, err := e.client.SendEvents(context.Background(), events("hp-01")); err != nil {
			t.Fatal(err)
		}

		get(t, e, "/api/healthz", http.StatusOK, nil)

		var sensors []store.Sensor
		get(t, e, "/api/sensors", http.StatusOK, &sensors)
		if len(sensors) != 1 || sensors[0].Name != "hp-01" {
			t.Errorf("sensors = %+v", sensors)
		}

		var sessions struct {
			Items []store.SessionSummary `json:"items"`
			Total int                    `json:"total"`
			Limit int                    `json:"limit"`
		}
		get(t, e, "/api/sessions?sensor=hp-01&login=success&has_commands=true&from=2026-10-14T00:00:00Z", http.StatusOK, &sessions)
		if sessions.Total != 1 || len(sessions.Items) != 1 || sessions.Limit != store.DefaultLimit {
			t.Fatalf("sessions = %+v", sessions)
		}
		id := sessions.Items[0].ID
		get(t, e, "/api/sessions?login=failed", http.StatusOK, &sessions)
		if sessions.Total != 0 || sessions.Items == nil {
			t.Errorf("failed sessions = %+v, want an empty list", sessions)
		}

		var detail store.SessionDetail
		get(t, e, fmt.Sprint("/api/sessions/", id), http.StatusOK, &detail)
		if len(detail.Events) != 4 || len(detail.CommandList) != 1 || *detail.SrcPort != 4242 {
			t.Errorf("detail = %+v", detail)
		}
		get(t, e, "/api/sessions/unknown", http.StatusNotFound, nil)
		get(t, e, "/api/sessions/999999", http.StatusNotFound, nil)

		var attempts struct {
			Items []store.LoginAttemptRow `json:"items"`
		}
		get(t, e, "/api/login-attempts?success=true", http.StatusOK, &attempts)
		if len(attempts.Items) != 1 || attempts.Items[0].Sensor != "hp-01" {
			t.Errorf("attempts = %+v", attempts)
		}

		for _, bad := range []string{"from=yesterday", "has_tty=maybe", "login=other", "limit=-1"} {
			get(t, e, "/api/sessions?"+bad, http.StatusBadRequest, nil)
		}
		get(t, e, "/api/nothing", http.StatusNotFound, nil)
	})
}

// ReadAuth protects the read API only: ingestors keep their tokens.
func TestReadAuthHook(t *testing.T) {
	deny := func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	}
	e := setup(t, storetest.SQLite(t), func(c *api.Config) { c.ReadAuth = deny })
	get(t, e, "/api/sessions", http.StatusUnauthorized, nil)
	get(t, e, "/api/sessions/x/cast", http.StatusUnauthorized, nil)
	get(t, e, "/api/healthz", http.StatusOK, nil)
	if _, err := e.client.SendEvents(context.Background(), events("hp-01")); err != nil {
		t.Errorf("ingest with ReadAuth set: %v", err)
	}
}

func TestUIHandler(t *testing.T) {
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ui") })
	e := setup(t, storetest.SQLite(t), func(c *api.Config) { c.UI = ui })
	resp, err := e.srv.Client().Get(e.srv.URL + "/sessions/abc")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "ui" {
		t.Errorf("UI path served %q", body)
	}
	get(t, e, "/api/nothing", http.StatusNotFound, nil)
}

// realRecording is a recording written by the official Cowrie image.
func realRecording(t *testing.T) (protocol.TTYMeta, []byte) {
	t.Helper()
	data, err := os.ReadFile("../ttylog/testdata/cowrie-v3.1.2.tty")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return protocol.TTYMeta{
		SensorID: "hp-01", Session: "a1b2c3d4e5f6", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data)),
	}, data
}

// reply is a response read and closed.
type reply struct {
	code   int
	header http.Header
}

func getRaw(t *testing.T, e *env, path string, header http.Header) (reply, string) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, e.srv.URL+path, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return reply{code: resp.StatusCode, header: resp.Header}, string(body)
}

func TestCast(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		ctx := context.Background()
		b := events("hp-01")
		// The client reports its terminal size.
		size := []byte(`{"eventid":"cowrie.client.size","session":"a1b2c3d4e5f6","timestamp":"2026-10-14T09:00:00.5Z","width":120,"height":40}`)
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", size), Raw: size})
		if _, err := e.client.SendEvents(ctx, b); err != nil {
			t.Fatal(err)
		}
		id := sid(t, e, "hp-01", "a1b2c3d4e5f6")
		path := "/api/sessions/" + id + "/cast"

		resp, body := getRaw(t, e, path, nil)
		if resp.code != http.StatusNotFound || !strings.Contains(body, "no stored recording") {
			t.Fatalf("before upload: %d %s", resp.code, body)
		}

		m, data := realRecording(t)
		if _, err := e.client.SendTTY(ctx, m, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}

		resp, body = getRaw(t, e, path, nil)
		if resp.code != http.StatusOK || resp.header.Get("Content-Type") != "application/x-asciicast" {
			t.Fatalf("cast: %d %s %s", resp.code, resp.header.Get("Content-Type"), body)
		}
		lines := strings.Split(strings.TrimSpace(body), "\n")
		var header struct {
			Version       int                      `json:"version"`
			Term          struct{ Cols, Rows int } `json:"term"`
			IdleTimeLimit float64                  `json:"idle_time_limit"`
		}
		if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
			t.Fatal(err)
		}
		if header.Version != 3 || header.Term.Cols != 120 || header.Term.Rows != 40 || header.IdleTimeLimit != 2 {
			t.Errorf("header = %+v", header)
		}
		// 93 recorded writes, and a marker for the "id" command of events().
		if len(lines) != 1+93+1 || !strings.Contains(body, `"i", "uname -a\n"`) || !strings.Contains(body, `"m", "id"]`) {
			t.Errorf("%d events, input missing", len(lines)-1)
		}

		// Real time: no idle_time_limit, another ETag.
		etag := resp.header.Get("ETag")
		resp, body = getRaw(t, e, path+"?idle_time_limit=0", nil)
		if resp.code != http.StatusOK || strings.Contains(strings.SplitN(body, "\n", 2)[0], "idle_time_limit") {
			t.Errorf("real time header: %s", strings.SplitN(body, "\n", 2)[0])
		}
		if resp.header.Get("ETag") == etag {
			t.Error("same ETag for another conversion")
		}

		if resp, _ := getRaw(t, e, path, http.Header{"If-None-Match": {etag}}); resp.code != http.StatusNotModified {
			t.Errorf("If-None-Match: %d", resp.code)
		}
		for _, bad := range []string{"-1", "soon", "100000"} {
			if resp, _ := getRaw(t, e, path+"?idle_time_limit="+bad, nil); resp.code != http.StatusBadRequest {
				t.Errorf("idle_time_limit=%s: %d", bad, resp.code)
			}
		}
		if resp, _ := getRaw(t, e, "/api/sessions/unknown/cast", nil); resp.code != http.StatusNotFound {
			t.Errorf("unknown session: %d", resp.code)
		}
	})
}

// A stored file that is not a Cowrie recording is reported, not served.
func TestCastUnreadable(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	ctx := context.Background()
	if _, err := e.client.SendEvents(ctx, events("hp-01")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.client.SendTTY(ctx, ttyMeta(), strings.NewReader(ttyContent)); err != nil {
		t.Fatal(err)
	}
	resp, body := getRaw(t, e, "/api/sessions/"+sid(t, e, "hp-01", "a1b2c3d4e5f6")+"/cast", nil)
	if resp.code != http.StatusUnprocessableEntity || resp.header.Get("ETag") != "" {
		t.Errorf("= %d %s", resp.code, body)
	}
}

// realSession ingests the events of session 6f07bbfeafaa from the real
// Cowrie capture, then its recording, and returns its internal id.
// sid returns the id of a session, as in URLs.
func sid(t *testing.T, e *env, sensor, cowrieSession string) string {
	t.Helper()
	id, err := store.LookupSession(context.Background(), e.db, sensor, cowrieSession)
	if err != nil {
		t.Fatalf("session %s/%s: %v", sensor, cowrieSession, err)
	}
	return fmt.Sprint(id)
}

func realSession(t *testing.T, e *env) string {
	t.Helper()
	ctx := context.Background()
	data, err := os.ReadFile("../ingest/testdata/cowrie-v3.1.2.json")
	if err != nil {
		t.Fatal(err)
	}
	b := protocol.EventBatch{SensorID: "hp-01"}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", []byte(line)), Raw: json.RawMessage(line)})
	}
	if _, err := e.client.SendEvents(ctx, b); err != nil {
		t.Fatal(err)
	}
	m, tty := realRecording(t)
	m.Session = "6f07bbfeafaa"
	if _, err := e.client.SendTTY(ctx, m, bytes.NewReader(tty)); err != nil {
		t.Fatal(err)
	}
	return sid(t, e, "hp-01", "6f07bbfeafaa")
}

func TestChapters(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		id := realSession(t, e)

		var got struct {
			Records  int `json:"records"`
			Chapters []struct {
				Time       float64 `json:"time"`
				Command    string  `json:"command"`
				Output     string  `json:"output"`
				Truncated  bool    `json:"truncated"`
				FullScreen bool    `json:"full_screen"`
			} `json:"chapters"`
		}
		get(t, e, "/api/sessions/"+id+"/chapters", http.StatusOK, &got)
		if got.Records != 95 || len(got.Chapters) != 4 {
			t.Fatalf("chapters = %+v", got)
		}
		if c := got.Chapters[0]; c.Command != "uname -a" || !strings.HasPrefix(c.Output, "Linux svr04") || c.Time <= 0 {
			t.Errorf("first chapter = %+v", c)
		}

		resp, _ := getRaw(t, e, "/api/sessions/"+id+"/chapters", nil)
		if again, _ := getRaw(t, e, "/api/sessions/"+id+"/chapters", http.Header{"If-None-Match": {resp.header.Get("ETag")}}); again.code != http.StatusNotModified {
			t.Errorf("If-None-Match: %d", again.code)
		}

		// The same commands are markers of the replay.
		_, cast := getRaw(t, e, "/api/sessions/"+id+"/cast", nil)
		if n := strings.Count(cast, `, "m", `); n != 4 || !strings.Contains(cast, `"m", "cat /proc/cpuinfo | head -3"]`) {
			t.Errorf("%d markers in the cast", n)
		}

		get(t, e, "/api/sessions/unknown/chapters", http.StatusNotFound, nil)
	})
}

type verdictBody struct {
	Verdict string `json:"verdict"`
	Score   int    `json:"score"`
	Reasons []struct {
		Code   string `json:"code"`
		Points int    `json:"points"`
	} `json:"reasons"`
}

func TestVerdict(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		ctx := context.Background()

		// The scripted session of the real capture: whole lines, constant
		// delays, Go client.
		var v verdictBody
		get(t, e, "/api/sessions/"+realSession(t, e)+"/verdict", http.StatusOK, &v)
		if v.Verdict != "bot" || v.Score != -5 || len(v.Reasons) != 3 {
			t.Errorf("scripted session = %+v", v)
		}

		// Commands without a terminal (ssh host command), and a session
		// with no interaction at all.
		send := func(session string, events ...string) {
			b := protocol.EventBatch{SensorID: "hp-01"}
			for i, ev := range events {
				raw := fmt.Sprintf(`{"session":%q,"timestamp":"2026-10-14T10:00:0%dZ",%s}`, session, i, ev)
				b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", []byte(raw)), Raw: json.RawMessage(raw)})
			}
			if _, err := e.client.SendEvents(ctx, b); err != nil {
				t.Fatal(err)
			}
		}
		send("exec1", `"eventid":"cowrie.session.connect"`, `"eventid":"cowrie.client.version","version":"SSH-2.0-libssh_0.9.6"`,
			`"eventid":"cowrie.command.input","input":"uname -a"`)
		send("quiet1", `"eventid":"cowrie.session.connect"`, `"eventid":"cowrie.login.failed","username":"root","password":"x"`)

		get(t, e, "/api/sessions/"+sid(t, e, "hp-01", "exec1")+"/verdict", http.StatusOK, &v)
		if v.Verdict != "bot" || v.Score != -3 {
			t.Errorf("exec session = %+v", v)
		}
		get(t, e, "/api/sessions/"+sid(t, e, "hp-01", "quiet1")+"/verdict", http.StatusOK, &v)
		if v.Verdict != "no_interaction" || v.Reasons == nil {
			t.Errorf("quiet session = %+v", v)
		}
		get(t, e, "/api/sessions/unknown/verdict", http.StatusNotFound, nil)

		// The list filters on the stored verdict, the same as computed on
		// read; the scripted session was judged again with its recording.
		var sessions struct {
			Items []store.SessionSummary `json:"items"`
			Total int                    `json:"total"`
		}
		// The capture also has two sessions without interaction and one
		// that only typed exit (uncertain): all counted as bots.
		get(t, e, "/api/sessions", http.StatusOK, &sessions)
		if sessions.Total != 6 {
			t.Fatalf("sessions = %d", sessions.Total)
		}
		get(t, e, "/api/sessions?verdict=bot", http.StatusOK, &sessions)
		if sessions.Total != 6 {
			t.Errorf("bots = %d, want all sessions", sessions.Total)
		}
		for _, it := range sessions.Items {
			if fmt.Sprint(it.ID) == sid(t, e, "hp-01", "6f07bbfeafaa") && (*it.Verdict != "bot" || *it.VerdictScore != -5) {
				t.Errorf("stored verdict of the scripted session = %s %d", *it.Verdict, *it.VerdictScore)
			}
		}
		get(t, e, "/api/sessions?verdict=human", http.StatusOK, &sessions)
		if sessions.Total != 0 {
			t.Errorf("humans = %d", sessions.Total)
		}
		get(t, e, "/api/sessions?verdict=uncertain", http.StatusBadRequest, nil)
	})
}

func TestGeoIP(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		path := geoiptest.Write(t, t.TempDir(), "DBIP-City-Lite", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			geoiptest.Network{CIDR: "203.0.113.0/24", Country: "IR", City: "Tehran", Lat: 35.6944, Lon: 51.4215})
		geo, err := geoip.Open(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = geo.Close() }()
		e := setup(t, db, func(c *api.Config) {
			c.Ingest.SetLocator(geo)
			c.GeoIP = geo
			c.Version = "1.2.3"
		})
		if _, err := e.client.SendEvents(context.Background(), events("hp-01")); err != nil {
			t.Fatal(err)
		}

		var list struct {
			Items []store.SessionSummary `json:"items"`
		}
		get(t, e, "/api/sessions?country=ir", http.StatusOK, &list)
		if len(list.Items) != 1 || *list.Items[0].GeoCity != "Tehran" || *list.Items[0].GeoSource != "DBIP-City-Lite 2026-10-01" {
			t.Fatalf("sessions from IR = %+v", list.Items)
		}
		get(t, e, "/api/sessions?country=FR", http.StatusOK, &list)
		if len(list.Items) != 0 {
			t.Errorf("sessions from FR = %d", len(list.Items))
		}
		get(t, e, "/api/sessions?country=Iran", http.StatusBadRequest, nil)

		var countries []store.CountryCount
		get(t, e, "/api/countries", http.StatusOK, &countries)
		if len(countries) != 1 || countries[0] != (store.CountryCount{Country: "IR", Sessions: 1, Shells: 1}) {
			t.Errorf("countries = %+v", countries)
		}
		var cities []store.CityCount
		get(t, e, "/api/cities?verdict=bot", http.StatusOK, &cities)
		if len(cities) != 1 || cities[0] != (store.CityCount{Country: "IR", City: "Tehran", Lat: 35.6944, Lon: 51.4215, Sessions: 1, Shells: 1}) {
			t.Errorf("cities = %+v", cities)
		}
		get(t, e, "/api/cities?verdict=robot", http.StatusBadRequest, nil)
		get(t, e, "/api/sessions?country=IR&city=Tehran", http.StatusOK, &list)
		if len(list.Items) != 1 || *list.Items[0].GeoLat != 35.6944 {
			t.Errorf("sessions from Tehran = %+v", list.Items)
		}

		var a struct {
			Version string `json:"version"`
			GeoIP   *struct {
				Type, Provider string
			} `json:"geoip"`
		}
		get(t, e, "/api/about", http.StatusOK, &a)
		if a.Version != "1.2.3" || a.GeoIP == nil || a.GeoIP.Provider != "db-ip" || a.GeoIP.Type != "DBIP-City-Lite" {
			t.Errorf("about = %+v", a)
		}
	})
}

func TestAboutWithoutGeoIP(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	var a map[string]any
	get(t, e, "/api/about", http.StatusOK, &a)
	if v, ok := a["geoip"]; !ok || v != nil {
		t.Errorf("about = %v", a)
	}
}
