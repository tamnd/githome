package config

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Load builds a Config from defaults, an optional file overlay, and the
// environment, in that order of increasing precedence. It then resolves the
// derived URLs and validates the result.
func Load() (Config, error) {
	c := defaults()

	if path := os.Getenv("GITHOME_CONFIG_FILE"); path != "" {
		vals, err := readFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("config file %s: %w", path, err)
		}
		overlayEnvFromFile(vals)
	}

	applyEnv(&c)

	if err := c.resolveURLs(); err != nil {
		return Config{}, err
	}
	fillSecrets(&c)
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// fillSecrets generates random values for any secret that was not set by the
// environment. The generated keys are ephemeral: sessions and tokens will not
// survive a restart. The server logs a warning for each missing secret so an
// operator running in production can supply stable values.
func fillSecrets(c *Config) {
	if len(c.Secrets.SessionKey) == 0 {
		slog.Warn("GITHOME_SESSION_KEY not set; using a random ephemeral key (sessions will not survive restarts)")
		k := make([]byte, 32)
		_, _ = rand.Read(k)
		c.Secrets.SessionKey = k
	}
	if len(c.Secrets.TokenPepper) == 0 {
		slog.Warn("GITHOME_TOKEN_PEPPER not set; using a random ephemeral pepper (personal access tokens will not survive restarts)")
		k := make([]byte, 16)
		_, _ = rand.Read(k)
		c.Secrets.TokenPepper = k
	}
}

// applyEnv overwrites a field only when its variable is set, so an unset
// variable leaves the default (or file) value intact.
func applyEnv(c *Config) {
	setStr(&c.DatabaseURL, "GITHOME_DATABASE_URL")
	setInt(&c.DBPoolSize, "GITHOME_DB_POOL_SIZE")
	setStr(&c.DataDir, "GITHOME_DATA_DIR")
	setStr(&c.GitBinaryPath, "GITHOME_GIT_BINARY")
	setStr(&c.GitBackend, "GITHOME_GIT_BACKEND")
	setStr(&c.Listen.HTTP, "GITHOME_LISTEN_HTTP")
	setStr(&c.Listen.SSH, "GITHOME_LISTEN_SSH")
	setStr(&c.Env, "GITHOME_ENV")
	setStr(&c.Log.Level, "GITHOME_LOG_LEVEL")
	setStr(&c.Log.Format, "GITHOME_LOG_FORMAT")

	setInt(&c.RateLimit.AuthedPerHour, "GITHOME_RL_AUTHED_PER_HOUR")
	setInt(&c.RateLimit.AnonPerHour, "GITHOME_RL_ANON_PER_HOUR")
	setInt(&c.RateLimit.GraphQLPoints, "GITHOME_RL_GRAPHQL_POINTS")
	setInt(&c.RateLimit.SearchPerMin, "GITHOME_RL_SEARCH_PER_MIN")
	setInt(&c.Worker.Concurrency, "GITHOME_WORKER_CONCURRENCY")
	setDur(&c.Worker.PollEvery, "GITHOME_WORKER_POLL")
	setDur(&c.ShutdownTimeout, "GITHOME_SHUTDOWN_TIMEOUT")

	setDur(&c.Server.ReadHeaderTimeout, "GITHOME_HTTP_READ_HEADER_TIMEOUT")
	setDur(&c.Server.ReadTimeout, "GITHOME_HTTP_READ_TIMEOUT")
	setDur(&c.Server.WriteTimeout, "GITHOME_HTTP_WRITE_TIMEOUT")
	setDur(&c.Server.IdleTimeout, "GITHOME_HTTP_IDLE_TIMEOUT")
	setInt(&c.Server.MaxHeaderBytes, "GITHOME_HTTP_MAX_HEADER_BYTES")
	setInt64(&c.Server.MaxBodyBytes, "GITHOME_HTTP_MAX_BODY_BYTES")
	setInt64(&c.Server.MaxBlobBytes, "GITHOME_GIT_MAX_BLOB_BYTES")

	setBool(&c.Web.Enabled, "GITHOME_WEB_ENABLED")
	setStr(&c.Web.SiteName, "GITHOME_WEB_SITE_NAME")

	setStr(&c.Markup.CamoBaseURL, "GITHOME_CAMO_BASE_URL")
	setInt(&c.Markup.MaxHighlightBytes, "GITHOME_MARKUP_MAX_HIGHLIGHT_BYTES")
	if v := secretBytes("GITHOME_CAMO_SECRET"); v != nil {
		c.Markup.CamoSecret = v
	}

	c.URLs.rawAPI = firstNonEmpty(os.Getenv("GITHOME_API_BASE_URL"), c.URLs.rawAPI)
	c.URLs.rawHTML = firstNonEmpty(os.Getenv("GITHOME_HTML_BASE_URL"), c.URLs.rawHTML)
	c.URLs.rawGraphQL = firstNonEmpty(os.Getenv("GITHOME_GRAPHQL_URL"), c.URLs.rawGraphQL)
	setStr(&c.URLs.SSHHost, "GITHOME_GIT_SSH_HOST")
	setInt(&c.URLs.SSHPort, "GITHOME_GIT_SSH_PORT")

	if v := secretBytes("GITHOME_SESSION_KEY"); v != nil {
		c.Secrets.SessionKey = v
	}
	if v := secretBytes("GITHOME_TOKEN_PEPPER"); v != nil {
		c.Secrets.TokenPepper = v
	}
	if v := secretBytesOrFile("GITHOME_SSH_HOST_KEY", "GITHOME_SSH_HOST_KEY_FILE"); v != nil {
		c.Secrets.SSHHostKey = v
	}
	if v := secretBytes("GITHOME_WEBHOOK_SECRET"); v != nil {
		c.Secrets.WebhookSigningDefault = v
	}
}

// overlayEnvFromFile feeds a parsed file's KEY=VALUE pairs through the same env
// path so file and env share one set of parsing rules. Already-set environment
// variables win and are never clobbered.
func overlayEnvFromFile(vals map[string]string) {
	for k, v := range vals {
		if _, ok := os.LookupEnv(k); ok {
			continue
		}
		_ = os.Setenv(k, v)
	}
}

// readFile parses a minimal KEY=VALUE file. Blank lines and lines beginning with
// '#' are ignored; surrounding quotes on the value are stripped.
func readFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("malformed line %q (want KEY=VALUE)", line)
		}
		key = strings.TrimSpace(key)
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		out[key] = val
	}
	return out, sc.Err()
}

func setStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok {
		*dst = v
	}
}

func setInt(dst *int, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func setInt64(dst *int64, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			*dst = n
		}
	}
}

// setBool parses a boolean variable, accepting the forms strconv.ParseBool takes
// (1, t, true, 0, f, false, and their cases). An unparseable value leaves the
// default intact rather than guessing.
func setBool(dst *bool, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func setDur(dst *time.Duration, key string) {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			*dst = d
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// secretBytes reads a secret from key, accepting hex, standard base64, or raw
// bytes (in that order of preference). It returns nil when the variable is unset.
func secretBytes(key string) []byte {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return nil
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) > 0 {
		return b
	}
	if b, err := base64.StdEncoding.DecodeString(v); err == nil && len(b) > 0 {
		return b
	}
	return []byte(v)
}

// secretBytesOrFile reads a secret inline from key, or from the file named by
// fileKey when the inline value is unset.
func secretBytesOrFile(key, fileKey string) []byte {
	if v := secretBytes(key); v != nil {
		return v
	}
	if path := os.Getenv(fileKey); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return b
		}
	}
	return nil
}
