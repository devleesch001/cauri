package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/api"
	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/store/storetest"
	"github.com/devleesch001/cauri/pkg/client"
)

// adminReply is what a test reads of a response.
type adminReply struct {
	code    int
	cookies []*http.Cookie
	body    []byte
}

// adminDo sends a request to the administration routes, with the session
// cookie if any and the administration header if header.
func adminDo(t *testing.T, e *env, method, path, body string, session *http.Cookie, header bool) adminReply {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if session != nil {
		req.AddCookie(session)
	}
	if header {
		req.Header.Set(api.AdminHeader, "1")
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return adminReply{resp.StatusCode, resp.Cookies(), b}
}

func TestAdmin(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		e := setup(t, db, func(c *api.Config) { c.Admin = auth.NewAdmin("s3cret") })
		code := func(method, path, body string, session *http.Cookie, header bool) int {
			return adminDo(t, e, method, path, body, session, header).code
		}

		// Signing in.
		if c := code("POST", "/api/admin/login", `{"password":"s3cret"}`, nil, false); c != http.StatusForbidden {
			t.Errorf("login without the header = %d", c)
		}
		if c := code("POST", "/api/admin/login", `not json`, nil, true); c != http.StatusBadRequest {
			t.Errorf("bad body = %d", c)
		}
		login := adminDo(t, e, "POST", "/api/admin/login", `{"password":"s3cret"}`, nil, true)
		if login.code != http.StatusNoContent || len(login.cookies) != 1 {
			t.Fatalf("login = %d, cookies %v", login.code, login.cookies)
		}
		session := login.cookies[0]
		if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteStrictMode || session.Path != "/api/admin" {
			t.Errorf("session cookie = %+v", session)
		}

		// The session opens the routes; nothing else does.
		if c := code("GET", "/api/admin/session", "", nil, false); c != http.StatusUnauthorized {
			t.Errorf("no session = %d", c)
		}
		forged := &http.Cookie{Name: session.Name, Value: session.Value + "x"}
		if c := code("GET", "/api/admin/tokens", "", forged, false); c != http.StatusUnauthorized {
			t.Errorf("forged session = %d", c)
		}
		if c := code("GET", "/api/admin/session", "", session, false); c != http.StatusNoContent {
			t.Errorf("session = %d", c)
		}

		// Tokens: listed without their hash, created, revoked.
		body := adminDo(t, e, "GET", "/api/admin/tokens", "", session, false).body
		var list []map[string]any
		if err := json.Unmarshal(body, &list); err != nil || len(list) != 1 || list[0]["sensor"] != "hp-01" || list[0]["name"] != "test" {
			t.Fatalf("tokens = %s", body)
		}
		if strings.Contains(strings.ToLower(string(body)), "hash") {
			t.Errorf("tokens show their hash: %s", body)
		}
		if c := code("POST", "/api/admin/tokens", `{"sensor":"hp-02","name":"edge"}`, session, false); c != http.StatusForbidden {
			t.Errorf("create without the header = %d", c)
		}
		if c := code("POST", "/api/admin/tokens", `{"sensor":"hp 02"}`, session, true); c != http.StatusBadRequest {
			t.Errorf("invalid sensor = %d", c)
		}
		reply := adminDo(t, e, "POST", "/api/admin/tokens", `{"sensor":"hp-02","name":" edge "}`, session, true)
		var created struct {
			Token struct {
				ID     int64  `json:"id"`
				Sensor string `json:"sensor"`
				Name   string `json:"name"`
			} `json:"token"`
			Secret string `json:"secret"`
		}
		if err := json.Unmarshal(reply.body, &created); err != nil || reply.code != http.StatusCreated ||
			created.Token.Sensor != "hp-02" || created.Token.Name != "edge" || created.Secret == "" {
			t.Fatalf("create = %d %s", reply.code, reply.body)
		}
		// The new token ingests for its sensor, until revoked.
		c, err := client.New(e.srv.URL, created.Secret, e.srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.SendEvents(context.Background(), events("hp-02")); err != nil {
			t.Errorf("ingesting with the new token: %v", err)
		}
		revoke := "/api/admin/tokens/" + strconv.FormatInt(created.Token.ID, 10) + "/revoke"
		if c := code("POST", revoke, "", session, false); c != http.StatusForbidden {
			t.Errorf("revoke without the header = %d", c)
		}
		if c := code("POST", revoke, "", session, true); c != http.StatusNoContent {
			t.Errorf("revoke = %d", c)
		}
		if _, err := c.SendEvents(context.Background(), events("hp-02")); err == nil {
			t.Error("revoked token still ingests")
		}
		if c := code("POST", "/api/admin/tokens/999/revoke", "", session, true); c != http.StatusNotFound {
			t.Errorf("revoke unknown = %d", c)
		}

		// Signing out clears the cookie.
		logout := adminDo(t, e, "POST", "/api/admin/logout", "", session, true)
		if logout.code != http.StatusNoContent || len(logout.cookies) != 1 || logout.cookies[0].MaxAge >= 0 {
			t.Errorf("logout = %d, cookies %v", logout.code, logout.cookies)
		}

		// Last: a wrong guess blocks this client for a while.
		if c := code("POST", "/api/admin/login", `{"password":"wrong"}`, nil, true); c != http.StatusUnauthorized {
			t.Errorf("wrong password = %d", c)
		}
	})
}

// login signs in from the client forwardedFor names, through the test
// server, a trusted proxy when the test sets it.
func login(t *testing.T, e *env, password, forwardedFor string) int {
	t.Helper()
	req, err := http.NewRequest("POST", e.srv.URL+"/api/admin/login", strings.NewReader(`{"password":"`+password+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(api.AdminHeader, "1")
	req.Header.Set("X-Forwarded-For", forwardedFor)
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// A wrong guess blocks its client for a second, and only it: a burst of
// guesses cannot try many at once, nor lock others out.
func TestAdminGuessesPerClient(t *testing.T) {
	e := setup(t, storetest.SQLite(t), func(c *api.Config) {
		c.Admin = auth.NewAdmin("s3cret")
		c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	})
	if c := login(t, e, "guess", "198.51.100.1"); c != http.StatusUnauthorized {
		t.Fatalf("wrong guess = %d", c)
	}
	if c := login(t, e, "s3cret", "198.51.100.1"); c != http.StatusTooManyRequests {
		t.Errorf("same client right after = %d, want 429", c)
	}
	if c := login(t, e, "s3cret", "198.51.100.2"); c != http.StatusNoContent {
		t.Errorf("another client = %d", c)
	}
	time.Sleep(time.Second)
	if c := login(t, e, "s3cret", "198.51.100.1"); c != http.StatusNoContent {
		t.Errorf("same client a second later = %d", c)
	}
}

// Without trusted proxies, X-Forwarded-For cannot make a guesser another
// client.
func TestAdminForwardedForIgnored(t *testing.T) {
	e := setup(t, storetest.SQLite(t), func(c *api.Config) { c.Admin = auth.NewAdmin("s3cret") })
	if c := login(t, e, "guess", "198.51.100.1"); c != http.StatusUnauthorized {
		t.Fatalf("wrong guess = %d", c)
	}
	if c := login(t, e, "s3cret", "198.51.100.2"); c != http.StatusTooManyRequests {
		t.Errorf("forged other client = %d, want 429", c)
	}
}

// Without an administration password, the routes do not exist.
func TestAdminDisabled(t *testing.T) {
	e := setup(t, storetest.SQLite(t), nil)
	if r := adminDo(t, e, "POST", "/api/admin/login", `{"password":""}`, nil, true); r.code != http.StatusNotFound {
		t.Errorf("login = %d", r.code)
	}
}
