package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/uptrace/bun"

	"github.com/devleesch001/cauri/internal/store/storetest"
)

func TestTokenLifecycle(t *testing.T) {
	storetest.Run(t, func(t *testing.T, db *bun.DB) {
		ctx := context.Background()
		tokens := NewTokens(db)

		rec, secret, err := tokens.Create(ctx, "hp-01", "vps")
		if err != nil {
			t.Fatal(err)
		}
		sensor, err := tokens.Authenticate(ctx, secret)
		if err != nil || sensor != "hp-01" {
			t.Fatalf("Authenticate = %q, %v", sensor, err)
		}
		list, err := tokens.List(ctx)
		if err != nil || len(list) != 1 || list[0].LastUsedAt == nil || list[0].TokenHash == secret {
			t.Fatalf("List = %+v, %v", list, err)
		}

		if err := tokens.Revoke(ctx, rec.ID); err != nil {
			t.Fatal(err)
		}
		if err := tokens.Revoke(ctx, rec.ID); err != nil {
			t.Fatalf("second revoke: %v", err)
		}
		if _, err := tokens.Authenticate(ctx, secret); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("revoked token: err = %v", err)
		}
		if err := tokens.Revoke(ctx, "missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing token: err = %v", err)
		}
	})
}

func TestAuthenticateUnknown(t *testing.T) {
	tokens := NewTokens(storetest.SQLite(t))
	for _, tok := range []string{"", "cauri_unknown", "something"} {
		if _, err := tokens.Authenticate(context.Background(), tok); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("%q: err = %v", tok, err)
		}
	}
}

func TestCreateInvalidSensor(t *testing.T) {
	tokens := NewTokens(storetest.SQLite(t))
	if _, _, err := tokens.Create(context.Background(), "bad/sensor", ""); err == nil {
		t.Fatal("want error")
	}
}
