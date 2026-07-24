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

	"github.com/jackc/pgx/v5"
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

	pool, err := openPool(ctx, cfg.DatabaseURL)
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

	// The connection guard (openPool) already refuses a session that stays
	// superuser after SET ROLE goen_app. A superuser LOGIN role is weaker but
	// still unsafe — it can RESET ROLE back to full privilege — yet the dev
	// Makefile connects as the owning superuser on purpose, so warn rather than
	// refuse. Production points GOEN_DATABASE_URL at a non-superuser member of
	// goen_app (goen_web) and this line stays quiet.
	var loginIsSuper bool
	if err := pool.QueryRow(ctx,
		"SELECT rolsuper FROM pg_roles WHERE rolname = session_user",
	).Scan(&loginIsSuper); err == nil && loginIsSuper {
		log.Warn("connected as a superuser login role; the privilege model can be " +
			"reset away with RESET ROLE — use a non-superuser member of goen_app in production")
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

// openPool builds the connection pool goen serves from.
//
// Two things beyond a bare pgxpool.New. The timeouts come from the database
// rules: a connection that cannot be reached should fail, not hang, and a
// pooled connection is recycled rather than kept forever. And every connection
// does SET ROLE goen_app on acquisition, which is what makes the privilege
// model in the schema bite: the running binary operates as goen_app — barred
// from writing stock, money or a ledger except through the SECURITY DEFINER
// functions — no matter which login role the deployment connects with. The
// migration tool connects separately, as the owner, and is unaffected.
func openPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.MaxConnIdleTime = 30 * time.Minute
	cfg.MaxConnLifetime = time.Hour
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		if _, err := conn.Exec(ctx, "SET ROLE goen_app"); err != nil {
			return fmt.Errorf("assume goen_app role: %w", err)
		}
		// The privilege model is only real if the running session cannot ignore
		// it. A superuser bypasses every REVOKE, so if the session is still a
		// superuser after SET ROLE goen_app — meaning goen_app itself was
		// granted superuser — refuse to serve. This holds in development too,
		// where the login role is the owning superuser but SET ROLE drops it.
		var superAsApp bool
		if err := conn.QueryRow(ctx,
			"SELECT current_setting('is_superuser')::boolean",
		).Scan(&superAsApp); err != nil {
			return fmt.Errorf("check privilege boundary: %w", err)
		}
		if superAsApp {
			return errors.New("refusing to serve: the session is a superuser " +
				"after SET ROLE goen_app, so the schema's write REVOKEs do not bind it")
		}
		return nil
	}
	return pgxpool.NewWithConfig(ctx, cfg)
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
