package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/ingestor"
	"github.com/devleesch001/cauri/internal/version"
	"github.com/devleesch001/cauri/pkg/client"
)

func init() {
	register(command{"ingestor", "forward Cowrie events and TTY recordings to the API", runIngestor})
}

// ingestorOptions are the options of the ingestor, declared apart so that
// the API knows their environment variables (see envflag.Unknown).
type ingestorOptions struct {
	apiURL, token, tokenFile, sensor, logDir, ttyDir, stateDir, start, logLevel *string
	batchSize                                                                   *int
	flush, poll, timeout                                                        *time.Duration
}

func ingestorFlags(fs *flag.FlagSet) ingestorOptions {
	return ingestorOptions{
		apiURL:    fs.String("api-url", "", "base URL of the API, such as https://cauri.example.org (required)"),
		token:     fs.String("token", "", "ingestor token; prefer --token-file"),
		tokenFile: fs.String("token-file", "", "file holding the ingestor token"),
		sensor:    fs.String("sensor-id", "", "identifier of this sensor, added to every event (required)"),
		logDir:    fs.String("cowrie-log-dir", "", "Cowrie var/log/cowrie directory, holding cowrie.json (required)"),
		ttyDir:    fs.String("cowrie-tty-dir", "", "Cowrie var/lib/cowrie/tty directory (required)"),
		stateDir:  fs.String("state-dir", "data/ingestor", "writable directory for the read position and pending uploads"),
		batchSize: fs.Int("batch-size", 500, "events per batch at most"),
		flush:     fs.Duration("flush-interval", time.Second, "delay before sending an incomplete batch"),
		poll:      fs.Duration("poll-interval", 500*time.Millisecond, "delay between checks for new lines"),
		start:     fs.String("start", "beginning", "without saved position: beginning (read existing logs) or end"),
		timeout:   fs.Duration("http-timeout", time.Minute, "timeout of a request to the API"),
		logLevel:  fs.String("log-level", "info", "log level: debug, info, warn or error"),
	}
}

// ingestorFlagSet is the ingestor's flags, unparsed.
func ingestorFlagSet() *flag.FlagSet {
	fs := flag.NewFlagSet("ingestor", flag.ContinueOnError)
	ingestorFlags(fs)
	return fs
}

func runIngestor(ctx context.Context, args []string) error {
	fs := envflag.New("ingestor")
	o := ingestorFlags(fs)
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}

	switch {
	case *o.apiURL == "", *o.sensor == "", *o.logDir == "", *o.ttyDir == "":
		return errors.New("--api-url, --sensor-id, --cowrie-log-dir and --cowrie-tty-dir are required")
	case *o.start != "beginning" && *o.start != "end":
		return fmt.Errorf("--start: want beginning or end, got %q", *o.start)
	}
	secret := *o.token
	if *o.tokenFile != "" {
		data, err := os.ReadFile(*o.tokenFile)
		if err != nil {
			return fmt.Errorf("token file: %w", err)
		}
		secret = strings.TrimSpace(string(data))
	}
	if secret == "" {
		return errors.New("a token is required: --token-file or --token")
	}

	log, err := newLogger(*o.logLevel)
	if err != nil {
		return err
	}
	c, err := client.New(*o.apiURL, secret, &http.Client{Timeout: *o.timeout})
	if err != nil {
		return err
	}
	log.Info("starting ingestor", "version", version.Get().String(), "api", *o.apiURL)
	return ingestor.Run(ctx, ingestor.Config{
		SensorID:      *o.sensor,
		LogDir:        *o.logDir,
		TTYDir:        *o.ttyDir,
		StateDir:      *o.stateDir,
		Sink:          ingestor.NewHTTPSink(c),
		BatchSize:     *o.batchSize,
		FlushInterval: *o.flush,
		PollInterval:  *o.poll,
		FromStart:     *o.start == "beginning",
		Logger:        log,
	})
}
