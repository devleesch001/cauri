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

	"github.com/devleesch001/cauri/internal/store"
	"github.com/devleesch001/cauri/pkg/protocol"
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

// Create issues a token for the sensor named sensor, registering the
// sensor if needed, and returns its record and its secret value, which is
// not stored and cannot be shown again.
func (t *Tokens) Create(ctx context.Context, sensor, name string) (store.IngestToken, string, error) {
	if !protocol.ValidSensorID(sensor) {
		return store.IngestToken{}, "", fmt.Errorf("invalid sensor id %q: use 1 to 64 letters, digits, '.', '_' or '-'", sensor)
	}
	secret := make([]byte, 32)
	_, _ = rand.Read(secret)
	token := tokenPrefix + base64.RawURLEncoding.EncodeToString(secret)

	rec := store.IngestToken{Sensor: sensor, Name: name, TokenHash: hash(token), CreatedAt: store.NewTime(t.now())}
	err := t.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// The sensor row comes before its first event: the token points to it.
		sn := store.Sensor{Name: sensor}
		if _, err := tx.NewInsert().Model(&sn).On("CONFLICT (name) DO NOTHING").Exec(ctx); err != nil {
			return err
		}
		if err := tx.NewSelect().Model((*store.Sensor)(nil)).Column("id").
			Where("name = ?", sensor).Scan(ctx, &rec.SensorID); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&rec).Exec(ctx)
		return err
	})
	if err != nil {
		return store.IngestToken{}, "", err
	}
	return rec, token, nil
}

// List returns every token, revoked ones included, oldest first.
func (t *Tokens) List(ctx context.Context) ([]store.IngestToken, error) {
	var recs []store.IngestToken
	err := t.db.NewSelect().Model(&recs).ColumnExpr("tok.*, sn.name AS sensor").
		Join("JOIN sensors AS sn ON sn.id = tok.sensor_id").
		OrderExpr("tok.created_at, tok.id").Scan(ctx)
	return recs, err
}

// Revoke revokes the token with the given identifier. Revoking a revoked
// token is a no-op.
func (t *Tokens) Revoke(ctx context.Context, id int64) error {
	res, err := t.db.NewUpdate().Model((*store.IngestToken)(nil)).
		Set("revoked_at = COALESCE(revoked_at, ?)", store.NewTime(t.now())).
		Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Authenticate returns the name of the sensor a valid token belongs to.
func (t *Tokens) Authenticate(ctx context.Context, token string) (string, error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return "", ErrUnauthorized
	}
	var rec store.IngestToken
	err := t.db.NewSelect().Model(&rec).ColumnExpr("tok.*, sn.name AS sensor").
		Join("JOIN sensors AS sn ON sn.id = tok.sensor_id").
		Where("tok.token_hash = ?", hash(token)).Where("tok.revoked_at IS NULL").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUnauthorized
	}
	if err != nil {
		return "", err
	}
	now := store.NewTime(t.now())
	if rec.LastUsedAt == nil || now.Sub(rec.LastUsedAt.Time) >= lastUsedPrecision {
		// Best effort: a failed update must not refuse the request.
		_, _ = t.db.NewUpdate().Model((*store.IngestToken)(nil)).
			Set("last_used_at = ?", now).Where("id = ?", rec.ID).Exec(ctx)
	}
	return rec.Sensor, nil
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
