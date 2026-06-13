package domain

import (
	"errors"
	"path"
	"strconv"
	"strings"

	"github.com/tamnd/githome/git"
)

// CommunityFile is one community-health file the profile reports: its path in
// the repository at the default branch. The presenter expands it into the
// {url, html_url} object GitHub returns; an absent file is a nil pointer.
type CommunityFile struct {
	Path string
}

// CommunityProfile is the repository's community-health summary: which of the
// recommended files are present at the default branch and a percentage over the
// checks GitHub counts (description, readme, code of conduct, contributing,
// license, issue template, pull request template). It backs
// GET /repos/{owner}/{repo}/community/profile.
type CommunityProfile struct {
	HealthPercentage    int
	HasDescription      bool
	CodeOfConduct       *CommunityFile
	Contributing        *CommunityFile
	IssueTemplate       *CommunityFile
	PullRequestTemplate *CommunityFile
	License             *CommunityFile
	Readme              *CommunityFile
}

// CommunityProfile computes the repository's community-health profile from the
// file tree at its default branch. An empty repository (no head) reports the
// description check alone, the way a freshly created repository does on GitHub.
func (s *RepoService) CommunityProfile(repo *Repo) (CommunityProfile, error) {
	prof := CommunityProfile{HasDescription: repo.Description != nil && strings.TrimSpace(*repo.Description) != ""}

	paths, err := s.repoFilePaths(repo)
	if err != nil && !errors.Is(err, ErrEmptyRepo) && !errors.Is(err, ErrGitNotFound) {
		return CommunityProfile{}, err
	}

	prof.Readme = firstMatch(paths, communityReadme)
	prof.License = firstMatch(paths, communityLicense)
	prof.Contributing = firstMatch(paths, communityContributing)
	prof.CodeOfConduct = firstMatch(paths, communityCodeOfConduct)
	prof.IssueTemplate = firstMatch(paths, communityIssueTemplate)
	prof.PullRequestTemplate = firstMatch(paths, communityPullRequestTemplate)

	checks := []bool{
		prof.HasDescription,
		prof.Readme != nil,
		prof.CodeOfConduct != nil,
		prof.Contributing != nil,
		prof.License != nil,
		prof.IssueTemplate != nil,
		prof.PullRequestTemplate != nil,
	}
	present := 0
	for _, ok := range checks {
		if ok {
			present++
		}
	}
	prof.HealthPercentage = present * 100 / len(checks)
	return prof, nil
}

// repoFilePaths reads the repository's file tree at the default branch and
// returns every blob path. It is the shared input the community-health and
// CODEOWNERS lookups scan.
func (s *RepoService) repoFilePaths(repo *Repo) ([]string, error) {
	tree, err := s.GetTree(repo, repo.DefaultBranch, true)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(tree.Entries))
	for _, e := range tree.Entries {
		if e.Type == git.ObjectBlob {
			out = append(out, e.Path)
		}
	}
	return out, nil
}

// The community-health file matchers, in GitHub's lookup precedence: the root,
// then .github/, then docs/. The first matching path wins so the reported file
// is the one GitHub would surface.
var (
	communityReadme              = matcher("README")
	communityLicense             = prefixMatcher([]string{""}, "LICENSE", "LICENSE", "COPYING")
	communityContributing        = matcher("CONTRIBUTING")
	communityCodeOfConduct       = matcher("CODE_OF_CONDUCT")
	communityIssueTemplate       = matcher("ISSUE_TEMPLATE")
	communityPullRequestTemplate = matcher("PULL_REQUEST_TEMPLATE")
)

// matcher builds a community matcher that accepts a file whose base name starts
// with name (case-insensitively, ignoring an extension) at the root, .github/,
// or docs/.
func matcher(name string) func([]string) *CommunityFile {
	return prefixMatcher([]string{"", ".github/", "docs/"}, name)
}

// prefixMatcher returns a function that finds the first path under one of dirs
// whose base name starts with any of the names, preferring earlier dirs and
// earlier names so the result is stable.
func prefixMatcher(dirs []string, names ...string) func([]string) *CommunityFile {
	return func(paths []string) *CommunityFile {
		for _, dir := range dirs {
			for _, name := range names {
				for _, p := range paths {
					d, base := path.Split(p)
					if !strings.EqualFold(d, dir) {
						continue
					}
					if strings.HasPrefix(strings.ToUpper(base), strings.ToUpper(name)) {
						return &CommunityFile{Path: p}
					}
				}
			}
		}
		return nil
	}
}

func firstMatch(paths []string, m func([]string) *CommunityFile) *CommunityFile {
	return m(paths)
}

// CodeownerError is one problem found while validating a CODEOWNERS file: the
// 1-based line, the offending source text, a short kind, a human message, and a
// suggested fix, matching the shape GitHub's codeowners/errors endpoint returns.
type CodeownerError struct {
	Line       int
	Column     int
	Kind       string
	Source     string
	Suggestion string
	Message    string
	Path       string
}

// CodeownersErrors validates the repository's CODEOWNERS file at the default
// branch and returns the syntax errors GitHub's endpoint reports: a rule with no
// owners and an owner token that is neither an @mention nor an email. A
// repository with no CODEOWNERS file (or an empty one) has no errors.
func (s *RepoService) CodeownersErrors(repo *Repo) ([]CodeownerError, error) {
	src, file, err := s.readCodeowners(repo)
	if err != nil {
		if errors.Is(err, ErrEmptyRepo) || errors.Is(err, ErrGitNotFound) {
			return []CodeownerError{}, nil
		}
		return nil, err
	}
	if src == nil {
		return []CodeownerError{}, nil
	}
	return validateCodeowners(string(src), file), nil
}

// readCodeowners loads the CODEOWNERS file from the locations GitHub honors,
// root then .github/ then docs/, returning its bytes and the path it was found
// at. A nil slice with a nil error means no CODEOWNERS file exists.
func (s *RepoService) readCodeowners(repo *Repo) ([]byte, string, error) {
	for _, p := range []string{"CODEOWNERS", ".github/CODEOWNERS", "docs/CODEOWNERS"} {
		res, err := s.Contents(repo, p, repo.DefaultBranch)
		if errors.Is(err, ErrGitNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		if res.IsDir || res.File == nil {
			continue
		}
		return res.File.Content, p, nil
	}
	return nil, "", nil
}

// validateCodeowners checks each rule line of a CODEOWNERS file and returns the
// errors GitHub reports. Blank lines and comments are skipped; a rule must name
// at least one owner, and each owner must be an @user, @org/team, or email.
func validateCodeowners(src, file string) []CodeownerError {
	out := []CodeownerError{}
	for i, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(stripComment(raw))
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			out = append(out, CodeownerError{
				Line:       i + 1,
				Column:     1,
				Kind:       "Missing owner",
				Source:     raw,
				Suggestion: "Add an owner, for example @octocat or octocat@example.com",
				Message:    "No owner specified on line " + itoaLine(i+1),
				Path:       file,
			})
			continue
		}
		for _, owner := range fields[1:] {
			if !validOwner(owner) {
				out = append(out, CodeownerError{
					Line:       i + 1,
					Column:     strings.Index(raw, owner) + 1,
					Kind:       "Invalid owner",
					Source:     raw,
					Suggestion: "Make sure the owner is a @user, @org/team, or an email address",
					Message:    "Invalid owner " + owner + " on line " + itoaLine(i+1),
					Path:       file,
				})
			}
		}
	}
	return out
}

// stripComment drops an unescaped trailing comment from a CODEOWNERS line.
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// validOwner reports whether tok is a valid CODEOWNERS owner: an @username,
// @org/team handle, or an email address.
func validOwner(tok string) bool {
	if strings.HasPrefix(tok, "@") {
		return len(tok) > 1 && !strings.HasSuffix(tok, "/")
	}
	at := strings.IndexByte(tok, '@')
	return at > 0 && at < len(tok)-1 && strings.IndexByte(tok[at+1:], '.') >= 0
}

func itoaLine(n int) string {
	return strconv.Itoa(n)
}
