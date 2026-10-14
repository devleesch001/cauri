// Package auth manages the ingestor tokens. A token belongs to one sensor;
// only its SHA-256 is stored, and revoking it takes effect immediately.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"cauri/internal/store"
	"cauri/pkg/protocol"
)

var (
	// ErrUnauthorized means the token is unknown or revoked.
	ErrUnauthorized = errors.New("invalid or revoked token")
	// ErrNotFound means no token has the given identifier.
	ErrNotFound = errors.New("token not found")
)

// tokenPrefix makes tokens recognisable, for example by secret scanners.
const tokenPrefix = "cauri_"

// lastUsedPrecision limits last_used_at updates to one write per token and
// period instead of one per request.
const lastUsedPrecision = time.Minute

// Tokens stores ingestor tokens in the database.
type Tokens struct {
	db  *bun.DB
	now func() time.Time
}

// NewTokens returns a token store backed by db.
func NewTokens(db *bun.DB) *Tokens {
	return &Tokens{db: db, now: time.Now}
}

// Create issues a token for sensorID and returns its record and its secret
// value, which is not stored and cannot be shown again.
func (t *Tokens) Create(ctx context.Context, sensorID, name string) (store.IngestToken, string, error) {
	if !protocol.ValidSensorID(sensorID) {
		return store.IngestToken{}, "", fmt.Errorf("invalid sensor id %q: use 1 to 64 letters, digits, '.', '_' or '-'", sensorID)
	}
	id := make([]byte, 8)
	secret := make([]byte, 32)
	_, _ = rand.Read(id)
	_, _ = rand.Read(secret)
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(secret)

	rec := store.IngestToken{
		ID: hex.EncodeToString(id), SensorID: sensorID, Name: name,
		TokenHash: hash(token), CreatedAt: t.now().UTC(),
	}
	if _, err := t.db.NewInsert().Model(&rec).Exec(ctx); err != nil {
		return store.IngestToken{}, "", err
	}
	return rec, token, nil
}

// List returns every token, revoked ones included, oldest first.
func (t *Tokens) List(ctx context.Context) ([]store.IngestToken, error) {
	var recs []store.IngestToken
	err := t.db.NewSelect().Model(&recs).Order("created_at", "id").Scan(ctx)
	return recs, err
}

// Revoke revokes the token with the given identifier. Revoking a revoked
// token is a no-op.
func (t *Tokens) Revoke(ctx context.Context, id string) error {
	res, err := t.db.NewUpdate().Model((*store.IngestToken)(nil)).
		Set("revoked_at = COALESCE(revoked_at, ?)", t.now().UTC()).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate returns the sensor a valid token belongs to.
func (t *Tokens) Authenticate(ctx context.Context, token string) (string, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return "", ErrUnauthorized
	}
	var rec store.IngestToken
	err := t.db.NewSelect().Model(&rec).
		Where("token_hash = ?", hash(token)).Where("revoked_at IS NULL").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	now := t.now().UTC()
	if rec.LastUsedAt == nil || now.Sub(*rec.LastUsedAt) >= lastUsedPrecision {
		// Best effort: a failed update must not refuse the request.
		_, _ = t.db.NewUpdate().Model((*store.IngestToken)(nil)).
			Set("last_used_at = ?", now).Where("id = ?", rec.ID).Exec(ctx)
	}
	return rec.SensorID, nil
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
