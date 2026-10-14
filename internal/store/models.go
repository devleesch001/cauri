package store

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/uptrace/bun"
)

// All times are stored in UTC: SQLite compares them as text.

type Sensor struct {
	bun.BaseModel `bun:"table:sensors"`

	ID        string    `bun:"id,pk" json:"id"`
	FirstSeen time.Time `bun:"first_seen" json:"first_seen"`
	LastSeen  time.Time `bun:"last_seen" json:"last_seen"`
}

type IngestToken struct {
	bun.BaseModel `bun:"table:ingest_tokens"`

	ID         string     `bun:"id,pk"`
	SensorID   string     `bun:"sensor_id"`
	Name       string     `bun:"name"`
	TokenHash  string     `bun:"token_hash"`
	CreatedAt  time.Time  `bun:"created_at"`
	LastUsedAt *time.Time `bun:"last_used_at"`
	RevokedAt  *time.Time `bun:"revoked_at"`
}

type TTYFile struct {
	bun.BaseModel `bun:"table:tty_files"`

	SHA256   string    `bun:"sha256,pk"`
	Size     int64     `bun:"size"`
	Path     string    `bun:"path"`
	StoredAt time.Time `bun:"stored_at"`
}

type Session struct {
	bun.BaseModel `bun:"table:sessions"`

	ID            string     `bun:"id,pk" json:"id"`
	SensorID      string     `bun:"sensor_id" json:"sensor_id"`
	Session       string     `bun:"session" json:"session"`
	SrcIP         *string    `bun:"src_ip" json:"src_ip"`
	SrcPort       *int       `bun:"src_port" json:"src_port"`
	DstIP         *string    `bun:"dst_ip" json:"dst_ip"`
	DstPort       *int       `bun:"dst_port" json:"dst_port"`
	Protocol      *string    `bun:"protocol" json:"protocol"`
	ClientVersion *string    `bun:"client_version" json:"client_version"`
	HASSH         *string    `bun:"hassh" json:"hassh"`
	StartedAt     time.Time  `bun:"started_at" json:"started_at"`
	EndedAt       *time.Time `bun:"ended_at" json:"ended_at"`
	Duration      *float64   `bun:"duration" json:"duration"`
	LoginSuccess  bool       `bun:"login_success" json:"login_success"`
	TTYSHA256     *string    `bun:"tty_sha256" json:"tty_sha256"`
	TTYSize       *int64     `bun:"tty_size" json:"tty_size"`
	TTYDuration   *float64   `bun:"tty_duration" json:"tty_duration"`
}

// SessionID returns the internal identifier of a Cowrie session. Cowrie
// session identifiers are only unique within a sensor.
func SessionID(sensorID, session string) string {
	sum := sha256.Sum256([]byte(sensorID + "\n" + session))
	return hex.EncodeToString(sum[:16])
}

type RawEvent struct {
	bun.BaseModel `bun:"table:raw_events"`

	ID         string    `bun:"id,pk" json:"id"`
	SensorID   string    `bun:"sensor_id" json:"sensor_id"`
	SessionID  *string   `bun:"session_id" json:"session_id"`
	EventID    string    `bun:"eventid" json:"eventid"`
	TS         time.Time `bun:"ts" json:"ts"`
	SrcIP      *string   `bun:"src_ip" json:"src_ip"`
	Data       string    `bun:"data" json:"-"`
	ReceivedAt time.Time `bun:"received_at" json:"received_at"`
}

type LoginAttempt struct {
	bun.BaseModel `bun:"table:login_attempts"`

	ID        string    `bun:"id,pk" json:"id"`
	SessionID string    `bun:"session_id" json:"session_id"`
	TS        time.Time `bun:"ts" json:"ts"`
	Username  string    `bun:"username" json:"username"`
	Password  string    `bun:"password" json:"password"`
	Success   bool      `bun:"success" json:"success"`
}

type Command struct {
	bun.BaseModel `bun:"table:commands"`

	ID        string    `bun:"id,pk" json:"id"`
	SessionID string    `bun:"session_id" json:"session_id"`
	TS        time.Time `bun:"ts" json:"ts"`
	Input     string    `bun:"input" json:"input"`
}

// Transfer directions.
const (
	Download = "download"
	Upload   = "upload"
)

type Transfer struct {
	bun.BaseModel `bun:"table:transfers"`

	ID        string    `bun:"id,pk" json:"id"`
	SessionID string    `bun:"session_id" json:"session_id"`
	TS        time.Time `bun:"ts" json:"ts"`
	Direction string    `bun:"direction" json:"direction"`
	URL       *string   `bun:"url" json:"url"`
	Filename  *string   `bun:"filename" json:"filename"`
	SHA256    *string   `bun:"sha256" json:"sha256"`
}
