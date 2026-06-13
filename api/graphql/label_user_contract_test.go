package graphql_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// commentFieldsQuery selects the IssueComment field set gh's --comments
// expansion reads, plus the connection's pageInfo and totalCount. The contract
// (R02-15) is that the whole document resolves and the seeded comment, authored
// by the repository owner, reports OWNER and viewerDidAuthor=true to its author.
const commentFieldsQuery = `query Comments($o: String!, $n: String!, $num: Int!) {
  repository(owner: $o, name: $n) {
    issue(number: $num) {
      comments(first: 5) {
        totalCount
        pageInfo { hasNextPage hasPreviousPage }
        nodes {
          body
          authorAssociation
          includesCreatedEdit
          isMinimized
          minimizedReason
          viewerDidAuthor
          reactionGroups { content }
          author { login }
        }
      }
    }
  }
}`

// TestIssueCommentConnectionAndFields confirms R02-15: the comment connection
// carries pageInfo and totalCount, and each comment fills the author-association,
// edit, minimize, reaction, and viewer fields gh selects.
func TestIssueCommentConnectionAndFields(t *testing.T) {
	srv, token := issueServer(t)
	got := post(t, srv, token, commentFieldsQuery, map[string]any{"o": "octocat", "n": "hello", "num": 1})

	var env struct {
		Data struct {
			Repository struct {
				Issue struct {
					Comments struct {
						TotalCount int `json:"totalCount"`
						PageInfo   struct {
							HasNextPage     bool `json:"hasNextPage"`
							HasPreviousPage bool `json:"hasPreviousPage"`
						} `json:"pageInfo"`
						Nodes []struct {
							Body                string  `json:"body"`
							AuthorAssociation   string  `json:"authorAssociation"`
							IncludesCreatedEdit bool    `json:"includesCreatedEdit"`
							IsMinimized         bool    `json:"isMinimized"`
							MinimizedReason     *string `json:"minimizedReason"`
							ViewerDidAuthor     bool    `json:"viewerDidAuthor"`
							ReactionGroups      []any   `json:"reactionGroups"`
							Author              struct {
								Login string `json:"login"`
							} `json:"author"`
						} `json:"nodes"`
					} `json:"comments"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("query returned errors, want none: %v\nbody %s", env.Errors, got)
	}

	c := env.Data.Repository.Issue.Comments
	if c.TotalCount != 1 {
		t.Errorf("comments.totalCount = %d, want 1", c.TotalCount)
	}
	if c.PageInfo.HasNextPage {
		t.Errorf("pageInfo.hasNextPage = true, want false for a single comment")
	}
	if len(c.Nodes) != 1 {
		t.Fatalf("comments.nodes = %d, want 1, body %s", len(c.Nodes), got)
	}
	n := c.Nodes[0]
	if n.Author.Login != "octocat" {
		t.Errorf("comment author = %q, want octocat", n.Author.Login)
	}
	if n.AuthorAssociation != "OWNER" {
		t.Errorf("authorAssociation = %q, want OWNER for the repo owner", n.AuthorAssociation)
	}
	if !n.ViewerDidAuthor {
		t.Errorf("viewerDidAuthor = false, want true (octocat authored and is the viewer)")
	}
	if n.IsMinimized {
		t.Errorf("isMinimized = true, want false")
	}
	if n.MinimizedReason != nil {
		t.Errorf("minimizedReason = %v, want null", *n.MinimizedReason)
	}
	if n.ReactionGroups == nil {
		t.Errorf("reactionGroups = null, want a (possibly empty) list")
	}
}

// labelUserQuery selects the Label fields R02-18 adds (isDefault, url,
// updatedAt) over the issue's labels connection, and the User fields it adds
// (avatarUrl(size:), isViewer, company, location, websiteUrl, twitterUsername,
// status) over the issue author. The contract is that the document resolves
// with zero errors and reports the seeded values.
const labelUserQuery = `query LabelsUser($o: String!, $n: String!) {
  repository(owner: $o, name: $n) {
    labels(first: 10) {
      totalCount
      edges { cursor node { name } }
      nodes { name isDefault url createdAt updatedAt }
    }
    issue(number: 1) {
      author {
        login
        ... on User {
          avatarUrl(size: 64)
          isViewer
          company
          location
          websiteUrl
          twitterUsername
          status { message }
        }
      }
    }
  }
}`

// TestLabelAndUserFieldGaps confirms R02-18: labels carry isDefault/url/
// updatedAt and the connection exposes edges, while the user carries the
// avatarUrl size argument, isViewer, and the profile fields (null when unset).
func TestLabelAndUserFieldGaps(t *testing.T) {
	srv, token := issueServer(t)
	got := post(t, srv, token, labelUserQuery, map[string]any{"o": "octocat", "n": "hello"})

	var env struct {
		Data struct {
			Repository struct {
				Labels struct {
					TotalCount int `json:"totalCount"`
					Edges      []struct {
						Cursor string `json:"cursor"`
						Node   struct {
							Name string `json:"name"`
						} `json:"node"`
					} `json:"edges"`
					Nodes []struct {
						Name      string `json:"name"`
						IsDefault bool   `json:"isDefault"`
						URL       string `json:"url"`
						CreatedAt string `json:"createdAt"`
						UpdatedAt string `json:"updatedAt"`
					} `json:"nodes"`
				} `json:"labels"`
				Issue struct {
					Author struct {
						Login           string  `json:"login"`
						AvatarURL       string  `json:"avatarUrl"`
						IsViewer        bool    `json:"isViewer"`
						Company         *string `json:"company"`
						Location        *string `json:"location"`
						WebsiteURL      *string `json:"websiteUrl"`
						TwitterUsername *string `json:"twitterUsername"`
						Status          *struct {
							Message *string `json:"message"`
						} `json:"status"`
					} `json:"author"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("unmarshal: %v, body %s", err, got)
	}
	if len(env.Errors) != 0 {
		t.Fatalf("query returned errors, want none: %v\nbody %s", env.Errors, got)
	}

	labels := env.Data.Repository.Labels
	if labels.TotalCount != 2 {
		t.Errorf("labels.totalCount = %d, want 2", labels.TotalCount)
	}
	if len(labels.Edges) != len(labels.Nodes) || len(labels.Edges) == 0 {
		t.Errorf("labels.edges = %d, nodes = %d, want equal and non-empty", len(labels.Edges), len(labels.Nodes))
	}
	var bug *struct {
		Name      string `json:"name"`
		IsDefault bool   `json:"isDefault"`
		URL       string `json:"url"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	for i := range labels.Nodes {
		if labels.Nodes[i].Name == "bug" {
			bug = &labels.Nodes[i]
		}
	}
	if bug == nil {
		t.Fatalf("no bug label in %+v", labels.Nodes)
	}
	if bug.IsDefault {
		t.Errorf("bug.isDefault = true, want false (created, not seeded as a default)")
	}
	if !strings.HasSuffix(bug.URL, "/octocat/hello/labels/bug") {
		t.Errorf("bug.url = %q, want it to end /octocat/hello/labels/bug", bug.URL)
	}
	if bug.UpdatedAt == "" || bug.UpdatedAt != bug.CreatedAt {
		t.Errorf("bug.updatedAt = %q, want it set and equal to createdAt %q", bug.UpdatedAt, bug.CreatedAt)
	}

	author := env.Data.Repository.Issue.Author
	if author.Login != "octocat" {
		t.Fatalf("issue author = %q, want octocat", author.Login)
	}
	if !strings.HasSuffix(author.AvatarURL, "?s=64") {
		t.Errorf("avatarUrl(size:64) = %q, want it to end ?s=64", author.AvatarURL)
	}
	if !author.IsViewer {
		t.Errorf("isViewer = false, want true (octocat is the viewer)")
	}
	if author.Company != nil || author.Location != nil || author.TwitterUsername != nil {
		t.Errorf("profile fields = company %v location %v twitter %v, want all null", author.Company, author.Location, author.TwitterUsername)
	}
	if author.WebsiteURL != nil && *author.WebsiteURL != "" {
		t.Errorf("websiteUrl = %v, want null for an unset blog", *author.WebsiteURL)
	}
	if author.Status != nil {
		t.Errorf("status = %+v, want null (statuses unmodeled)", author.Status)
	}
}
