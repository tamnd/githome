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
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-mizu/mizu"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/tamnd/githome/api/graphql"
	"github.com/tamnd/githome/api/rest"
	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/fe"
	"github.com/tamnd/githome/fe/assets"
	"github.com/tamnd/githome/fe/render"
	"github.com/tamnd/githome/fe/view"
	websettings "github.com/tamnd/githome/fe/web/settings"
	"github.com/tamnd/githome/fe/webmw"
	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/gittransport"
	"github.com/tamnd/githome/markup"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/webhook"
	"github.com/tamnd/githome/worker"
)

func main() {
	// "githome browse <path>" is a zero-config subcommand that does not use the
	// normal config loader. Route it before flag.Parse so its own flags are not
	// mixed with the top-level ones.
	if len(os.Args) >= 2 && os.Args[1] == "browse" {
		if err := runBrowse(os.Args[2:]); err != nil {
			slog.Error("fatal", "err", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	showVersion := flag.Bool("version", false, "print the build version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(config.Version)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DatabaseURL, cfg.DBPoolSize)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	if err := st.Migrate(ctx); err != nil {
		return err
	}

	authSvc := auth.NewService(st, cfg.URLs.HTML.String())
	defer authSvc.Close()

	// Seed the OAuth app rows first-party clients hardcode, so "gh auth login"
	// can open its device flow against a fresh install. Idempotent: an existing
	// row is left alone.
	if err := authSvc.EnsureFirstPartyApps(ctx); err != nil {
		return fmt.Errorf("seed first-party oauth apps: %w", err)
	}

	gitStore := git.NewStore(cfg.RepoRoot())
	if cfg.GitBinaryPath != "" {
		gitStore.SetGitBin(cfg.GitBinaryPath)
	}
	gitStore.SetMaxBlobBytes(cfg.Server.MaxBlobBytes)
	repoSvc := domain.NewRepoService(st, gitStore)
	issueSvc := domain.NewIssueService(st, repoSvc)
	pullSvc := domain.NewPRService(st, repoSvc, issueSvc, gitStore)
	reviewSvc := domain.NewReviewService(st, repoSvc, pullSvc, issueSvc, gitStore)
	checksSvc := domain.NewChecksService(st, repoSvc, issueSvc, gitStore)
	userSvc := domain.NewUserService(st)
	enqueuer := worker.NewStoreEnqueuer(st)
	hookSvc := domain.NewHookService(st, repoSvc, enqueuer)
	eventSvc := domain.NewEventService(st, repoSvc)
	searchSvc := domain.NewSearchService(st, repoSvc, issueSvc, gitStore)
	releaseSvc := domain.NewReleaseService(st, repoSvc, cfg.AssetRoot())
	gistSvc := domain.NewGistService(st)
	socialSvc := domain.NewSocialService(st)
	keySvc := domain.NewKeyService(st)
	teamSvc := domain.NewTeamService(st)
	notifSvc := domain.NewNotificationService(st)
	urls := presenter.NewURLBuilder(cfg.URLs)

	// The markup renderer is the one path from file or comment content to trusted
	// HTML. It is built once and shared by the web front and the REST surface (the
	// /markdown endpoints and the text/html media type) so both apply the same
	// allowlist and link rules. It reads links and anchors against the configured
	// HTML base and proxies off-host images through camo when a secret is set.
	markupRenderer := markup.New(markup.Config{
		BaseURL:           cfg.URLs.HTML.String(),
		CamoSecret:        cfg.Markup.CamoSecret,
		CamoBaseURL:       cfg.Markup.CamoBaseURL,
		MaxHighlightBytes: cfg.Markup.MaxHighlightBytes,
		Logger:            logger,
	})

	// The webhook deliverer renders each recorded event through the presenter and
	// posts it to the repository's subscribed hooks behind an SSRF guard. Its two
	// handlers join the runtime below: deliver_event fans an event out to its
	// hooks, and deliver_webhook performs one signed POST and records the result.
	webhookRenderer := webhook.NewRenderer(repoSvc, issueSvc, pullSvc, userSvc, urls, nodeid.FormatNew)
	webhookRenderer.BindGit(gitStore)
	webhookRenderer.BindReviews(reviewSvc)
	webhookRenderer.BindReleases(releaseSvc)
	deliverer := webhook.NewDeliverer(st, webhookRenderer, nil, enqueuer, config.Version)

	// The job runtime drains the queue the domain fills. M5 registers the
	// mergeability recompute a pull request enqueues when it opens or its base or
	// head moves; M7 adds the webhook fan-out and delivery handlers. It runs for
	// the process lifetime and stops when the root context is canceled.
	runtime := worker.NewRuntime(st, logger, 0)
	runtime.SetWorkers(4)
	runtime.Register(domain.JobReindexSearch, worker.ReindexSearchHandler(searchSvc))
	runtime.Register(domain.JobRecomputeMergeability, worker.RecomputeMergeabilityHandler(pullSvc))
	runtime.Register(domain.JobRecomputeReviewDecision, worker.RecomputeReviewDecisionHandler(reviewSvc))
	runtime.Register(domain.JobDeliverEvent, deliverer.DeliverEventHandler())
	runtime.Register(domain.JobDeliverWebhook, deliverer.DeliverWebhookHandler())
	go func() {
		if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("worker runtime stopped", "err", err)
		}
	}()

	// Delivery records carry full request and response bodies; the retention
	// loop keeps the table bounded by pruning records past the 30-day window.
	go worker.RunDeliveryRetention(ctx, st, logger)

	root := mizu.NewRouter()
	rest.Mount(root, rest.Deps{
		Config:        cfg,
		WebFront:      cfg.Web.Enabled,
		Logger:        logger,
		Ready:         st,
		Auth:          authSvc,
		Users:         userSvc,
		Repos:         repoSvc,
		Issues:        issueSvc,
		Pulls:         pullSvc,
		Reviews:       reviewSvc,
		Checks:        checksSvc,
		Keys:          keySvc,
		Teams:         teamSvc,
		Hooks:         hookSvc,
		Events:        eventSvc,
		Search:        searchSvc,
		Gists:         gistSvc,
		Social:        socialSvc,
		Releases:      releaseSvc,
		Notifications: notifSvc,
		URLs:          urls,
		NodeFormat:    nodeid.FormatNew,
		Markup:        markupRenderer,
	})
	graphql.Mount(root, graphql.Deps{
		Auth:       authSvc,
		Repos:      repoSvc,
		Issues:     issueSvc,
		Pulls:      pullSvc,
		Reviews:    reviewSvc,
		Checks:     checksSvc,
		Users:      userSvc,
		Search:     searchSvc,
		Releases:   releaseSvc,
		Batch:      domain.NewBatcher(st),
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

	// The server-rendered web front mounts beside the APIs on the same router,
	// sharing the domain services and the session secret. It owns its own
	// middleware chain (recover, session, color mode, CSRF, flash) through scoped
	// subrouters, so the API surface keeps its own. A build error in the template
	// set or asset manifest is fatal: the front cannot serve a page without them.
	// The web front, when enabled, wraps root: it serves its asset tree ahead of
	// the shared router (the two cannot live on one mux) and delegates every
	// dynamic route back to root, where the APIs and git transport also sit.
	var handler http.Handler = root
	if cfg.Web.Enabled {
		webHandler, err := mountWeb(root, cfg, logger, authSvc, st, userSvc, repoSvc, hookSvc, checksSvc, issueSvc, pullSvc, reviewSvc, searchSvc, eventSvc, notifSvc, urls, markupRenderer)
		if err != nil {
			return fmt.Errorf("web front: %w", err)
		}
		handler = webHandler
		logger.Info("web front mounted", "site", cfg.Web.SiteName, "dev_assets", assets.Dev())
	}

	// Serve HTTP/2 over cleartext (h2c) as well as HTTP/1.1. The deployment
	// terminates TLS at a reverse proxy, so the listener is plaintext; without
	// h2c a client (or a proxy speaking h2c upstream) is pinned to HTTP/1.1 and
	// the page's asset fetches serialize over one connection. Wrapping the
	// handler lets an h2c prior-knowledge or Upgrade request multiplex them.
	// An HTTP/1.1 client is unaffected: the wrapper passes it straight through.
	h2s := &http2.Server{IdleTimeout: cfg.Server.IdleTimeout}
	srv := &http.Server{
		Addr:              cfg.Listen.HTTP,
		Handler:           h2c.NewHandler(handler, h2s),
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
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

// mountWeb builds the web front's render set, view builder and middleware, then
// mounts it on root. The viewer lookup adapts the user service to the front's
// Viewer model; a user the session names but the store no longer has resolves to
// anonymous, not an error, so a stale session cookie degrades gracefully.
func mountWeb(root *mizu.Router, cfg config.Config, logger *slog.Logger, authSvc *auth.Service, st *store.Store, users *domain.UserService, repos *domain.RepoService, hooks *domain.HookService, checks *domain.ChecksService, issues *domain.IssueService, pulls *domain.PRService, reviews *domain.ReviewService, search *domain.SearchService, events *domain.EventService, notifications *domain.NotificationService, urls *presenter.URLBuilder, markupRenderer *markup.Renderer) (http.Handler, error) {
	renderSet, err := render.New(assets.FS(), assets.Dev())
	if err != nil {
		return nil, err
	}

	// The social service backs the profile's stars, followers, and following tabs,
	// the same domain service the REST social routes use.
	socialSvc := domain.NewSocialService(st)

	lookup := func(ctx context.Context, pk int64) (*view.Viewer, error) {
		u, err := users.Viewer(ctx, pk)
		if err != nil {
			if errors.Is(err, domain.ErrUserNotFound) {
				return nil, nil
			}
			return nil, err
		}
		v := &view.Viewer{Login: u.Login, SiteAdmin: u.SiteAdmin}
		if u.Name != nil {
			v.Name = *u.Name
		}
		return v, nil
	}

	return fe.Mount(root, fe.Deps{
		Render:        renderSet,
		View:          view.NewBuilder(cfg.Web.SiteName),
		Auth:          st,
		OAuthSvc:      authSvc,
		Tokens:        patService{authSvc},
		Repos:         repos,
		Hooks:         hooks,
		Checks:        checks,
		Issues:        issues,
		Pulls:         pulls,
		Reviews:       reviews,
		Search:        search,
		Users:         users,
		Events:        events,
		Social:        socialSvc,
		Notifications: notifications,
		URLs:          urls,
		Markup:        markupRenderer,
		Sessions:      webmw.NewSessions(cfg.Secrets.SessionKey, 0, lookup),
		CSRF:          webmw.NewCSRF(renderSet),
		Flash:         webmw.NewFlash(cfg.Secrets.SessionKey),
		Logger:        logger,
	}), nil
}

// patService adapts the auth service to the settings page's TokenService. The
// calls pass straight through; only the list copies each summary into the
// front's own PAT shape, which keeps fe off the auth package.
type patService struct{ svc *auth.Service }

func (p patService) CreatePAT(ctx context.Context, userPK int64, note string, scopes []string) (string, error) {
	return p.svc.CreatePAT(ctx, userPK, note, scopes)
}

func (p patService) ListPATs(ctx context.Context, userPK int64) ([]websettings.PAT, error) {
	infos, err := p.svc.ListPATs(ctx, userPK)
	if err != nil {
		return nil, err
	}
	out := make([]websettings.PAT, len(infos))
	for i, t := range infos {
		out[i] = websettings.PAT{
			ID:         t.ID,
			Note:       t.Note,
			Scopes:     t.Scopes,
			LastEight:  t.LastEight,
			CreatedAt:  t.CreatedAt,
			LastUsedAt: t.LastUsedAt,
		}
	}
	return out, nil
}

func (p patService) DeletePAT(ctx context.Context, userPK, id int64) error {
	return p.svc.DeletePAT(ctx, userPK, id)
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
