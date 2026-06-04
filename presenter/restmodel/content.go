package restmodel

// The git-data and contents wire shapes. These match GitHub's repository
// contents API (GET /repos/{owner}/{repo}/contents/{path}) and git database API
// (blobs, trees, commits, refs) byte for byte: the same keys, the same
// nullability, base64 content wrapped at 60 columns with newlines, and the
// unsigned verification block every object carries until signing lands.

// Content is one entry in a contents response. A file carries encoding and
// content; a directory listing is an array of entries with those two omitted.
// download_url is the raw URL for a file and null for a directory.
type Content struct {
	Type        string       `json:"type"`
	Encoding    string       `json:"encoding,omitempty"`
	Size        int64        `json:"size"`
	Name        string       `json:"name"`
	Path        string       `json:"path"`
	Content     string       `json:"content,omitempty"`
	SHA         string       `json:"sha"`
	URL         string       `json:"url"`
	GitURL      *string      `json:"git_url"`
	HTMLURL     *string      `json:"html_url"`
	DownloadURL *string      `json:"download_url"`
	Links       ContentLinks `json:"_links"`
}

// ContentLinks is the _links block on a content entry. GitHub orders the keys
// git, self, html.
type ContentLinks struct {
	Git  *string `json:"git"`
	Self string  `json:"self"`
	HTML *string `json:"html"`
}

// Tree is the body of GET /git/trees/{sha}. Truncated reports that the recursive
// walk hit the entry ceiling.
type Tree struct {
	SHA       string      `json:"sha"`
	URL       string      `json:"url"`
	Tree      []TreeEntry `json:"tree"`
	Truncated bool        `json:"truncated"`
}

// TreeEntry is one node in a tree. Subtree and submodule entries carry no size,
// and submodule entries no url, so both are omitted when absent.
type TreeEntry struct {
	Path string  `json:"path"`
	Mode string  `json:"mode"`
	Type string  `json:"type"`
	SHA  string  `json:"sha"`
	Size *int64  `json:"size,omitempty"`
	URL  *string `json:"url,omitempty"`
}

// Blob is the body of GET /git/blobs/{sha}. Content is base64 wrapped at 60
// columns; encoding is always "base64".
type Blob struct {
	SHA      string `json:"sha"`
	NodeID   string `json:"node_id"`
	Size     int64  `json:"size"`
	URL      string `json:"url"`
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
}

// GitIdentity is the name/email/date triple a git commit or tag records. The
// date is RFC3339 in UTC with a trailing Z.
type GitIdentity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Date  Time   `json:"date"`
}

// GitRef is the {sha, url} pointer git-data objects use for a tree or parent.
type GitRef struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}

// Verification is the commit/tag signature block. Until Githome verifies
// signatures every object is reported unsigned.
type Verification struct {
	Verified   bool    `json:"verified"`
	Reason     string  `json:"reason"`
	Signature  *string `json:"signature"`
	Payload    *string `json:"payload"`
	VerifiedAt *string `json:"verified_at"`
}

// GitCommit is the body of GET /git/commits/{sha}, the git-database view of a
// commit.
type GitCommit struct {
	SHA          string       `json:"sha"`
	NodeID       string       `json:"node_id"`
	URL          string       `json:"url"`
	HTMLURL      string       `json:"html_url"`
	Author       GitIdentity  `json:"author"`
	Committer    GitIdentity  `json:"committer"`
	Message      string       `json:"message"`
	Tree         GitRef       `json:"tree"`
	Parents      []GitRef     `json:"parents"`
	Verification Verification `json:"verification"`
}

// GitRefObject is the body of GET /git/ref/{ref} and one element of the
// GET /git/refs listing.
type GitRefObject struct {
	Ref    string       `json:"ref"`
	NodeID string       `json:"node_id"`
	URL    string       `json:"url"`
	Object GitRefTarget `json:"object"`
}

// GitRefTarget is the object a ref points at. Type is "commit" for a branch or
// lightweight tag and "tag" for an annotated tag.
type GitRefTarget struct {
	SHA  string `json:"sha"`
	Type string `json:"type"`
	URL  string `json:"url"`
}

// ShortCommit is the {sha, url} commit pointer the branch and tag listings use.
type ShortCommit struct {
	SHA string `json:"sha"`
	URL string `json:"url"`
}

// BranchShort is one element of the GET /branches listing.
type BranchShort struct {
	Name      string      `json:"name"`
	Commit    ShortCommit `json:"commit"`
	Protected bool        `json:"protected"`
}

// Tag is one element of the GET /tags listing.
type Tag struct {
	Name       string      `json:"name"`
	Commit     ShortCommit `json:"commit"`
	ZipballURL string      `json:"zipball_url"`
	TarballURL string      `json:"tarball_url"`
	NodeID     string      `json:"node_id"`
}

// RepoCommit is one element of the GET /commits listing and the commit object a
// single branch embeds. Author and Committer are the matched accounts, or null
// when the commit's email maps to no Githome user; M2 does not map emails yet,
// so they are null.
type RepoCommit struct {
	SHA         string         `json:"sha"`
	NodeID      string         `json:"node_id"`
	Commit      RepoCommitBody `json:"commit"`
	URL         string         `json:"url"`
	HTMLURL     string         `json:"html_url"`
	CommentsURL string         `json:"comments_url"`
	Author      *SimpleUser    `json:"author"`
	Committer   *SimpleUser    `json:"committer"`
	Parents     []CommitParent `json:"parents"`
}

// RepoCommitBody is the nested "commit" object on a RepoCommit.
type RepoCommitBody struct {
	Author       GitIdentity  `json:"author"`
	Committer    GitIdentity  `json:"committer"`
	Message      string       `json:"message"`
	Tree         GitRef       `json:"tree"`
	URL          string       `json:"url"`
	CommentCount int          `json:"comment_count"`
	Verification Verification `json:"verification"`
}

// CommitParent is one parent pointer on a RepoCommit.
type CommitParent struct {
	SHA     string `json:"sha"`
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}

// Branch is the body of GET /branches/{branch}: the named branch with its full
// head commit, navigation links, and protection state. Githome does not support
// branch protection yet, so an unprotected branch reports protection disabled.
type Branch struct {
	Name          string           `json:"name"`
	Commit        RepoCommit       `json:"commit"`
	Links         BranchLinks      `json:"_links"`
	Protected     bool             `json:"protected"`
	Protection    BranchProtection `json:"protection"`
	ProtectionURL string           `json:"protection_url"`
}

// BranchLinks is the _links block on a single branch.
type BranchLinks struct {
	HTML string `json:"html"`
	Self string `json:"self"`
}

// BranchProtection is the protection summary on a single branch.
type BranchProtection struct {
	Enabled              bool                       `json:"enabled"`
	RequiredStatusChecks BranchRequiredStatusChecks `json:"required_status_checks"`
}

// BranchRequiredStatusChecks is the required-status-checks summary. With
// protection off the enforcement level is "off" and the lists are empty.
type BranchRequiredStatusChecks struct {
	EnforcementLevel string   `json:"enforcement_level"`
	Contexts         []string `json:"contexts"`
	Checks           []string `json:"checks"`
}
