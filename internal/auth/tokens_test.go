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
		// A second token of the same sensor shares its row.
		second, _, err := tokens.Create(ctx, "hp-01", "rotation")
		if err != nil || second.SensorID != rec.SensorID {
			t.Fatalf("second token = %+v, %v", second, err)
		}
		list, err := tokens.List(ctx)
		if err != nil || len(list) != 2 || list[0].LastUsedAt == nil || list[0].TokenHash == secret || list[0].Sensor != "hp-01" {
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
		if err := tokens.Revoke(ctx, rec.ID+1000); !errors.Is(err, ErrNotFound) {
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
