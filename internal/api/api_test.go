package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"cauri/internal/api"
	"cauri/internal/auth"
	"cauri/internal/ingest"
	"cauri/internal/store"
	"cauri/internal/store/storetest"
	"cauri/internal/ttystore"
	"cauri/pkg/client"
	"cauri/pkg/protocol"
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
		DB: db, Ingest: ingest.New(db, tty, 1024), Tokens: tokens,
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

	// Larger than the limit of the test service (1024 bytes).
	big := strings.Repeat("x", 2048)
	sum := sha256.Sum256([]byte(big))
	m := protocol.TTYMeta{SensorID: "hp-01", Session: "a1b2c3d4e5f6", SHA256: hex.EncodeToString(sum[:]), Size: 2048}
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
		if len(sensors) != 1 || sensors[0].ID != "hp-01" {
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
		get(t, e, "/api/sessions/"+id, http.StatusOK, &detail)
		if len(detail.Events) != 4 || len(detail.CommandList) != 1 || *detail.SrcPort != 4242 {
			t.Errorf("detail = %+v", detail)
		}
		get(t, e, "/api/sessions/unknown", http.StatusNotFound, nil)

		var attempts struct {
			Items []store.LoginAttemptRow `json:"items"`
		}
		get(t, e, "/api/login-attempts?success=true", http.StatusOK, &attempts)
		if len(attempts.Items) != 1 || attempts.Items[0].SensorID != "hp-01" {
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
