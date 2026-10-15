package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/devleesch001/cauri/internal/auth"
	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/store"
)

func init() {
	register(command{"token", "manage ingestor tokens (create, list, revoke)", runToken})
}

//nolint:gosec // G101: help text, not a credential.
const tokenUsage = `Usage:
  cauri token create --sensor <id> [--name <label>]
  cauri token list
  cauri token revoke <token id>
  cauri token verify --token-file <file>    exits 1 if the token is unknown or revoked

Database options (--db-driver, --db-dsn) are the same as for "cauri api".`

func runToken(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, tokenUsage)
		return flag.ErrHelp
	}
	sub, args := args[0], args[1:]
	fs := envflag.New("token " + sub)
	var db dbFlags
	db.register(fs)
	sensor := fs.String("sensor", "", "sensor identifier the token belongs to (create)")
	name := fs.String("name", "", "free label, such as the host of the sensor (create)")
	tokenFile := fs.String("token-file", "", "file holding the token to check (verify)")
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}

	// On stderr, so that the token printed on stdout stays alone.
	log, err := newLogger("info")
	if err != nil {
		return err
	}
	conn, err := db.open(ctx, log)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	tokens := auth.NewTokens(conn)

	switch sub {
	case "create":
		if *sensor == "" {
			return errors.New("--sensor is required")
		}
		rec, secret, err := tokens.Create(ctx, *sensor, *name)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Token %d created for sensor %s. It is shown only once:\n", rec.ID, rec.Sensor)
		fmt.Println(secret)
		return nil
	case "list":
		recs, err := tokens.List(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSENSOR\tNAME\tCREATED\tLAST USED\tREVOKED")
		for _, r := range recs {
			fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", r.ID, r.Sensor, r.Name,
				fmtTime(&r.CreatedAt), fmtTime(r.LastUsedAt), fmtTime(r.RevokedAt))
		}
		return w.Flush()
	case "revoke":
		if fs.NArg() != 1 {
			return errors.New("usage: cauri token revoke <token id>")
		}
		id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil {
			return fmt.Errorf("token id %q: want the number shown by \"cauri token list\"", fs.Arg(0))
		}
		if err := tokens.Revoke(ctx, id); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Token %s revoked.\n", fs.Arg(0))
		return nil
	case "verify":
		if *tokenFile == "" {
			return errors.New("--token-file is required")
		}
		data, err := os.ReadFile(*tokenFile) //nolint:gosec // G703: path given by the operator
		if err != nil {
			return err
		}
		sensor, err := tokens.Authenticate(ctx, strings.TrimSpace(string(data)))
		if err != nil {
			return fmt.Errorf("%s: %w", *tokenFile, err)
		}
		fmt.Fprintf(os.Stderr, "%s: valid token of sensor %s.\n", *tokenFile, sensor)
		return nil
	default:
		fmt.Fprintln(os.Stderr, tokenUsage)
		return fmt.Errorf("unknown token command %q", sub)
	}
}

func fmtTime(t *store.Time) string {
	if t == nil {
		return "-"
	}
	return t.Format(time.RFC3339)
}
