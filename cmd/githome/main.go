// Command githome runs the Githome server.
//
// The server wires configuration, the metadata store with migrations, the REST
// and GraphQL surfaces, the git Smart HTTP transport, and the job runtime. As of
// M7 it serves users, repository metadata, repository contents and git data,
// issues, pull requests with their merge surface, code review, the repository
// GraphQL query, git clone and fetch, git push (receive-pack plus the REST
// ref-write endpoints), repository webhooks with signed delivery, and the
// activity feed the Events API exposes. The job runtime drains the queue
// in-process; a push or issue or pull request records an event the runtime fans
// out to the repository's hooks. The SSH transport joins in a later milestone.
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
	"github.com/tamnd/githome/webhook"
	"github.com/tamnd/githome/worker"
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
	if cfg.GitBinaryPath != "" {
		gitStore.SetGitBin(cfg.GitBinaryPath)
	}
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gitStore)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)
	userSvc := domain.NewUserService(st)
	enqueuer := worker.NewStoreEnqueuer(st)
	hookSvc := domain.NewHookService(st, repoSvc, enqueuer)
	eventSvc := domain.NewEventService(st, repoSvc)
	urls := presenter.NewURLBuilder(cfg.URLs)

	// The webhook deliverer renders each recorded event through the presenter and
	// posts it to the repository's subscribed hooks behind an SSRF guard. Its two
	// handlers join the runtime below: deliver_event fans an event out to its
	// hooks, and deliver_webhook performs one signed POST and records the result.
	webhookRenderer := webhook.NewRenderer(repoSvc, issueSvc, pullSvc, userSvc, urls, nodeid.FormatNew)
	deliverer := webhook.NewDeliverer(st, webhookRenderer, nil, enqueuer, config.Version)

	// The job runtime drains the queue the domain fills. M5 registers the
	// mergeability recompute a pull request enqueues when it opens or its base or
	// head moves; M7 adds the webhook fan-out and delivery handlers. It runs for
	// the process lifetime and stops when the root context is canceled.
	runtime := worker.NewRuntime(st, logger, 0)
	runtime.Register(domain.JobRecomputeMergeability, worker.RecomputeMergeabilityHandler(pullSvc))
	runtime.Register(domain.JobRecomputeReviewDecision, worker.RecomputeReviewDecisionHandler(reviewSvc))
	runtime.Register(domain.JobDeliverEvent, deliverer.DeliverEventHandler())
	runtime.Register(domain.JobDeliverWebhook, deliverer.DeliverWebhookHandler())
	go func() {
		if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("worker runtime stopped", "err", err)
		}
	}()

	root := mizu.NewRouter()
	rest.Mount(root, rest.Deps{
		Config:     cfg,
		Logger:     logger,
		Ready:      st,
		Auth:       authSvc,
		Users:      userSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Reviews:    reviewSvc,
		Checks:     checksSvc,
		Hooks:      hookSvc,
		Events:     eventSvc,
		URLs:       urls,
		NodeFormat: nodeid.FormatNew,
	})
	graphql.Mount(root, graphql.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Reviews:    reviewSvc,
		Checks:     checksSvc,
		URLs:       urls,
		NodeFormat: nodeid.FormatNew,
	})
	gittransport.Mount(root, &gittransport.Service{
		GitBin: cfg.GitBinaryPath,
		Repos:  repoSvc,
		Git:    gitStore,
		Pulls:  pullSvc,
		Auth:   authSvc,
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
