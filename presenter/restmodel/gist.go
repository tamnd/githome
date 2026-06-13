package restmodel

// GistFile is the wire shape for one file inside a gist.
type GistFile struct {
	Filename  string  `json:"filename"`
	Type      string  `json:"type"`
	Language  *string `json:"language"`
	RawURL    string  `json:"raw_url"`
	Size      int     `json:"size"`
	Truncated bool    `json:"truncated"`
	Content   *string `json:"content,omitempty"`
}

// Gist is the body returned by every gist endpoint.
type Gist struct {
	ID          string              `json:"id"`
	NodeID      string              `json:"node_id"`
	Description string              `json:"description"`
	Public      bool                `json:"public"`
	Owner       SimpleUser          `json:"owner"`
	User        *SimpleUser         `json:"user"`
	Files       map[string]GistFile `json:"files"`
	Forks       []any               `json:"forks"`
	History     []any               `json:"history"`
	Truncated   bool                `json:"truncated"`
	Comments    int                 `json:"comments"`
	GitPullURL  string              `json:"git_pull_url"`
	GitPushURL  string              `json:"git_push_url"`
	HTMLURL     string              `json:"html_url"`
	CommitsURL  string              `json:"commits_url"`
	ForksURL    string              `json:"forks_url"`
	CommentsURL string              `json:"comments_url"`
	CreatedAt   Time                `json:"created_at"`
	UpdatedAt   Time                `json:"updated_at"`
}

// GistComment is the body returned by gist comment endpoints.
type GistComment struct {
	ID        int64      `json:"id"`
	NodeID    string     `json:"node_id"`
	Body      string     `json:"body"`
	User      SimpleUser `json:"user"`
	CreatedAt Time       `json:"created_at"`
	UpdatedAt Time       `json:"updated_at"`
}
