// Package config loads and validates Githome's runtime configuration from the
// environment (with an optional file overlay) into a single immutable Config.
//
// Precedence is defaults, then an optional KEY=VALUE file pointed to by
// GITHOME_CONFIG_FILE, then GITHOME_* environment variables (highest). URLs are
// parsed once at startup and handed to the presenter layer so every response
// builds links from the configured host, never a hardcoded one.
package config

import (
	"net/url"
	"runtime"
	"time"
)

// Version is the build version, set at link time with
// -ldflags "-X github.com/tamnd/githome/config.Version=<v>". It is reported by
// the /healthz endpoint and stamped onto the default logger.
var Version = "dev"

// Config is the fully resolved server configuration. It is built once by Load
// and treated as immutable afterwards.
type Config struct {
	URLs            URLs
	Listen          Listen
	DatabaseURL     string // GITHOME_DATABASE_URL; scheme selects the dialect
	DBPoolSize      int    // GITHOME_DB_POOL_SIZE; Postgres max-open-connections, default 25
	DataDir         string // GITHOME_DATA_DIR; bare repos live under DataDir/repos
	GitBinaryPath   string // GITHOME_GIT_BINARY; resolved on PATH when empty
	GitBackend      string // GITHOME_GIT_BACKEND; auto|gogit|gitcli|git2go
	RateLimit       RateLimit
	Secrets         Secrets
	Worker          Worker
	Log             Log
	Server          Server
	ShutdownTimeout time.Duration // GITHOME_SHUTDOWN_TIMEOUT; default 30s
	Env             string        // GITHOME_ENV; "production" switches slog to JSON
}

// URLs are the resolved external base URLs. API and GraphQL default to the HTML
// base plus the GHES-style suffixes when not set explicitly.
type URLs struct {
	API     *url.URL // GITHOME_API_BASE_URL   e.g. https://git.example.com/api/v3
	HTML    *url.URL // GITHOME_HTML_BASE_URL  e.g. https://git.example.com
	GraphQL *url.URL // GITHOME_GRAPHQL_URL    e.g. https://git.example.com/api/graphql
	SSHHost string   // GITHOME_GIT_SSH_HOST   e.g. git.example.com
	SSHPort int      // GITHOME_GIT_SSH_PORT   default 22

	rawAPI     string
	rawHTML    string
	rawGraphQL string
}

// Listen holds the bind addresses for the two listeners.
type Listen struct {
	HTTP string // GITHOME_LISTEN_HTTP  default ":3000"
	SSH  string // GITHOME_LISTEN_SSH   default ":2222"
}

// RateLimit configures the per-actor rate-limit buckets that back the
// x-ratelimit-* headers and the /rate_limit endpoint.
type RateLimit struct {
	AuthedPerHour int           // GITHOME_RL_AUTHED_PER_HOUR    default 5000
	AnonPerHour   int           // GITHOME_RL_ANON_PER_HOUR      default 60
	GraphQLPoints int           // GITHOME_RL_GRAPHQL_POINTS     default 5000
	SearchPerMin  int           // GITHOME_RL_SEARCH_PER_MIN     default 30
	Window        time.Duration // fixed 1h to match GitHub reset semantics
}

// Secrets holds the sensitive material the server needs. Values may be supplied
// inline or, for the SSH host key, via a *_FILE indirection.
type Secrets struct {
	SessionKey            []byte // GITHOME_SESSION_KEY  (>= 32 bytes)
	TokenPepper           []byte // GITHOME_TOKEN_PEPPER (>= 16 bytes)
	SSHHostKey            []byte // GITHOME_SSH_HOST_KEY or GITHOME_SSH_HOST_KEY_FILE
	WebhookSigningDefault []byte // GITHOME_WEBHOOK_SECRET
}

// Worker configures the background job pool.
type Worker struct {
	Concurrency int           // GITHOME_WORKER_CONCURRENCY  default GOMAXPROCS
	PollEvery   time.Duration // GITHOME_WORKER_POLL         default 2s
}

// Server holds the HTTP server hardening knobs. The two whole-request deadlines
// default to zero on purpose: git smart-HTTP clone and push stream a single
// response or request body for as long as a multi-gigabyte transfer takes, and a
// blanket ReadTimeout or WriteTimeout would sever those mid-transfer. An operator
// who fronts the JSON API on a separate listener with no git traffic can set
// them. The always-safe guards (header read deadline, idle keep-alive reaping,
// header size cap, and the JSON request-body cap) are on by default.
type Server struct {
	ReadHeaderTimeout time.Duration // GITHOME_HTTP_READ_HEADER_TIMEOUT  default 10s
	ReadTimeout       time.Duration // GITHOME_HTTP_READ_TIMEOUT         default 0 (off; git streams)
	WriteTimeout      time.Duration // GITHOME_HTTP_WRITE_TIMEOUT        default 0 (off; git streams)
	IdleTimeout       time.Duration // GITHOME_HTTP_IDLE_TIMEOUT         default 120s
	MaxHeaderBytes    int           // GITHOME_HTTP_MAX_HEADER_BYTES     default 1 MiB
	MaxBodyBytes      int64         // GITHOME_HTTP_MAX_BODY_BYTES       default 25 MiB; JSON API only
	MaxBlobBytes      int64         // GITHOME_GIT_MAX_BLOB_BYTES        default 100 MiB; -1 disables
}

// Log configures the structured logger.
type Log struct {
	Level  string // GITHOME_LOG_LEVEL   debug|info|warn|error  default info
	Format string // GITHOME_LOG_FORMAT  json|text; empty resolves from Env
}

// RepoRoot is the directory that holds the bare git repositories.
func (c Config) RepoRoot() string { return c.DataDir + "/repos" }

func defaults() Config {
	return Config{
		Listen:          Listen{HTTP: ":3000", SSH: ":2222"},
		DataDir:         "/data",
		DBPoolSize:      25,
		GitBackend:      "auto",
		ShutdownTimeout: 30 * time.Second,
		Env:             "development",
		RateLimit: RateLimit{
			AuthedPerHour: 5000,
			AnonPerHour:   60,
			GraphQLPoints: 5000,
			SearchPerMin:  30,
			Window:        time.Hour,
		},
		Worker: Worker{Concurrency: runtime.GOMAXPROCS(0), PollEvery: 2 * time.Second},
		Log:    Log{Level: "info"},
		Server: Server{
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    1 << 20,
			MaxBodyBytes:      25 << 20,
			MaxBlobBytes:      100 << 20,
		},
	}
}
