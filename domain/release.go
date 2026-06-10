package domain

import "time"

// Release is the domain view of a repository release. Draft releases are only
// visible to collaborators with write access; non-draft published releases are
// publicly visible. Assets are populated by the service when needed.
type Release struct {
	PK              int64
	ID              int64
	RepoPK          int64
	TagName         string
	TargetCommitish string
	Name            *string
	Body            *string
	Draft           bool
	Prerelease      bool
	Author          *User
	Assets          []*ReleaseAsset
	CreatedAt       time.Time
	PublishedAt     *time.Time
	UpdatedAt       time.Time
}

// ReleaseAsset is one downloadable file attached to a release. State is
// "uploaded" once the binary has been written; "open" while the upload is in
// progress. The binary lives on disk under DataDir/assets/{PK}.
type ReleaseAsset struct {
	PK            int64
	ID            int64
	ReleasePK     int64
	Name          string
	Label         *string
	ContentType   string
	Size          int64
	DownloadCount int64
	Uploader      *User
	State         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ReleaseFilter selects and orders a repository's release list.
type ReleaseFilter struct {
	IncludeDrafts bool
	Page          int
	PerPage       int
}
