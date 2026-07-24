// Command goen serves the goen storefront.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(); err != nil {
		slog.Error("goen exited", "error", err)
		os.Exit(1)
	}
}

// config is goen's entire runtime configuration. It comes from the
// environment only: there is no config file to fall out of step with a
// deployment.
type config struct {
	Addr        string
	DatabaseURL string
	LogLevel    slog.Level
}

func loadConfig() (config, error) {
	url := os.Getenv("GOEN_DATABASE_URL")
	if url == "" {
		return config{}, errors.New("GOEN_DATABASE_URL is required")
	}

	level, err := parseLevel(envOr("GOEN_LOG_LEVEL", "info"))
	if err != nil {
		return config{}, err
	}

	return config{
		Addr:        envOr("GOEN_ADDR", "127.0.0.1:9700"),
		DatabaseURL: url,
		LogLevel:    level,
	}, nil
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		// The URL carries the password; report the failure without it.
		return fmt.Errorf("open database pool: %w", redactURL(err, cfg.DatabaseURL))
	}
	defer pool.Close()

	pingCtx, cancelPing := context.WithTimeout(ctx, 5*time.Second)
	defer cancelPing()
	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("reach database: %w", redactURL(err, cfg.DatabaseURL))
	}

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           newRouter(pool, log),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		// Default is 1 MiB. goen sends no large headers and receives none it
		// needs, so the smaller cap costs nothing and closes a cheap way to
		// hold a connection open.
		MaxHeaderBytes: 16 << 10,
		ErrorLog:       slog.NewLogLogger(log.Handler(), slog.LevelError),
	}

	serveErr := make(chan error, 1)
	go func() {
		log.Info("goen serving", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	log.Info("goen shutting down")
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down server: %w", err)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func parseLevel(s string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err != nil {
		return 0, fmt.Errorf("parse GOEN_LOG_LEVEL %q: %w", s, err)
	}
	return level, nil
}

// redactURL removes the database URL from an error message. pgx includes the
// connection string in some failures, and that string holds the password.
func redactURL(err error, url string) error {
	if url == "" {
		return err
	}
	msg := strings.ReplaceAll(err.Error(), url, "[database url]")
	if msg == err.Error() {
		return err
	}
	return errors.New(msg)
}
