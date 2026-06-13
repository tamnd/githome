package restmodel

// Release is the body returned by every release endpoint. All pointer fields
// may be null; all array fields are always present (never null).
type Release struct {
	URL             string         `json:"url"`
	HTMLURL         string         `json:"html_url"`
	AssetsURL       string         `json:"assets_url"`
	UploadURL       string         `json:"upload_url"`
	TarballURL      string         `json:"tarball_url"`
	ZipballURL      string         `json:"zipball_url"`
	ID              int64          `json:"id"`
	NodeID          string         `json:"node_id"`
	TagName         string         `json:"tag_name"`
	TargetCommitish string         `json:"target_commitish"`
	Name            *string        `json:"name"`
	Body            *string        `json:"body"`
	Draft           bool           `json:"draft"`
	Prerelease      bool           `json:"prerelease"`
	CreatedAt       Time           `json:"created_at"`
	PublishedAt     *Time          `json:"published_at"`
	Author          SimpleUser     `json:"author"`
	Assets          []ReleaseAsset `json:"assets"`
}

// GeneratedNotes is the body of POST /releases/generate-notes: a suggested
// name and markdown body for a release.
type GeneratedNotes struct {
	Name string `json:"name"`
	Body string `json:"body"`
}

// ReleaseAsset is one downloadable file attached to a release.
type ReleaseAsset struct {
	URL                string     `json:"url"`
	BrowserDownloadURL string     `json:"browser_download_url"`
	ID                 int64      `json:"id"`
	NodeID             string     `json:"node_id"`
	Name               string     `json:"name"`
	Label              *string    `json:"label"`
	State              string     `json:"state"`
	ContentType        string     `json:"content_type"`
	Size               int64      `json:"size"`
	DownloadCount      int64      `json:"download_count"`
	CreatedAt          Time       `json:"created_at"`
	UpdatedAt          Time       `json:"updated_at"`
	Uploader           SimpleUser `json:"uploader"`
}
