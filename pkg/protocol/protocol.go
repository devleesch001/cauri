// Package protocol defines the contract between an ingestor and the cauri
// API: routes, headers, payloads and the identifiers both sides must agree
// on. Third-party tools can use it to feed an API without the cauri ingestor.
//
// Events: POST EventsPath with an EventBatch as JSON, answered by an
// EventBatchResult. Events are the raw lines of Cowrie's cowrie.json; the
// API parses them, the ingestor does not.
//
// TTY recordings: PUT TTYPath(session) with the raw file as body, the
// HeaderSensor header and a Content-Digest header (RFC 9530, sha-256),
// answered by a TTYResult. The upload is idempotent.
//
// The recordings of a session's channels: POST TTYChannelsPath(session)
// with a multipart/mixed body, one part per file (Content-Length and
// Content-Digest headers each), and the HeaderSensor header, answered by
// a TTYResult. Cowrie writes a recording per SSH channel; the API merges
// the files of a session into its one recording, once. The upload is
// idempotent.
//
// Every request carries "Authorization: Bearer <token>"; a token belongs to
// one sensor. Errors are answered with an ErrorBody.
package protocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	// EventsPath receives event batches (POST).
	EventsPath = "/api/ingest/events"
	// TTYPathPrefix is followed by the Cowrie session identifier (PUT).
	TTYPathPrefix = "/api/ingest/tty/"
	// TTYChannelsSuffix follows TTYPath(session) to upload the recordings
	// of the session's channels at once (POST).
	TTYChannelsSuffix = "/channels"

	// HeaderSensor carries the sensor identifier of a TTY upload.
	HeaderSensor = "Cauri-Sensor"
	// HeaderDigest carries the SHA-256 of a TTY upload, see FormatDigest.
	HeaderDigest = "Content-Digest"

	// MaxBatchEvents is the largest batch the API accepts.
	MaxBatchEvents = 1000
	// MaxBatchBytes is the largest event batch body the API accepts.
	MaxBatchBytes = 16 << 20
	// MaxTTYChannels is the largest number of files in an upload of a
	// session's channels: the attacker chooses how many it opens.
	MaxTTYChannels = 256
)

// TTYPath returns the upload path of the TTY recording of a session.
func TTYPath(session string) string { return TTYPathPrefix + session }

// TTYChannelsPath returns the upload path of the recordings of a
// session's channels.
func TTYChannelsPath(session string) string { return TTYPath(session) + TTYChannelsSuffix }

// Event is one line of cowrie.json.
type Event struct {
	// ID identifies the event for deduplication, see EventID.
	ID string `json:"id"`
	// Raw is the JSON object written by Cowrie, unmodified.
	Raw json.RawMessage `json:"raw"`
}

// EventBatch is the body of a POST to EventsPath.
type EventBatch struct {
	SensorID string  `json:"sensor_id"`
	Events   []Event `json:"events"`
}

// Rejection explains why one event of a batch was refused. Rejected events
// are not retried: the rest of the batch is still accepted.
type Rejection struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// EventBatchResult answers an EventBatch. Accepted + Duplicates +
// len(Rejected) equals the number of events sent.
type EventBatchResult struct {
	Accepted   int         `json:"accepted"`
	Duplicates int         `json:"duplicates"`
	Rejected   []Rejection `json:"rejected,omitempty"`
}

// TTYMeta describes a TTY recording upload.
type TTYMeta struct {
	SensorID string
	// Session is the Cowrie session identifier.
	Session string
	// SHA256 is the hex-encoded digest of the file.
	SHA256 string
	Size   int64
}

// TTYFile is one file of an upload of a session's channels: Body yields
// Size bytes whose SHA-256 is SHA256 (hex).
type TTYFile struct {
	SHA256 string
	Size   int64
	Body   io.Reader
}

// TTYStatus tells what the API did with an upload.
type TTYStatus string

const (
	// TTYStored means the file was written (HTTP 201).
	TTYStored TTYStatus = "stored"
	// TTYExists means the same file was already linked to the session (HTTP 200).
	TTYExists TTYStatus = "exists"
)

// TTYResult answers a TTY upload.
type TTYResult struct {
	Status TTYStatus `json:"status"`
	SHA256 string    `json:"sha256"`
}

// ErrorBody is the JSON body of every error response.
type ErrorBody struct {
	Error string `json:"error"`
}

// EventID returns the identifier of a cowrie.json line read by a sensor.
// Cowrie gives no identifier of its own, so the line itself, without its
// line terminator, is hashed with the sensor identifier.
func EventID(sensorID string, line []byte) string {
	line = bytes.TrimRight(line, "\r\n")
	h := sha256.New()
	h.Write([]byte(sensorID))
	h.Write([]byte{'\n'})
	h.Write(line)
	return hex.EncodeToString(h.Sum(nil))
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidSensorID reports whether s can be used as a sensor identifier:
// 1 to 64 characters among letters, digits, '.', '_' and '-'.
func ValidSensorID(s string) bool { return identRe.MatchString(s) }

// ValidSession reports whether s looks like a Cowrie session identifier;
// same rule as ValidSensorID.
func ValidSession(s string) bool { return identRe.MatchString(s) }

// ValidSHA256 reports whether s is a lower-case hex SHA-256.
func ValidSHA256(s string) bool {
	if len(s) != sha256.Size*2 || strings.ToLower(s) != s {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// FormatDigest returns the Content-Digest value for a hex SHA-256.
func FormatDigest(hexSHA256 string) (string, error) {
	sum, err := hex.DecodeString(hexSHA256)
	if err != nil || len(sum) != sha256.Size {
		return "", fmt.Errorf("invalid sha256 %q", hexSHA256)
	}
	return "sha-256=:" + base64.StdEncoding.EncodeToString(sum) + ":", nil
}

// ParseDigest extracts the hex SHA-256 from a Content-Digest value. Other
// algorithms listed in the header are ignored.
func ParseDigest(header string) (string, error) {
	for _, member := range strings.Split(header, ",") {
		name, value, ok := strings.Cut(strings.TrimSpace(member), "=")
		if !ok || strings.TrimSpace(name) != "sha-256" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) < 2 || value[0] != ':' || value[len(value)-1] != ':' {
			return "", errors.New("malformed sha-256 digest")
		}
		sum, err := base64.StdEncoding.DecodeString(value[1 : len(value)-1])
		if err != nil || len(sum) != sha256.Size {
			return "", errors.New("malformed sha-256 digest")
		}
		return hex.EncodeToString(sum), nil
	}
	return "", errors.New("no sha-256 digest")
}
