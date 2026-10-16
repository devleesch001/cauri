package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/geoip"
)

func init() {
	register(command{"download", "download a third-party database: geoip or asn", runDownload})
}

const downloadUsage = `Usage:
  cauri download geoip [options]   the GeoIP database (--geoip-db), from --geoip-db-url
  cauri download asn [options]     the ASN database (--geoip-asn-db), from --geoip-asn-db-url

Downloads the database over its file: written beside it, checked to be a
database of the right kind, then put in its place, so that the file in
place is never left half written. A running API loads it within a minute.
Options are those of "cauri api", environment variables included.

Options:`

func runDownload(ctx context.Context, args []string) error {
	fs := envflag.New("download")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), downloadUsage)
		fs.PrintDefaults()
	}
	var geo geoFlags
	geo.register(fs)
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fs.Usage()
		return flag.ErrHelp
	}
	name := args[0]
	if err := envflag.Parse(fs, args[1:]); err != nil {
		return err
	}
	geo.defaults()
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	path, url, asn := geo.db, geo.url, false
	switch name {
	case "geoip":
	case "asn":
		path, url, asn = geo.asnDB, geo.asnURL, true
	default:
		fs.Usage()
		return fmt.Errorf("unknown database %q: want geoip or asn", name)
	}
	if path == "" {
		return errors.New("no file to download into: the database is turned off (empty path)")
	}

	log, err := newLogger("info")
	if err != nil {
		return err
	}
	log.Info("downloading", "database", name, "url", maskDSN(url), "path", path)
	start := time.Now()
	var next int64
	info, err := geoip.Download(ctx, &http.Client{Timeout: 30 * time.Minute}, url, path, asn, func(done, total int64) {
		if done >= next {
			log.Info("downloading", "bytes", done, "total", total)
			next = done + 10<<20
		}
	})
	if err != nil {
		return err
	}
	log.Info("downloaded", "database", name, "path", path, "source", info.Source(), "duration_ms", time.Since(start).Milliseconds())
	fmt.Fprintf(os.Stdout, "%s: %s\n", path, info.Source())
	return nil
}
