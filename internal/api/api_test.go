package api_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
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

// syncBuffer collects logs written by the server's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func TestTTYErrors(t *testing.T) {
	var logs syncBuffer
	e := setup(t, storetest.SQLite(t), func(c *api.Config) { c.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
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
	// The log says which sensor, session and recordings, which the request
	// log alone does not.
	var refused map[string]any
	for line := range strings.SplitSeq(logs.String(), "\n") {
		if strings.Contains(line, `"status":409`) && strings.Contains(line, "ingestion refused") {
			_ = json.Unmarshal([]byte(line), &refused)
		}
	}
	if refused["sensor"] != "hp-01" || refused["session"] != "a1b2c3d4e5f6" || refused["sha256"] != m.SHA256 ||
		!strings.Contains(fmt.Sprint(refused["reason"]), "stored "+ttySHA()) {
		t.Errorf("conflict log = %v\nlogs:\n%s", refused, logs.String())
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

		for _, bad := range []string{"from=yesterday", "has_tty=maybe", "login=other", "limit=-1", "sort=s.id", "order=up"} {
			get(t, e, "/api/sessions?"+bad, http.StatusBadRequest, nil)
		}
		get(t, e, "/api/login-attempts?sort=ts", http.StatusBadRequest, nil)
		get(t, e, "/api/attackers?sort=sessions&order=asc", http.StatusOK, nil)
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

		// No recording yet: the replay is simulated from the commands.
		resp, body := getRaw(t, e, path, nil)
		if resp.code != http.StatusOK || resp.header.Get("ETag") != "" ||
			!strings.Contains(body, `"m", "id"]`) || !strings.Contains(body, `"o", "# id\r\n"]`) {
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
		if len(countries) != 1 || countries[0] != (store.CountryCount{Country: "IR", Sessions: 1, LoggedIn: 1}) {
			t.Errorf("countries = %+v", countries)
		}
		var cities []store.CityCount
		get(t, e, "/api/cities?verdict=bot", http.StatusOK, &cities)
		if len(cities) != 1 || cities[0] != (store.CityCount{Country: "IR", City: "Tehran", Lat: 35.6944, Lon: 51.4215, Sessions: 1, LoggedIn: 1}) {
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

// An ASN database gives the network of sessions, independently of the
// location, and its attribution in /api/about.
func TestASN(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		path := geoiptest.Write(t, t.TempDir(), "GeoLite2-ASN", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC),
			geoiptest.Network{CIDR: "203.0.113.0/24", ASN: 64500, ASOrg: "Example Net"})
		asn, err := geoip.Open(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = asn.Close() }()
		e := setup(t, db, func(c *api.Config) {
			c.Ingest.SetNetworker(asn)
			c.ASN = asn
		})
		if _, err := e.client.SendEvents(context.Background(), events("hp-01")); err != nil {
			t.Fatal(err)
		}
		var list struct {
			Items []store.SessionSummary `json:"items"`
		}
		get(t, e, "/api/sessions?asn=64500", http.StatusOK, &list)
		if len(list.Items) != 1 || *list.Items[0].ASOrg != "Example Net" {
			t.Errorf("sessions of AS64500 = %+v", list.Items)
		}
		get(t, e, "/api/sessions?asn=AS64500", http.StatusBadRequest, nil)
		var o store.Origins
		get(t, e, "/api/stats/origins", http.StatusOK, &o)
		if len(o.Networks) != 1 || o.Networks[0].ASN != 64500 {
			t.Errorf("networks = %+v", o.Networks)
		}
		var a struct {
			ASN *struct{ Provider string } `json:"asn"`
		}
		get(t, e, "/api/about", http.StatusOK, &a)
		if a.ASN == nil || a.ASN.Provider != "maxmind" {
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
	if v, ok := a["providers"].([]any); !ok || len(v) != 0 {
		t.Errorf("providers without any location = %v", a["providers"])
	}
}

// Stored locations still require their attribution once their database
// is no longer loaded.
func TestAboutStoredLocations(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		if _, err := e.client.SendEvents(context.Background(), events("hp-01")); err != nil {
			t.Fatal(err)
		}
		if _, err := db.NewUpdate().Model((*store.Session)(nil)).
			Set("geo_source = ?", "DBIP-City-Lite 2026-10-01").Where("1 = 1").Exec(context.Background()); err != nil {
			t.Fatal(err)
		}
		var a struct {
			GeoIP     any      `json:"geoip"`
			Providers []string `json:"providers"`
		}
		get(t, e, "/api/about", http.StatusOK, &a)
		if a.GeoIP != nil || !slices.Equal(a.Providers, []string{"db-ip"}) {
			t.Errorf("about = %+v", a)
		}
	})
}

// The statistics answer on the real capture, with their filters.
func TestStats(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		realSession(t, e)

		var sum store.Summary
		get(t, e, "/api/stats/summary", http.StatusOK, &sum)
		if sum.Sessions != 4 || sum.LoggedIn != 2 || sum.SourceIPs != 1 || sum.Logins != 4 || sum.Accepted != 2 || sum.Previous != nil {
			t.Errorf("summary = %+v", sum)
		}
		get(t, e, "/api/stats/summary?from=2026-10-14T00:00:00Z&to=2026-10-15T00:00:00Z", http.StatusOK, &sum)
		if sum.Sessions != 4 || sum.Previous == nil || sum.Previous.Sessions != 0 {
			t.Errorf("summary of the day = %+v", sum)
		}

		var act store.Activity
		get(t, e, "/api/stats/activity", http.StatusOK, &act)
		if len(act.Days) != 1 || act.Days[0].Day != "2026-10-14" || len(act.Hours) != 1 || act.Hours[0].Hour != 8 {
			t.Errorf("activity = %+v", act)
		}

		var cred store.Credentials
		get(t, e, "/api/stats/credentials?sensor=hp-01", http.StatusOK, &cred)
		if cred.Attempts != 4 || len(cred.Worked) != 1 || cred.Worked[0] != (store.PairCount{Username: "admin", Password: "admin", Count: 2, Accepted: 2}) {
			t.Errorf("credentials = %+v", cred)
		}

		var pay store.Payloads
		get(t, e, "/api/stats/payloads", http.StatusOK, &pay)
		if pay.Items == nil {
			t.Errorf("payloads = %+v, want an empty list, not null", pay)
		}

		var orig store.Origins
		get(t, e, "/api/stats/origins?verdict=bot", http.StatusOK, &orig)
		if len(orig.Clients) != 1 || orig.Clients[0] != (store.ClientCount{Client: "SSH-2.0-Go", Sessions: 4, Sources: 1}) {
			t.Errorf("origins = %+v", orig)
		}

		// The scripted session ran 4 commands, the first one only exit.
		var cmds store.Commands
		get(t, e, "/api/stats/commands", http.StatusOK, &cmds)
		if cmds.Total != 5 || cmds.Distinct != 4 || cmds.Sessions != 2 || cmds.Top[0].Input != "exit" || cmds.Top[0].Sessions != 2 ||
			len(cmds.HASSH) != 1 || cmds.HASSH[0].Sessions != 2 {
			t.Errorf("commands = %+v", cmds)
		}
		var list struct {
			Total int `json:"total"`
		}
		get(t, e, "/api/sessions?command="+url.QueryEscape("uname -a"), http.StatusOK, &list)
		if list.Total != 1 {
			t.Errorf("sessions that ran uname -a = %d", list.Total)
		}
		get(t, e, "/api/sessions?hassh="+cmds.HASSH[0].HASSH, http.StatusOK, &list)
		if list.Total != 4 {
			t.Errorf("sessions with the HASSH = %d", list.Total)
		}

		get(t, e, "/api/stats/credentials?from=yesterday", http.StatusBadRequest, nil)
	})
}

// Exports follow the list filters, and neutralise cells a spreadsheet
// would run: attackers choose usernames.
func TestExport(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		realSession(t, e)
		raw := `{"session":"evil01","timestamp":"2026-10-14T10:00:00Z","eventid":"cowrie.login.failed","src_ip":"203.0.113.9","username":"=HYPERLINK(\"http://x\")","password":"-1+1"}`
		if _, err := e.client.SendEvents(context.Background(), protocol.EventBatch{SensorID: "hp-01",
			Events: []protocol.Event{{ID: protocol.EventID("hp-01", []byte(raw)), Raw: json.RawMessage(raw)}}}); err != nil {
			t.Fatal(err)
		}

		resp, body := getRaw(t, e, "/api/sessions.csv?verdict=bot", nil)
		if resp.code != http.StatusOK || !strings.HasPrefix(resp.header.Get("Content-Type"), "text/csv") ||
			!strings.Contains(resp.header.Get("Content-Disposition"), "attachment") {
			t.Fatalf("export = %d %v", resp.code, resp.header)
		}
		records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil || len(records) != 6 || records[0][0] != "id" || records[1][3] != "evil01" {
			t.Fatalf("sessions csv = %v, %v", records, err)
		}

		_, body = getRaw(t, e, "/api/login-attempts.csv?username="+url.QueryEscape(`=HYPERLINK("http://x")`), nil)
		records, err = csv.NewReader(strings.NewReader(body)).ReadAll()
		if err != nil || len(records) != 2 || records[1][8] != `'=HYPERLINK("http://x")` || records[1][9] != "'-1+1" {
			t.Fatalf("login attempts csv = %v, %v", records, err)
		}
		get(t, e, "/api/sessions.csv?from=yesterday", http.StatusBadRequest, nil)
	})
}

// The API answers JSON only, whatever the client accepts.
func TestJSONOnly(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	for path, code := range map[string]int{"/api/sensors": http.StatusOK, "/api/nowhere": http.StatusNotFound} {
		req, err := http.NewRequest("GET", e.srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Accept", "application/xml")
		resp, err := e.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != code || resp.Header.Get("Content-Type") != "application/json" || !json.Valid(body) {
			t.Errorf("%s = %d %s %s", path, resp.StatusCode, resp.Header.Get("Content-Type"), body)
		}
	}
}

// Without a recording, chapters are the commands without output; once the
// file is removed from the volume the stored recording is replaced the same way.
func TestSimulatedReplay(t *testing.T) {
	dir := t.TempDir()
	e := setup(t, storetest.SQLite(t), func(c *api.Config) {
		tty, err := ttystore.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		c.TTY, c.Ingest = tty, ingest.New(c.DB, tty, 1<<16)
	})
	ctx := context.Background()
	if _, err := e.client.SendEvents(ctx, events("hp-01")); err != nil {
		t.Fatal(err)
	}
	id := sid(t, e, "hp-01", "a1b2c3d4e5f6")
	type chapters struct {
		Simulated bool `json:"simulated"`
		Chapters  []struct {
			Time    float64 `json:"time"`
			Command string  `json:"command"`
			Output  string  `json:"output"`
		} `json:"chapters"`
	}
	var got chapters
	get(t, e, "/api/sessions/"+id+"/chapters", http.StatusOK, &got)
	if !got.Simulated || len(got.Chapters) != 1 || got.Chapters[0].Command != "id" || got.Chapters[0].Output != "" {
		t.Fatalf("simulated chapters = %+v", got)
	}
	// After the banner and its pause, so that chapters match the replay.
	if got.Chapters[0].Time != 0.5 {
		t.Errorf("first chapter at %v s, want 0.5", got.Chapters[0].Time)
	}
	if _, body := getRaw(t, e, "/api/sessions/"+id+"/cast", nil); !strings.Contains(body, "### simulated session ###") ||
		!strings.Contains(body, `[0.5, "m", "id"]`) {
		t.Errorf("simulated cast = %s", body)
	}

	// With the recording the replay is real, and no longer after removal.
	m, data := realRecording(t)
	if _, err := e.client.SendTTY(ctx, m, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	got = chapters{}
	get(t, e, "/api/sessions/"+id+"/chapters", http.StatusOK, &got)
	if got.Simulated {
		t.Fatal("recording stored but replay simulated")
	}
	// The recording exists, and the client may still ask for the simulation.
	if resp, _ := getRaw(t, e, "/api/sessions/"+id+"/cast", nil); resp.header.Get("X-Cauri-Replay") != "recording" {
		t.Errorf("replay = %q, want the recording", resp.header.Get("X-Cauri-Replay"))
	}
	resp, body := getRaw(t, e, "/api/sessions/"+id+"/cast?simulated=1", nil)
	if resp.code != http.StatusOK || resp.header.Get("X-Cauri-Replay") != "simulated" ||
		resp.header.Get("ETag") != "" || !strings.Contains(body, `"o", "# id\r\n"]`) {
		t.Errorf("forced simulation: %d %q %s", resp.code, resp.header.Get("X-Cauri-Replay"), body)
	}
	got = chapters{}
	get(t, e, "/api/sessions/"+id+"/chapters?simulated=1", http.StatusOK, &got)
	if !got.Simulated || len(got.Chapters) != 1 || got.Chapters[0].Output != "" {
		t.Errorf("forced chapters = %+v", got)
	}
	if resp, _ := getRaw(t, e, "/api/sessions/"+id+"/cast?simulated=maybe", nil); resp.code != http.StatusBadRequest {
		t.Errorf("simulated=maybe: %d", resp.code)
	}
	got = chapters{}
	get(t, e, "/api/sessions/"+id+"/chapters", http.StatusOK, &got)
	if err := os.RemoveAll(filepath.Join(dir, m.SHA256[:2])); err != nil {
		t.Fatal(err)
	}
	got = chapters{}
	get(t, e, "/api/sessions/"+id+"/chapters", http.StatusOK, &got)
	if !got.Simulated || len(got.Chapters) != 1 {
		t.Fatalf("after removal: %+v", got)
	}
	if resp, body := getRaw(t, e, "/api/sessions/"+id+"/cast", nil); resp.code != http.StatusOK || !strings.Contains(body, `"m", "id"]`) {
		t.Errorf("cast after removal: %d %s", resp.code, body)
	}
	// The record of the missing file is gone, the session no longer has it.
	if n, _ := e.db.NewSelect().Model((*store.TTYFile)(nil)).Count(ctx); n != 0 {
		t.Errorf("%d recordings still recorded", n)
	}
	var detail struct {
		TTYAvailable bool `json:"tty_available"`
	}
	get(t, e, "/api/sessions/"+id, http.StatusOK, &detail)
	if detail.TTYAvailable {
		t.Error("session still has a recording")
	}
}

// A volume that is not there says nothing of the files: nothing is forgotten.
func TestMissingVolumeKeepsRecordings(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tty")
	e := setup(t, storetest.SQLite(t), func(c *api.Config) {
		tty, err := ttystore.New(dir)
		if err != nil {
			t.Fatal(err)
		}
		c.TTY, c.Ingest = tty, ingest.New(c.DB, tty, 1<<16)
	})
	ctx := context.Background()
	if _, err := e.client.SendEvents(ctx, events("hp-01")); err != nil {
		t.Fatal(err)
	}
	m, data := realRecording(t)
	if _, err := e.client.SendTTY(ctx, m, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	id := sid(t, e, "hp-01", "a1b2c3d4e5f6")
	if resp, _ := getRaw(t, e, "/api/sessions/"+id+"/cast", nil); resp.code != http.StatusOK {
		t.Fatalf("cast = %d", resp.code)
	}
	if n, _ := e.db.NewSelect().Model((*store.TTYFile)(nil)).Count(ctx); n != 1 {
		t.Errorf("%d recordings recorded, want 1", n)
	}
}

// A session with neither recording nor command has nothing to replay.
func TestNothingToReplay(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	raw := []byte(`{"eventid":"cowrie.session.connect","session":"b1b2b3b4b5b6","src_ip":"203.0.113.9","timestamp":"2026-10-14T09:00:00Z"}`)
	b := protocol.EventBatch{SensorID: "hp-01", Events: []protocol.Event{{ID: protocol.EventID("hp-01", raw), Raw: raw}}}
	if _, err := e.client.SendEvents(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	id := sid(t, e, "hp-01", "b1b2b3b4b5b6")
	for _, p := range []string{"/cast", "/chapters"} {
		if resp, body := getRaw(t, e, "/api/sessions/"+id+p, nil); resp.code != http.StatusNotFound || !strings.Contains(body, "nothing") && !strings.Contains(body, "no command") {
			t.Errorf("%s = %d %s", p, resp.code, body)
		}
	}
}

// Attackers are the source addresses, their sessions grouped.
func TestAttackers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, nil)
		ctx := context.Background()
		if _, err := e.client.SendEvents(ctx, events("hp-01")); err != nil {
			t.Fatal(err)
		}
		// A second session of the same address, and one over IPv6.
		b := protocol.EventBatch{SensorID: "hp-01"}
		for _, l := range []string{
			`{"eventid":"cowrie.session.connect","session":"b1b2c3d4e5f6","src_ip":"203.0.113.9","timestamp":"2026-10-14T10:00:00Z"}`,
			`{"eventid":"cowrie.session.connect","session":"c1b2c3d4e5f6","src_ip":"2001:db8::1","timestamp":"2026-10-14T11:00:00Z"}`,
		} {
			b.Events = append(b.Events, protocol.Event{ID: protocol.EventID("hp-01", []byte(l)), Raw: []byte(l)})
		}
		if _, err := e.client.SendEvents(ctx, b); err != nil {
			t.Fatal(err)
		}

		var list struct {
			Items []store.Attacker `json:"items"`
			Total int              `json:"total"`
		}
		get(t, e, "/api/attackers", http.StatusOK, &list)
		if list.Total != 2 || list.Items[0].IP != "203.0.113.9" || list.Items[0].Sessions != 2 || list.Items[0].LoggedIn != 1 {
			t.Fatalf("attackers = %+v", list)
		}
		get(t, e, "/api/attackers?logged_in=true", http.StatusOK, &list)
		if list.Total != 1 {
			t.Errorf("with a shell = %+v", list)
		}
		get(t, e, "/api/attackers?ip_prefix=2001:", http.StatusOK, &list)
		if list.Total != 1 || list.Items[0].IP != "2001:db8::1" {
			t.Errorf("by prefix = %+v", list)
		}
		get(t, e, "/api/attackers?sort=name", http.StatusBadRequest, nil)
		get(t, e, "/api/attackers?logged_in=maybe", http.StatusBadRequest, nil)

		var d store.AttackerDetail
		get(t, e, "/api/attackers/203.0.113.9", http.StatusOK, &d)
		if d.Sessions != 2 || !slices.Equal(d.Sensors, []string{"hp-01"}) || d.Logins != 1 {
			t.Errorf("attacker = %+v", d)
		}
		get(t, e, "/api/attackers/2001:db8::1", http.StatusOK, &d)
		if d.IP != "2001:db8::1" {
			t.Errorf("IPv6 attacker = %+v", d)
		}
		get(t, e, "/api/attackers/198.51.100.1", http.StatusNotFound, nil)

		// Statistics narrow to one attacker.
		var sum store.Summary
		get(t, e, "/api/stats/summary?ip=203.0.113.9", http.StatusOK, &sum)
		if sum.Sessions != 2 || sum.SourceIPs != 1 {
			t.Errorf("summary of 203.0.113.9 = %+v", sum)
		}
	})
}
