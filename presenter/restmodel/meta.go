// Package restmodel holds the exact JSON wire structs Githome serves on the REST
// API. Field names, types, ordering of presence, and nullability all match
// GitHub; the json tags are the contract.
package restmodel

// Meta is the body of GET /meta. The address arrays describe the network ranges
// a deployment serves from; a self-hosted instance reports its own (often empty)
// ranges rather than github.com's. Arrays are always present, never null.
// InstalledVersion is the Githome version string; gh uses it for version-gated
// features and Renovate reads it for capability detection.
type Meta struct {
	VerifiablePasswordAuthentication bool              `json:"verifiable_password_authentication"`
	InstalledVersion                 string            `json:"installed_version"`
	SSHKeyFingerprints               map[string]string `json:"ssh_key_fingerprints"`
	SSHKeys                          []string          `json:"ssh_keys"`
	Hooks                            []string          `json:"hooks"`
	Web                              []string          `json:"web"`
	API                              []string          `json:"api"`
	Git                              []string          `json:"git"`
	Packages                         []string          `json:"packages"`
	Pages                            []string          `json:"pages"`
	Importer                         []string          `json:"importer"`
	Actions                          []string          `json:"actions"`
	Dependabot                       []string          `json:"dependabot"`
}

// RateLimit is the body of GET /rate_limit. The rate field mirrors resources.core
// and is retained for backward compatibility, exactly as GitHub does.
type RateLimit struct {
	Resources RateLimitResources `json:"resources"`
	Rate      RateLimitBucket    `json:"rate"`
}

// RateLimitResources is the set of named rate-limit buckets. The buckets Githome
// does not meter are still reported, at their full configured budget, so a client
// that reads a specific resource (go-github exposes every one) never sees a
// missing key.
type RateLimitResources struct {
	Core                      RateLimitBucket `json:"core"`
	Search                    RateLimitBucket `json:"search"`
	GraphQL                   RateLimitBucket `json:"graphql"`
	IntegrationManifest       RateLimitBucket `json:"integration_manifest"`
	SourceImport              RateLimitBucket `json:"source_import"`
	CodeScanningUpload        RateLimitBucket `json:"code_scanning_upload"`
	ActionsRunnerRegistration RateLimitBucket `json:"actions_runner_registration"`
	SCIM                      RateLimitBucket `json:"scim"`
	DependencySnapshots       RateLimitBucket `json:"dependency_snapshots"`
	CodeSearch                RateLimitBucket `json:"code_search"`
	AuditLog                  RateLimitBucket `json:"audit_log"`
}

// RateLimitBucket is one rate-limit window. Reset is a Unix epoch in seconds.
type RateLimitBucket struct {
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	Reset     int64  `json:"reset"`
	Used      int    `json:"used"`
	Resource  string `json:"resource"`
}
