// Package client is an HTTP client for the ingestion API described in
// package protocol.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"

	"github.com/devleesch001/cauri/pkg/protocol"
)

// Client sends events and TTY recordings to a cauri API.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// New returns a client for the API at baseURL (for example
// "https://cauri.example.org"), authenticated with token. A nil httpClient
// means http.DefaultClient.
func New(baseURL, token string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSuffix(baseURL, "/"))
	if err != nil {
		return nil, fmt.Errorf("api url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("api url %q: want http(s)://host[/path]", baseURL)
	}
	if token == "" {
		return nil, errors.New("empty token")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{base: u, token: token, http: httpClient}, nil
}

// StatusError is returned when the API answers with an error status.
type StatusError struct {
	Code    int
	Message string
	// FromAPI is false when the answer is not in the API's error format
	// (protocol.ErrorBody): something between the client and the API, such
	// as a reverse proxy, answered. Message then holds the start of its body.
	FromAPI bool
}

func (e *StatusError) Error() string {
	if !e.FromAPI {
		return fmt.Sprintf("api: %d %s, not answered by the cauri API (a proxy in between?): %s",
			e.Code, http.StatusText(e.Code), e.Message)
	}
	return fmt.Sprintf("api: %d %s: %s", e.Code, http.StatusText(e.Code), e.Message)
}

// maxForeignBody bounds what StatusError keeps of an answer that is not the
// API's: a proxy may send a whole HTML page.
const maxForeignBody = 300

// Retryable reports whether sending the same request later may succeed.
// Network errors and server-side errors are retryable; so is 404 on a TTY
// upload, which means the API has not yet seen the session.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var se *StatusError
	if !errors.As(err, &se) {
		return !errors.Is(err, context.Canceled)
	}
	return se.Code >= 500 || se.Code == http.StatusTooManyRequests ||
		se.Code == http.StatusNotFound || se.Code == http.StatusRequestTimeout
}

// SendEvents posts a batch of events.
func (c *Client) SendEvents(ctx context.Context, b protocol.EventBatch) (protocol.EventBatchResult, error) {
	var res protocol.EventBatchResult
	body, err := json.Marshal(b)
	if err != nil {
		return res, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(protocol.EventsPath), bytes.NewReader(body))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	err = c.do(req, &res)
	return res, err
}

// SendTTY uploads a TTY recording. body must yield exactly m.Size bytes
// whose SHA-256 is m.SHA256.
func (c *Client) SendTTY(ctx context.Context, m protocol.TTYMeta, body io.Reader) (protocol.TTYResult, error) {
	var res protocol.TTYResult
	digest, err := protocol.FormatDigest(m.SHA256)
	if err != nil {
		return res, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.url(protocol.TTYPath(url.PathEscape(m.Session))), body)
	if err != nil {
		return res, err
	}
	req.ContentLength = m.Size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set(protocol.HeaderDigest, digest)
	req.Header.Set(protocol.HeaderSensor, m.SensorID)
	err = c.do(req, &res)
	return res, err
}

// SendTTYChannels uploads the recordings of a session's channels at once,
// at most protocol.MaxTTYChannels files: the API merges them into the
// session's recording. Each file's Body must yield exactly its Size bytes,
// whose SHA-256 is its SHA256. The body is streamed, read once.
func (c *Client) SendTTYChannels(ctx context.Context, sensorID, session string, files []protocol.TTYFile) (protocol.TTYResult, error) {
	var res protocol.TTYResult
	digests := make([]string, len(files))
	for i, f := range files {
		d, err := protocol.FormatDigest(f.SHA256)
		if err != nil {
			return res, err
		}
		digests[i] = d
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		pw.CloseWithError(writeParts(mw, files, digests))
	}()
	defer func() { _ = pr.Close() }()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url(protocol.TTYChannelsPath(url.PathEscape(session))), pr)
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "multipart/mixed; boundary="+mw.Boundary())
	req.Header.Set(protocol.HeaderSensor, sensorID)
	err = c.do(req, &res)
	return res, err
}

func writeParts(mw *multipart.Writer, files []protocol.TTYFile, digests []string) error {
	for i, f := range files {
		h := textproto.MIMEHeader{}
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Length", strconv.FormatInt(f.Size, 10))
		h.Set(protocol.HeaderDigest, digests[i])
		w, err := mw.CreatePart(h)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, io.LimitReader(f.Body, f.Size)); err != nil {
			return err
		}
	}
	return mw.Close()
}

func (c *Client) url(path string) string { return c.base.String() + path }

func (c *Client) do(req *http.Request, out any) error {
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var eb protocol.ErrorBody
		if json.Unmarshal(data, &eb) == nil && eb.Error != "" {
			return &StatusError{Code: resp.StatusCode, Message: eb.Error, FromAPI: true}
		}
		msg := strings.ToValidUTF8(strings.TrimSpace(string(data)), "")
		if len(msg) > maxForeignBody {
			msg = strings.ToValidUTF8(msg[:maxForeignBody], "") + "…"
		}
		return &StatusError{Code: resp.StatusCode, Message: msg}
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("api: decode response: %w", err)
	}
	return nil
}
