// Package cowrie knows what cauri keeps of a cowrie.json line besides the
// line itself: the fields projected into tables, the rest kept with the
// event (Fragment), and the SSH key exchange the client offered (Kex). The
// ingestion service and the migration that projected older lines share it,
// so that both store the same thing.
package cowrie

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// Cowrie event identifiers projected into dedicated tables. Every other
// event is kept in events, its fields in events.data.
const (
	Connect       = "cowrie.session.connect"
	ClientVersion = "cowrie.client.version"
	ClientKex     = "cowrie.client.kex"
	LoginSuccess  = "cowrie.login.success"
	LoginFailed   = "cowrie.login.failed"
	CommandInput  = "cowrie.command.input"
	FileDownload  = "cowrie.session.file_download"
	FileUpload    = "cowrie.session.file_upload"
	LogClosed     = "cowrie.log.closed"
	SessionClosed = "cowrie.session.closed"
)

// Fields are the members of a cowrie.json object, decoded on demand.
type Fields map[string]json.RawMessage

// Parse decodes a cowrie.json line; ok is false if it is not an object.
func Parse(raw []byte) (Fields, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, false
	}
	var f Fields
	if json.Unmarshal(raw, &f) != nil {
		return nil, false
	}
	return f, true
}

// kind is what a projected field must hold to be in its column: a value of
// another type leaves the column empty (lenient parsing), so it stays in
// the fragment.
type kind int

const (
	text   kind = iota // a JSON string
	number             // a number, or a string holding one
	list               // an array of strings
)

// envelope are the fields of every line that the event row or its session
// hold, or that only identify the Cowrie process (sensor, uuid). Cowrie's
// message restates the other fields.
var envelope = map[string]bool{"eventid": true, "timestamp": true, "session": true, "sensor": true, "uuid": true, "message": true}

// connection are the fields Cowrie copies on every line of a session: the
// session row holds them, so they leave only lines that have one.
var connection = map[string]bool{"src_ip": true, "src_port": true, "dst_ip": true, "dst_port": true, "protocol": true}

// projected are the fields each event type stores in a table, one value
// per event. log.closed is not here: a session keeps only its last
// recording name and duration, not those of each channel.
var projected = map[string]map[string]kind{
	ClientVersion: {"version": text},
	ClientKex: {
		"hassh": text, "hasshAlgorithms": text, "kexAlgs": list, "encCS": list, "macCS": list,
		"compCS": list, "keyAlgs": list, "langCS": list,
	},
	LoginSuccess:  {"username": text, "password": text},
	LoginFailed:   {"username": text, "password": text},
	CommandInput:  {"input": text},
	FileDownload:  {"url": text, "destfile": text, "shasum": text},
	FileUpload:    {"filename": text, "shasum": text},
	SessionClosed: {"duration_ms": number, "duration": number},
}

// Fragment returns what the tables do not hold of an event of a session
// (inSession) or not, as a JSON object, or nil if nothing is left. It is
// kept with the event so that the raw line can be purged without losing
// anything: public keys offered, terminal sizes, environment variables,
// password changes, and the events Cowrie may add.
func Fragment(f Fields, eventType string, inSession bool) *string {
	rest := map[string]json.RawMessage{}
	for k, v := range f {
		if envelope[k] || inSession && connection[k] {
			continue
		}
		if want, ok := projected[eventType][k]; ok && holds(v, want) {
			continue
		}
		rest[k] = v
	}
	if len(rest) == 0 {
		return nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(rest) != nil {
		return nil
	}
	s := strings.TrimSuffix(b.String(), "\n")
	return &s
}

func holds(v json.RawMessage, want kind) bool {
	switch want {
	case text:
		var s string
		return json.Unmarshal(v, &s) == nil
	case number:
		var n float64
		var s string
		return json.Unmarshal(v, &n) == nil || json.Unmarshal(v, &s) == nil
	default:
		var l []string
		return json.Unmarshal(v, &l) == nil
	}
}

// Kex is what a client offered in the SSH key exchange (cowrie.client.kex),
// stored once per combination in ssh_kex.
type Kex struct {
	HASSH *string
	// Algorithms is what the HASSH hashes: key exchange, encryption, MAC
	// and compression lists, separated by ";".
	Algorithms *string
	KeyAlgs    *string // host key algorithms, not in the HASSH
	LangAlgs   *string
	// Digest is the SHA-256 of all of the above: one row per combination.
	// The lists come from the attacker, of any length: a digest keeps the
	// unique key small, and SHA-256 cannot be made to collide.
	Digest []byte
}

// ParseKex reads the key exchange of a cowrie.client.kex line.
func ParseKex(f Fields) Kex {
	k := Kex{HASSH: str(f, "hassh"), KeyAlgs: joined(f, "keyAlgs"), LangAlgs: joined(f, "langCS")}
	parts := []*string{joined(f, "kexAlgs"), joined(f, "encCS"), joined(f, "macCS"), joined(f, "compCS")}
	if parts[0] != nil && parts[1] != nil && parts[2] != nil && parts[3] != nil {
		s := *parts[0] + ";" + *parts[1] + ";" + *parts[2] + ";" + *parts[3]
		k.Algorithms = &s
	} else {
		k.Algorithms = str(f, "hasshAlgorithms")
	}
	k.digest()
	return k
}

// HASSHOnly is the key exchange of a session known only by its HASSH, its
// line purged before cauri kept the lists.
func HASSHOnly(hassh string) Kex {
	k := Kex{HASSH: &hassh}
	k.digest()
	return k
}

func (k *Kex) digest() {
	// Absent and empty differ in the digest.
	b, _ := json.Marshal([]*string{k.HASSH, k.Algorithms, k.KeyAlgs, k.LangAlgs})
	sum := sha256.Sum256(b)
	k.Digest = sum[:]
}

func str(f Fields, key string) *string {
	var s string
	if v, ok := f[key]; !ok || json.Unmarshal(v, &s) != nil {
		return nil
	}
	s = CleanText(s)
	return &s
}

func joined(f Fields, key string) *string {
	var l []string
	if v, ok := f[key]; !ok || json.Unmarshal(v, &l) != nil || l == nil {
		return nil
	}
	s := CleanText(strings.Join(l, ","))
	return &s
}

// CleanText makes a string storable in every database: PostgreSQL rejects
// NUL bytes and invalid UTF-8 in TEXT columns, and attackers do send them
// (as usernames, passwords or commands).
func CleanText(s string) string {
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	return strings.ReplaceAll(s, "\x00", string(utf8.RuneError))
}
