package git

import "errors"

// The sentinel errors the git layer returns. Callers (the domain repo service)
// translate these into the right REST and GraphQL status: a missing repository
// or object is a 404, an empty repository yields an empty ref set rather than an
// error at the API edge.
var (
	// ErrRepoNotFound is returned when no bare repository exists on disk for
	// the requested handle.
	ErrRepoNotFound = errors.New("git: repository not found")

	// ErrObjectNotFound is returned when a ref, revision, or object id does not
	// resolve in the repository.
	ErrObjectNotFound = errors.New("git: object not found")

	// ErrEmptyRepository is returned by HEAD-dependent reads on a repository
	// that has no commits yet.
	ErrEmptyRepository = errors.New("git: repository is empty")

	// ErrPathNotFound is returned when a path does not exist at the given ref.
	ErrPathNotFound = errors.New("git: path not found")
)
