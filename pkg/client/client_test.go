package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devleesch001/cauri/pkg/protocol"
)

func TestSendEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/base"+protocol.EventsPath {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("missing token")
		}
		var b protocol.EventBatch
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.SensorID != "hp-01" || len(b.Events) != 1 {
			t.Errorf("bad batch %+v: %v", b, err)
		}
		_ = json.NewEncoder(w).Encode(protocol.EventBatchResult{Accepted: 1})
	}))
	defer srv.Close()

	c, err := New(srv.URL+"/base/", "tok", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.SendEvents(context.Background(), protocol.EventBatch{
		SensorID: "hp-01",
		Events:   []protocol.Event{{ID: "x", Raw: json.RawMessage(`{}`)}},
	})
	if err != nil || res.Accepted != 1 {
		t.Fatalf("SendEvents = %+v, %v", res, err)
	}
}

func TestSendTTY(t *testing.T) {
	content := "tty content"
	sum := sha256.Sum256([]byte(content))
	hexSum := hex.EncodeToString(sum[:])

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != protocol.TTYPath("abc123") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		got, err := protocol.ParseDigest(r.Header.Get(protocol.HeaderDigest))
		if err != nil || got != hexSum {
			t.Errorf("digest %q, %v", got, err)
		}
		if r.Header.Get(protocol.HeaderSensor) != "hp-01" || r.ContentLength != int64(len(content)) {
			t.Errorf("sensor %q, length %d", r.Header.Get(protocol.HeaderSensor), r.ContentLength)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != content {
			t.Errorf("body %q", body)
		}
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(protocol.TTYResult{Status: protocol.TTYStored, SHA256: hexSum})
	}))
	defer srv.Close()

	c, _ := New(srv.URL, "tok", nil)
	res, err := c.SendTTY(context.Background(), protocol.TTYMeta{
		SensorID: "hp-01", Session: "abc123", SHA256: hexSum, Size: int64(len(content)),
	}, strings.NewReader(content))
	if err != nil || res.Status != protocol.TTYStored {
		t.Fatalf("SendTTY = %+v, %v", res, err)
	}
}

func TestErrors(t *testing.T) {
	for code, retry := range map[int]bool{
		http.StatusBadRequest: false, http.StatusUnauthorized: false, http.StatusConflict: false,
		http.StatusNotFound: true, http.StatusTooManyRequests: true, http.StatusBadGateway: true,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(protocol.ErrorBody{Error: "nope"})
		}))
		c, _ := New(srv.URL, "tok", nil)
		_, err := c.SendEvents(context.Background(), protocol.EventBatch{})
		srv.Close()

		var se *StatusError
		if !errors.As(err, &se) || se.Code != code || se.Message != "nope" {
			t.Errorf("%d: err = %v", code, err)
		}
		if Retryable(err) != retry {
			t.Errorf("%d: Retryable = %v, want %v", code, !retry, retry)
		}
	}
}

func TestNetworkErrorRetryable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, _ := New(srv.URL, "tok", nil)
	_, err := c.SendEvents(context.Background(), protocol.EventBatch{})
	if err == nil || !Retryable(err) {
		t.Fatalf("err = %v, want retryable", err)
	}
}

func TestNewValidation(t *testing.T) {
	for _, u := range []string{"", "ftp://x", "cauri.example"} {
		if _, err := New(u, "tok", nil); err == nil {
			t.Errorf("New(%q): want error", u)
		}
	}
	if _, err := New("https://x", "", nil); err == nil {
		t.Error("want error for empty token")
	}
}
