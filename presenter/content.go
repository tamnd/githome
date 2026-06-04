package presenter

import (
	"encoding/base64"
	"strings"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// ContentFile renders a single file's contents response. The body carries the
// base64-wrapped content GitHub emits; ref is the revision the path was read at
// and is echoed into the url query and the blob/raw links.
func (b *URLBuilder) ContentFile(owner, repoName, ref string, e git.PathEntry, content []byte) restmodel.Content {
	c := b.contentEntry(owner, repoName, ref, e)
	c.Encoding = "base64"
	c.Content = base64Wrap(content)
	return c
}

// ContentDir renders a directory listing: one entry per immediate child, with
// encoding and content omitted and a null download_url for sub-directories.
func (b *URLBuilder) ContentDir(owner, repoName, ref string, entries []git.PathEntry) []restmodel.Content {
	out := make([]restmodel.Content, 0, len(entries))
	for _, e := range entries {
		out = append(out, b.contentEntry(owner, repoName, ref, e))
	}
	return out
}

// contentEntry builds the shared shape for a contents entry, file or directory.
func (b *URLBuilder) contentEntry(owner, repoName, ref string, e git.PathEntry) restmodel.Content {
	repoBase := b.RepoAPI(owner, repoName)
	html := b.RepoHTML(owner, repoName)
	url := repoBase + "/contents/" + e.Path + "?ref=" + ref

	out := restmodel.Content{
		Type: contentType(e.Type, e.Mode),
		Size: e.Size,
		Name: e.Name,
		Path: e.Path,
		SHA:  e.SHA,
		URL:  url,
	}
	if out.Type == "dir" {
		g := repoBase + "/git/trees/" + e.SHA
		h := html + "/tree/" + ref + "/" + e.Path
		out.GitURL, out.HTMLURL = &g, &h
	} else {
		g := repoBase + "/git/blobs/" + e.SHA
		h := html + "/blob/" + ref + "/" + e.Path
		d := html + "/raw/" + ref + "/" + e.Path
		out.GitURL, out.HTMLURL, out.DownloadURL = &g, &h, &d
	}
	out.Links = restmodel.ContentLinks{Git: out.GitURL, Self: url, HTML: out.HTMLURL}
	return out
}

// Tree renders GET /git/trees/{sha}. Blob entries carry size and a blob url,
// subtree entries a tree url and no size, and submodule entries neither.
func (b *URLBuilder) Tree(owner, repoName string, t git.Tree) restmodel.Tree {
	repoBase := b.RepoAPI(owner, repoName)
	entries := make([]restmodel.TreeEntry, 0, len(t.Entries))
	for _, e := range t.Entries {
		te := restmodel.TreeEntry{Path: e.Path, Mode: e.Mode, Type: string(e.Type), SHA: e.SHA}
		switch e.Type {
		case git.ObjectBlob:
			sz := e.Size
			u := repoBase + "/git/blobs/" + e.SHA
			te.Size, te.URL = &sz, &u
		case git.ObjectTree:
			u := repoBase + "/git/trees/" + e.SHA
			te.URL = &u
		}
		entries = append(entries, te)
	}
	return restmodel.Tree{
		SHA:       t.SHA,
		URL:       repoBase + "/git/trees/" + t.SHA,
		Tree:      entries,
		Truncated: t.Truncated,
	}
}

// Blob renders GET /git/blobs/{sha} with base64-wrapped content.
func (b *URLBuilder) Blob(owner, repoName string, repoDBID int64, blob git.Blob) restmodel.Blob {
	return restmodel.Blob{
		SHA:      blob.SHA,
		NodeID:   nodeid.EncodeGitObject("blob", repoDBID, blob.SHA),
		Size:     blob.Size,
		URL:      b.RepoAPI(owner, repoName) + "/git/blobs/" + blob.SHA,
		Content:  base64Wrap(blob.Content),
		Encoding: "base64",
	}
}

// GitCommit renders GET /git/commits/{sha}, the git-database view of a commit.
func (b *URLBuilder) GitCommit(owner, repoName string, repoDBID int64, c git.Commit) restmodel.GitCommit {
	repoBase := b.RepoAPI(owner, repoName)
	parents := make([]restmodel.GitRef, 0, len(c.Parents))
	for _, p := range c.Parents {
		parents = append(parents, restmodel.GitRef{SHA: p, URL: repoBase + "/git/commits/" + p})
	}
	return restmodel.GitCommit{
		SHA:          c.SHA,
		NodeID:       nodeid.EncodeGitObject("commit", repoDBID, c.SHA),
		URL:          repoBase + "/git/commits/" + c.SHA,
		HTMLURL:      b.RepoHTML(owner, repoName) + "/commit/" + c.SHA,
		Author:       gitIdentity(c.Author),
		Committer:    gitIdentity(c.Committer),
		Message:      c.Message,
		Tree:         restmodel.GitRef{SHA: c.Tree, URL: repoBase + "/git/trees/" + c.Tree},
		Parents:      parents,
		Verification: unsignedVerification(),
	}
}

// RepoCommit renders one element of GET /commits and the commit a single branch
// embeds. The matched author/committer accounts are null until email-to-account
// mapping lands.
func (b *URLBuilder) RepoCommit(owner, repoName string, repoDBID int64, c git.Commit) restmodel.RepoCommit {
	repoBase := b.RepoAPI(owner, repoName)
	html := b.RepoHTML(owner, repoName)
	parents := make([]restmodel.CommitParent, 0, len(c.Parents))
	for _, p := range c.Parents {
		parents = append(parents, restmodel.CommitParent{
			SHA:     p,
			URL:     repoBase + "/commits/" + p,
			HTMLURL: html + "/commit/" + p,
		})
	}
	return restmodel.RepoCommit{
		SHA:    c.SHA,
		NodeID: nodeid.EncodeGitObject("commit", repoDBID, c.SHA),
		Commit: restmodel.RepoCommitBody{
			Author:       gitIdentity(c.Author),
			Committer:    gitIdentity(c.Committer),
			Message:      c.Message,
			Tree:         restmodel.GitRef{SHA: c.Tree, URL: repoBase + "/git/trees/" + c.Tree},
			URL:          repoBase + "/git/commits/" + c.SHA,
			CommentCount: 0,
			Verification: unsignedVerification(),
		},
		URL:         repoBase + "/commits/" + c.SHA,
		HTMLURL:     html + "/commit/" + c.SHA,
		CommentsURL: repoBase + "/commits/" + c.SHA + "/comments",
		Author:      nil,
		Committer:   nil,
		Parents:     parents,
	}
}

// GitRef renders GET /git/ref/{ref} and one element of GET /git/refs.
func (b *URLBuilder) GitRef(owner, repoName string, repoDBID int64, ref git.Ref) restmodel.GitRefObject {
	repoBase := b.RepoAPI(owner, repoName)
	objType, objURL := "commit", repoBase+"/git/commits/"+ref.Target
	if ref.Type == git.ObjectTag {
		objType, objURL = "tag", repoBase+"/git/tags/"+ref.Target
	}
	return restmodel.GitRefObject{
		Ref:    ref.Name,
		NodeID: nodeid.EncodeGitObject("ref", repoDBID, ref.Name),
		URL:    repoBase + "/git/" + ref.Name,
		Object: restmodel.GitRefTarget{SHA: ref.Target, Type: objType, URL: objURL},
	}
}

// BranchShort renders one element of GET /branches.
func (b *URLBuilder) BranchShort(owner, repoName string, br git.Branch) restmodel.BranchShort {
	return restmodel.BranchShort{
		Name:      br.Name,
		Commit:    restmodel.ShortCommit{SHA: br.Commit, URL: b.RepoAPI(owner, repoName) + "/commits/" + br.Commit},
		Protected: false,
	}
}

// Branch renders GET /branches/{branch} with the branch's full head commit and
// its (always disabled, for now) protection summary.
func (b *URLBuilder) Branch(owner, repoName string, repoDBID int64, br git.Branch, head git.Commit) restmodel.Branch {
	repoBase := b.RepoAPI(owner, repoName)
	return restmodel.Branch{
		Name:   br.Name,
		Commit: b.RepoCommit(owner, repoName, repoDBID, head),
		Links: restmodel.BranchLinks{
			HTML: b.RepoHTML(owner, repoName) + "/tree/" + br.Name,
			Self: repoBase + "/branches/" + br.Name,
		},
		Protected: false,
		Protection: restmodel.BranchProtection{
			Enabled: false,
			RequiredStatusChecks: restmodel.BranchRequiredStatusChecks{
				EnforcementLevel: "off",
				Contexts:         []string{},
				Checks:           []string{},
			},
		},
		ProtectionURL: repoBase + "/branches/" + br.Name + "/protection",
	}
}

// Tag renders one element of GET /tags.
func (b *URLBuilder) Tag(owner, repoName string, repoDBID int64, t git.Tag) restmodel.Tag {
	repoBase := b.RepoAPI(owner, repoName)
	return restmodel.Tag{
		Name:       t.Name,
		Commit:     restmodel.ShortCommit{SHA: t.Commit, URL: repoBase + "/commits/" + t.Commit},
		ZipballURL: repoBase + "/zipball/refs/tags/" + t.Name,
		TarballURL: repoBase + "/tarball/refs/tags/" + t.Name,
		NodeID:     nodeid.EncodeGitObject("ref", repoDBID, "refs/tags/"+t.Name),
	}
}

// contentType maps a git object kind and mode to GitHub's contents type label.
func contentType(t git.ObjectType, mode string) string {
	switch {
	case t == git.ObjectTree:
		return "dir"
	case t == git.ObjectCommit:
		return "submodule"
	case mode == "120000":
		return "symlink"
	default:
		return "file"
	}
}

// base64Wrap renders data as standard base64 wrapped at 60 columns with a
// trailing newline on each line, matching GitHub's content encoding.
func base64Wrap(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for i := 0; i < len(enc); i += 60 {
		end := min(i+60, len(enc))
		b.WriteString(enc[i:end])
		b.WriteByte('\n')
	}
	return b.String()
}

func gitIdentity(s git.Signature) restmodel.GitIdentity {
	return restmodel.GitIdentity{Name: s.Name, Email: s.Email, Date: restmodel.NewTime(s.When)}
}

func unsignedVerification() restmodel.Verification {
	return restmodel.Verification{Verified: false, Reason: "unsigned"}
}
