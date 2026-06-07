// Package realworld is the real-world ingest subsystem: it exports the public
// metadata and git history of a handful of the largest GitHub-native
// repositories into a pinned, normalized corpus, then seeds that corpus into a
// Githome instance so the read, write, search, git, and event paths can be
// exercised at a scale the small development fixture never reaches.
//
// The subsystem is two stages that meet at one on-disk format, the Corpus:
//
//   - Stage A (export) turns a live source — a git mirror, the public GraphQL
//     API, the GH Archive event stream — into a normalized Corpus written to a
//     snapshot directory, pinned by a manifest so a later upstream change cannot
//     silently move the numbers.
//   - Stage B (seed) reads a Corpus snapshot and writes it into a target store
//     and git store through the bulk-seed write path, preserving the real
//     numbers and timestamps.
//
// Stage A needs network and, for the bulk API, credentials, so it is not
// exercised in unit tests; Stage B runs entirely against a local snapshot and a
// SQLite store, so the seeding, pseudonymization, replay, and capture logic is
// fully testable on the small fixtures checked in under testdata.
package realworld

import "time"

// Provenance records where a corpus value came from, so a reader never mistakes
// a modeled number for a measured one. It is carried in the manifest per repo
// and per synthesized field.
type Provenance string

const (
	// Official is copied verbatim from the public source: a real number, a real
	// timestamp, a real body.
	Official Provenance = "OFFICIAL"
	// Derived is computed from official data by a documented rule: a comment
	// count recounted from the comments, an event payload rendered from typed
	// columns.
	Derived Provenance = "DERIVED"
	// Modeled is synthesized to hit a realistic shape where the public source
	// has no per-row truth: the reaction reactor pool, the synthetic webhook
	// subscriptions, a pseudonymized login.
	Modeled Provenance = "MODELED"
)

// RepoRef identifies one repository in a corpus and pins its git history. The
// owner and name preserve the real namespace so URLs match real GitHub paths;
// MirrorURL and PinnedSHA freeze the git side the way DatasetRevision freezes
// the metadata side.
type RepoRef struct {
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
	MirrorURL     string `json:"mirror_url,omitempty"`
	PinnedSHA     string `json:"pinned_sha,omitempty"`
}

// NWO returns the owner/name form used in paths and logs.
func (r RepoRef) NWO() string { return r.Owner + "/" + r.Name }

// Corpus is the normalized metadata of one repository: the eight tables the
// public dataset and the GraphQL export both reduce to, with people named by
// login string and cross-references named by number or id. Stage B resolves the
// logins to user pks and the numbers to row pks as it seeds. A Corpus is the
// unit Stage A writes and Stage B reads; one snapshot directory holds one Corpus
// per repository.
type Corpus struct {
	Repo           RepoRef         `json:"repo"`
	Issues         []Issue         `json:"issues"`
	PullRequests   []PullRequest   `json:"pull_requests"`
	Comments       []Comment       `json:"comments"`
	Reviews        []Review        `json:"reviews"`
	ReviewComments []ReviewComment `json:"review_comments"`
	TimelineEvents []TimelineEvent `json:"timeline_events"`
	PRFiles        []PRFile        `json:"pr_files"`
	CommitStatuses []CommitStatus  `json:"commit_statuses"`
}

// Label is one label carried on an issue, deduped per repository at seed time.
type Label struct {
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
	Description string `json:"description,omitempty"`
}

// Issue is one row of the shared issue/PR table: an issue when IsPullRequest is
// false, the issue half of a pull request when true. Number is the per-repo
// number preserved verbatim. Reactions are counts per content
// (`{"+1": 5, "heart": 2}`), materialized into rows against the reactor pool at
// seed time. NodeID is the dataset's node id, recorded for provenance but never
// written: Githome mints its own GraphQL ids.
type Issue struct {
	Number          int64          `json:"number"`
	NodeID          string         `json:"node_id,omitempty"`
	IsPullRequest   bool           `json:"is_pull_request"`
	Title           string         `json:"title"`
	Body            string         `json:"body,omitempty"`
	State           string         `json:"state"`
	StateReason     string         `json:"state_reason,omitempty"`
	Author          string         `json:"author"`
	CreatedAt       time.Time      `json:"created_at"`
	UpdatedAt       time.Time      `json:"updated_at"`
	ClosedAt        *time.Time     `json:"closed_at,omitempty"`
	Labels          []Label        `json:"labels,omitempty"`
	Assignees       []string       `json:"assignees,omitempty"`
	MilestoneTitle  string         `json:"milestone_title,omitempty"`
	MilestoneNumber int64          `json:"milestone_number,omitempty"`
	Reactions       map[string]int `json:"reactions,omitempty"`
	CommentCount    int            `json:"comment_count"`
	Locked          bool           `json:"locked,omitempty"`
	LockReason      string         `json:"lock_reason,omitempty"`
}

// PullRequest is the PR-only extension joined to an Issue on Number.
type PullRequest struct {
	Number              int64      `json:"number"`
	Merged              bool       `json:"merged"`
	MergedAt            *time.Time `json:"merged_at,omitempty"`
	MergedBy            string     `json:"merged_by,omitempty"`
	MergeCommitSHA      string     `json:"merge_commit_sha,omitempty"`
	BaseRef             string     `json:"base_ref"`
	HeadRef             string     `json:"head_ref"`
	HeadSHA             string     `json:"head_sha"`
	Additions           int        `json:"additions"`
	Deletions           int        `json:"deletions"`
	ChangedFiles        int        `json:"changed_files"`
	Draft               bool       `json:"draft,omitempty"`
	MaintainerCanModify bool       `json:"maintainer_can_modify,omitempty"`
}

// Comment is one conversation comment. ID is the dataset id, used to order the
// db_id allocation deterministically; IssueNumber joins it to its issue.
type Comment struct {
	ID                int64          `json:"id"`
	IssueNumber       int64          `json:"issue_number"`
	Author            string         `json:"author"`
	Body              string         `json:"body"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	Reactions         map[string]int `json:"reactions,omitempty"`
	AuthorAssociation string         `json:"author_association,omitempty"`
}

// Review is one act of reviewing a pull request.
type Review struct {
	ID          int64      `json:"id"`
	PRNumber    int64      `json:"pr_number"`
	Author      string     `json:"author"`
	State       string     `json:"state"`
	Body        string     `json:"body,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	CommitID    string     `json:"commit_id,omitempty"`
}

// ReviewComment is one inline review comment anchored to a diff line. ReviewID
// joins it to its review; InReplyToID threads a reply under the comment that
// started a conversation, so threads reassemble.
type ReviewComment struct {
	ID          int64     `json:"id"`
	PRNumber    int64     `json:"pr_number"`
	ReviewID    int64     `json:"review_id"`
	Author      string    `json:"author"`
	Body        string    `json:"body"`
	Path        string    `json:"path"`
	Line        *int64    `json:"line,omitempty"`
	Side        string    `json:"side,omitempty"`
	DiffHunk    string    `json:"diff_hunk,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	InReplyToID *int64    `json:"in_reply_to_id,omitempty"`
}

// TimelineEvent is one lifecycle event. EventType maps to the issue_events
// event column; the typed columns and Data blob render into the event payload.
// This is the largest table in an automation-heavy corpus.
type TimelineEvent struct {
	ID          int64          `json:"id"`
	IssueNumber int64          `json:"issue_number"`
	EventType   string         `json:"event_type"`
	Actor       string         `json:"actor,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	LabelName   string         `json:"label_name,omitempty"`
	LabelColor  string         `json:"label_color,omitempty"`
	Assignee    string         `json:"assignee_login,omitempty"`
	Milestone   string         `json:"milestone_title,omitempty"`
	TitleFrom   string         `json:"title_from,omitempty"`
	TitleTo     string         `json:"title_to,omitempty"`
	RefType     string         `json:"ref_type,omitempty"`
	RefNumber   int64          `json:"ref_number,omitempty"`
	LockReason  string         `json:"lock_reason,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
}

// PRFile is one changed file of a pull request. These are not seeded as state:
// a PR's file list is derived from the git diff at request time. They are kept
// as a correctness oracle so the diff path can be checked against recorded
// add/delete counts.
type PRFile struct {
	PRNumber         int64  `json:"pr_number"`
	Path             string `json:"path"`
	Additions        int    `json:"additions"`
	Deletions        int    `json:"deletions"`
	Status           string `json:"status"`
	PreviousFilename string `json:"previous_filename,omitempty"`
}

// CommitStatus is one external pass/fail report against a head sha under a
// context. An automation-heavy repo carries many contexts per sha.
type CommitStatus struct {
	SHA         string    `json:"sha"`
	Context     string    `json:"context"`
	State       string    `json:"state"`
	Description string    `json:"description,omitempty"`
	TargetURL   string    `json:"target_url,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Logins returns the distinct set of every login named anywhere in the corpus,
// in first-seen order, so the seeder can build the user table once before it
// writes any row that references a user. First-seen order keeps the build
// deterministic.
func (c *Corpus) Logins() []string {
	seen := map[string]bool{}
	var out []string
	add := func(login string) {
		if login == "" || seen[login] {
			return
		}
		seen[login] = true
		out = append(out, login)
	}
	add(c.Repo.Owner)
	for _, iss := range c.Issues {
		add(iss.Author)
		for _, a := range iss.Assignees {
			add(a)
		}
	}
	for _, pr := range c.PullRequests {
		add(pr.MergedBy)
	}
	for _, cm := range c.Comments {
		add(cm.Author)
	}
	for _, r := range c.Reviews {
		add(r.Author)
	}
	for _, rc := range c.ReviewComments {
		add(rc.Author)
	}
	for _, ev := range c.TimelineEvents {
		add(ev.Actor)
		add(ev.Assignee)
	}
	return out
}
