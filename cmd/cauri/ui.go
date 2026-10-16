package main

import (
	"context"
	"net/http"
	"time"

	"github.com/devleesch001/cauri/internal/envflag"
	"github.com/devleesch001/cauri/internal/version"
	"github.com/devleesch001/cauri/internal/webui"
)

func init() {
	register(command{"ui", "serve the web UI only (the reverse proxy sends /api to the API)", runUI})
}

func runUI(ctx context.Context, args []string) error {
	fs := envflag.New("ui")
	listen := fs.String("listen", ":8080", "HTTP listen address")
	logLevel := fs.String("log-level", "info", "log level: debug, info, warn or error")
	if err := envflag.Parse(fs, args); err != nil {
		return err
	}
	log, err := newLogger(*logLevel)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           webui.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	log.Info("starting ui", "version", version.Get().String())
	return serve(ctx, srv, log)
}
