// Command githome runs the Githome server.
//
// The server wires configuration, the metadata store with migrations, the REST
// and GraphQL surfaces, and the git Smart HTTP transport. As of M2 it serves
// users, repository metadata, repository contents and git data, the repository
// GraphQL query, and read-only git clone and fetch. The git write path, the SSH
// transport, and the worker pool join in later milestones.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/api/rest"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if err := st.Migrate(ctx); err != nil {
		return err
	}

	authSvc := auth.NewService(st, cfg.URLs.HTML.String())
	defer authSvc.Close()

	gitStore := git.NewStore(cfg.RepoRoot())
	repoSvc := domain.NewRepoService(st, gitStore)
	urls := presenter.NewURLBuilder(cfg.URLs)

	root := mizu.NewRouter()
	rest.Mount(root, rest.Deps{
		Config:     cfg,
		Logger:     logger,
		Ready:      st,
		Auth:       authSvc,
		Users:      domain.NewUserService(st),
		Repos:      repoSvc,
		URLs:       urls,
		NodeFormat: nodeid.FormatNew,
	})
	graphql.Mount(root, graphql.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		URLs:       urls,
		NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{
		GitBin: cfg.GitBinaryPath,
		Repos:  repoSvc,
		Git:    gitStore,
		Log:    logger,
	})

	srv := &http.Server{
		Addr:              cfg.Listen.HTTP,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("http listening", "addr", cfg.Listen.HTTP)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errc:
		logger.Error("server failed, shutting down", "err", err)
		stop()
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	logger.Info("shutdown complete")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	level := parseLevel(cfg.Log.Level)
	format := cfg.Log.Format
	if format == "" {
		if cfg.Env == "production" {
			format = "json"
		} else {
			format = "text"
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.New(h).With("service", "githome", "version", config.Version)
}

func parseLevel(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
