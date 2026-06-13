package restmodel

// CommunityFileLink is one community-health file in the profile: the contents
// API url and the blob html_url GitHub points at. Githome reports the file's
// location only; it does not classify a license, so the license entry carries
// the same link shape rather than a fabricated SPDX object.
type CommunityFileLink struct {
	URL     string `json:"url"`
	HTMLURL string `json:"html_url"`
}

// CommunityFiles is the files block of the community profile. Each member is
// null when the repository has no such file at its default branch.
type CommunityFiles struct {
	CodeOfConduct       *CommunityFileLink `json:"code_of_conduct"`
	CodeOfConductFile   *CommunityFileLink `json:"code_of_conduct_file"`
	Contributing        *CommunityFileLink `json:"contributing"`
	IssueTemplate       *CommunityFileLink `json:"issue_template"`
	PullRequestTemplate *CommunityFileLink `json:"pull_request_template"`
	License             *CommunityFileLink `json:"license"`
	Readme              *CommunityFileLink `json:"readme"`
}

// CommunityProfile is the response of GET /repos/{owner}/{repo}/community/profile:
// the health percentage over the recommended files, the description, and the
// files block. documentation and updated_at are null and content_reports_enabled
// is false, matching a self-hosted forge without those subsystems.
type CommunityProfile struct {
	HealthPercentage      int            `json:"health_percentage"`
	Description           *string        `json:"description"`
	Documentation         *string        `json:"documentation"`
	Files                 CommunityFiles `json:"files"`
	UpdatedAt             *string        `json:"updated_at"`
	ContentReportsEnabled bool           `json:"content_reports_enabled"`
}

// CodeownerError is one validation problem in a CODEOWNERS file.
type CodeownerError struct {
	Line       int    `json:"line"`
	Column     int    `json:"column"`
	Kind       string `json:"kind"`
	Source     string `json:"source"`
	Suggestion string `json:"suggestion"`
	Message    string `json:"message"`
	Path       string `json:"path"`
}

// CodeownersErrors is the response of GET /repos/{owner}/{repo}/codeowners/errors.
type CodeownersErrors struct {
	Errors []CodeownerError `json:"errors"`
}
