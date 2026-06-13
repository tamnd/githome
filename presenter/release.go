package presenter

import (
	"fmt"
	"strings"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Release renders a domain.Release into the REST wire model.
func (b *URLBuilder) Release(owner, repo string, r *domain.Release, format nodeid.Format) restmodel.Release {
	base := b.API("repos", owner, repo, "releases", fmt.Sprintf("%d", r.ID))
	htmlBase := b.HTML(owner, repo, "releases", "tag", r.TagName)
	uploadURL := b.uploadURL(owner, repo, r.ID)

	out := restmodel.Release{
		URL:             base,
		HTMLURL:         htmlBase,
		AssetsURL:       base + "/assets",
		UploadURL:       uploadURL + "{?name,label}",
		TarballURL:      b.HTML(owner, repo, "archive", "refs", "tags", r.TagName+".tar.gz"),
		ZipballURL:      b.HTML(owner, repo, "archive", "refs", "tags", r.TagName+".zip"),
		ID:              r.ID,
		NodeID:          nodeid.Encode(nodeid.KindRelease, r.ID, format),
		TagName:         r.TagName,
		TargetCommitish: r.TargetCommitish,
		Name:            r.Name,
		Body:            r.Body,
		Draft:           r.Draft,
		Prerelease:      r.Prerelease,
		CreatedAt:       restmodel.NewTime(r.CreatedAt),
		Assets:          make([]restmodel.ReleaseAsset, 0, len(r.Assets)),
	}
	if r.PublishedAt != nil {
		t := restmodel.NewTime(*r.PublishedAt)
		out.PublishedAt = &t
	}
	if r.Author != nil {
		out.Author = b.SimpleUser(r.Author, format)
	}
	for _, a := range r.Assets {
		out.Assets = append(out.Assets, b.ReleaseAsset(owner, repo, r.ID, a, format))
	}
	return out
}

// ReleaseAsset renders a domain.ReleaseAsset into the REST wire model.
func (b *URLBuilder) ReleaseAsset(owner, repo string, releaseID int64, a *domain.ReleaseAsset, format nodeid.Format) restmodel.ReleaseAsset {
	base := b.API("repos", owner, repo, "releases", "assets", fmt.Sprintf("%d", a.ID))
	out := restmodel.ReleaseAsset{
		URL:                base,
		BrowserDownloadURL: b.HTML(owner, repo, "releases", "download", fmt.Sprintf("%d", releaseID), a.Name),
		ID:                 a.ID,
		NodeID:             nodeid.Encode(nodeid.KindReleaseAsset, a.ID, format),
		Name:               a.Name,
		Label:              a.Label,
		State:              a.State,
		ContentType:        a.ContentType,
		Size:               a.Size,
		DownloadCount:      a.DownloadCount,
		CreatedAt:          restmodel.NewTime(a.CreatedAt),
		UpdatedAt:          restmodel.NewTime(a.UpdatedAt),
	}
	if a.Uploader != nil {
		out.Uploader = b.SimpleUser(a.Uploader, format)
	}
	return out
}

// GeneratedNotes renders POST /releases/generate-notes: GitHub's "What's
// Changed" markdown built from the release's commit range, closed by a Full
// Changelog link that compares against the previous tag when one exists.
func (b *URLBuilder) GeneratedNotes(owner, repo string, n *domain.GeneratedNotes) restmodel.GeneratedNotes {
	var sb strings.Builder
	sb.WriteString("## What's Changed\n")
	for _, c := range n.Commits {
		subject := c.Message
		if i := strings.IndexByte(subject, '\n'); i >= 0 {
			subject = subject[:i]
		}
		sb.WriteString("* " + subject + " by " + c.Author.Name + "\n")
	}
	sb.WriteString("\n\n**Full Changelog**: ")
	if n.PreviousTagName != "" {
		sb.WriteString(b.HTML(owner, repo, "compare", n.PreviousTagName+"..."+n.TagName))
	} else {
		sb.WriteString(b.HTML(owner, repo, "commits", n.TagName))
	}
	return restmodel.GeneratedNotes{Name: n.TagName, Body: sb.String()}
}

// uploadURL returns the upload base URL for release assets, e.g.
// https://HOST/api/uploads/repos/{owner}/{repo}/releases/{id}/assets.
// The RFC 6570 template suffix {?name,label} is appended by Release().
func (b *URLBuilder) uploadURL(owner, repo string, releaseID int64) string {
	// Build upload base from the HTML host, matching GHES's /api/uploads/ prefix.
	scheme := b.api.Scheme
	host := b.api.Host
	return fmt.Sprintf("%s://%s/api/uploads/repos/%s/%s/releases/%d/assets",
		scheme, host, owner, repo, releaseID)
}
