package config

import (
	"strings"
	"testing"
)

// setMinimalEnv sets the smallest environment that makes Load succeed.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GITHOME_CONFIG_FILE", "")
	t.Setenv("GITHOME_HTML_BASE_URL", "https://git.example.com")
	t.Setenv("GITHOME_DATABASE_URL", "sqlite:///tmp/githome.db")
	t.Setenv("GITHOME_DATA_DIR", "/data")
	t.Setenv("GITHOME_SESSION_KEY", "session-key-raw-value-at-least-32-bytes!!")
	t.Setenv("GITHOME_TOKEN_PEPPER", "token-pepper-raw-16+chars")
	// Clear the URL overrides so derivation is exercised.
	t.Setenv("GITHOME_API_BASE_URL", "")
	t.Setenv("GITHOME_GRAPHQL_URL", "")
	t.Setenv("GITHOME_GIT_SSH_HOST", "")
	t.Setenv("GITHOME_GIT_SSH_PORT", "")
}

func TestLoadDerivesURLs(t *testing.T) {
	setMinimalEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.URLs.API.String(); got != "https://git.example.com/api/v3" {
		t.Errorf("derived API base = %q", got)
	}
	if got := c.URLs.GraphQL.String(); got != "https://git.example.com/api/graphql" {
		t.Errorf("derived GraphQL endpoint = %q", got)
	}
	if c.URLs.SSHHost != "git.example.com" {
		t.Errorf("SSH host should default to the HTML hostname, got %q", c.URLs.SSHHost)
	}
	if c.URLs.SSHPort != 22 {
		t.Errorf("SSH port should default to 22, got %d", c.URLs.SSHPort)
	}
	if c.RepoRoot() != "/data/repos" { // test sets GITHOME_DATA_DIR=/data explicitly
		t.Errorf("RepoRoot = %q", c.RepoRoot())
	}
}

func TestLoadExplicitURLsWin(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("GITHOME_API_BASE_URL", "https://git.example.com/custom/api")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.URLs.API.String(); got != "https://git.example.com/custom/api" {
		t.Errorf("explicit API base should win, got %q", got)
	}
}

func TestValidateRejectsUpstreamHost(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("GITHOME_HTML_BASE_URL", "https://"+"github"+".com")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "upstream") {
		t.Fatalf("expected upstream-host rejection, got %v", err)
	}
}

func TestValidateRejectsShortSecrets(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("GITHOME_SESSION_KEY", "tooshort")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SESSION_KEY") {
		t.Fatalf("expected session-key length error, got %v", err)
	}
}

func TestValidateRejectsUnknownDSN(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("GITHOME_DATABASE_URL", "mysql://nope")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "dialect") {
		t.Fatalf("expected DSN dialect error, got %v", err)
	}
}

func TestValidateRejectsBadGitBackend(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("GITHOME_GIT_BACKEND", "perforce")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "GIT_BACKEND") {
		t.Fatalf("expected git-backend error, got %v", err)
	}
}

func TestMissingHTMLBaseDerivedFromPort(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("GITHOME_HTML_BASE_URL", "")
	t.Setenv("GITHOME_LISTEN_HTTP", ":8080")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load without HTML base: %v", err)
	}
	if got := c.URLs.HTML.String(); got != "http://localhost:8080" {
		t.Errorf("derived HTML base = %q, want http://localhost:8080", got)
	}
}
