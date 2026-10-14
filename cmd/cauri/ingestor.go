package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"cauri/internal/envflag"
	"cauri/internal/ingestor"
	"cauri/pkg/client"
)

func init() {
	register(command{"ingestor", "forward Cowrie events and TTY recordings to the API", runIngestor})
}

func runIngestor(ctx context.Context, args []string) error {
	fs := envflag.New("ingestor")
	apiURL := fs.String("api-url", "", "base URL of the API, such as https://cauri.example.org (required)")
	token := fs.String("token", "", "ingestor token; prefer --token-file")
	tokenFile := fs.String("token-file", "", "file holding the ingestor token")
	sensor := fs.String("sensor-id", "", "identifier of this sensor, added to every event (required)")
	logDir := fs.String("cowrie-log-dir", "", "Cowrie var/log/cowrie directory, holding cowrie.json (required)")
	ttyDir := fs.String("cowrie-tty-dir", "", "Cowrie var/lib/cowrie/tty directory (required)")
	stateDir := fs.String("state-dir", "data/ingestor", "writable directory for the read position and pending uploads")
	batchSize := fs.Int("batch-size", 500, "events per batch at most")
	flush := fs.Duration("flush-interval", time.Second, "delay before sending an incomplete batch")
	poll := fs.Duration("poll-interval", 500*time.Millisecond, "delay between checks for new lines")
	start := fs.String("start", "beginning", "without saved position: beginning (read existing logs) or end")
	timeout := fs.Duration("http-timeout", time.Minute, "timeout of a request to the API")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}

	switch {
	case *apiURL == "", *sensor == "", *logDir == "", *ttyDir == "":
		return errors.New("--api-url, --sensor-id, --cowrie-log-dir and --cowrie-tty-dir are required")
	case *start != "beginning" && *start != "end":
		return fmt.Errorf("--start: want beginning or end, got %q", *start)
	}
	secret := *token
	if *tokenFile != "" {
		data, err := os.ReadFile(*tokenFile)
		if err != nil {
			return fmt.Errorf("token file: %w", err)
		}
		secret = strings.TrimSpace(string(data))
	}
	if secret == "" {
		return errors.New("a token is required: --token-file or --token")
	}

	log, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	c, err := client.New(*apiURL, secret, &http.Client{Timeout: *timeout})
	if err != nil {
		return err
	}
	log.Info("starting ingestor", "version", buildVersion(), "api", *apiURL)
	return ingestor.Run(ctx, ingestor.Config{
		SensorID:      *sensor,
		LogDir:        *logDir,
		TTYDir:        *ttyDir,
		StateDir:      *stateDir,
		Sink:          ingestor.NewHTTPSink(c),
		BatchSize:     *batchSize,
		FlushInterval: *flush,
		PollInterval:  *poll,
		FromStart:     *start == "beginning",
		Logger:        log,
	})
}
