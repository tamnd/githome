package restmodel

import "encoding/json"

// Hook is the REST representation of a repository webhook: GET, POST, and PATCH
// on /repos/{owner}/{repo}/hooks all return it. The signing secret is never
// emitted; config.secret is rendered as a fixed mask when one is set and omitted
// when it is not, matching github.com.
type Hook struct {
	Type          string       `json:"type"`
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Active        bool         `json:"active"`
	Events        []string     `json:"events"`
	Config        HookConfig   `json:"config"`
	UpdatedAt     Time         `json:"updated_at"`
	CreatedAt     Time         `json:"created_at"`
	URL           string       `json:"url"`
	TestURL       string       `json:"test_url"`
	PingURL       string       `json:"ping_url"`
	DeliveriesURL string       `json:"deliveries_url"`
	LastResponse  HookResponse `json:"last_response"`
}

// HookConfig is the transport configuration of a webhook. insecure_ssl is a
// string ("0" or "1") the way GitHub serializes it, not a bool.
type HookConfig struct {
	ContentType string  `json:"content_type"`
	InsecureSSL string  `json:"insecure_ssl"`
	URL         string  `json:"url"`
	Secret      *string `json:"secret,omitempty"`
}

// HookResponse is the summary of a webhook's most recent delivery. Before any
// delivery the status is "unused" and the code and message are null.
type HookResponse struct {
	Code    *int    `json:"code"`
	Status  string  `json:"status"`
	Message *string `json:"message"`
}

// HookDelivery is one recorded delivery attempt. The list shape omits the
// request and response bodies; the single-delivery GET includes them, so those
// two fields and url are emitted only when set.
type HookDelivery struct {
	ID             int64                 `json:"id"`
	GUID           string                `json:"guid"`
	DeliveredAt    Time                  `json:"delivered_at"`
	Redelivery     bool                  `json:"redelivery"`
	Duration       float64               `json:"duration"`
	Status         string                `json:"status"`
	StatusCode     int                   `json:"status_code"`
	Event          string                `json:"event"`
	Action         *string               `json:"action"`
	InstallationID *int64                `json:"installation_id"`
	RepositoryID   *int64                `json:"repository_id"`
	URL            string                `json:"url,omitempty"`
	Request        *HookDeliveryRequest  `json:"request,omitempty"`
	Response       *HookDeliveryResponse `json:"response,omitempty"`
}

// HookDeliveryRequest is the request half of a delivery's full record: the
// headers Githome sent and the JSON body it posted.
type HookDeliveryRequest struct {
	Headers map[string]string `json:"headers"`
	Payload json.RawMessage   `json:"payload"`
}

// HookDeliveryResponse is the response half: the headers the receiver returned
// and its body as a string.
type HookDeliveryResponse struct {
	Headers map[string]string `json:"headers"`
	Payload string            `json:"payload"`
}

// Event is one entry in the activity feed the Events API serves. id is a string
// of the database id, type is the GitHub event type (PushEvent, IssuesEvent,
// and so on), and payload is the type-specific object the fan-out worker
// rendered and stored on the event.
type Event struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Actor     EventActor      `json:"actor"`
	Repo      EventRepo       `json:"repo"`
	Payload   json.RawMessage `json:"payload"`
	Public    bool            `json:"public"`
	CreatedAt Time            `json:"created_at"`
}

// EventActor is the compact actor object an Event embeds. display_login is the
// login as typed; Githome has no separate display form, so it equals login.
type EventActor struct {
	ID           int64  `json:"id"`
	Login        string `json:"login"`
	DisplayLogin string `json:"display_login"`
	GravatarID   string `json:"gravatar_id"`
	URL          string `json:"url"`
	AvatarURL    string `json:"avatar_url"`
}

// EventRepo is the compact repository reference an Event embeds: id, the
// owner/name pair, and the API URL.
type EventRepo struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

// WebhookPush is the body of a push delivery. commits carries the pushed range
// walked from the git layer, capped at twenty like GitHub, and head_commit is
// the new tip; before and after carry the moved shas a receiver keys
// synchronization off.
type WebhookPush struct {
	Ref        string          `json:"ref"`
	Before     string          `json:"before"`
	After      string          `json:"after"`
	Created    bool            `json:"created"`
	Deleted    bool            `json:"deleted"`
	Forced     bool            `json:"forced"`
	BaseRef    *string         `json:"base_ref"`
	Compare    string          `json:"compare"`
	Commits    []WebhookCommit `json:"commits"`
	HeadCommit *WebhookCommit  `json:"head_commit"`
	Repository Repository      `json:"repository"`
	Pusher     WebhookPusher   `json:"pusher"`
	Sender     SimpleUser      `json:"sender"`
}

// WebhookCommit is one commit in a push delivery's commits list and its
// head_commit field: the commit coordinates plus the per-file change lists.
type WebhookCommit struct {
	ID        string            `json:"id"`
	TreeID    string            `json:"tree_id"`
	Distinct  bool              `json:"distinct"`
	Message   string            `json:"message"`
	Timestamp Time              `json:"timestamp"`
	URL       string            `json:"url"`
	Author    WebhookCommitUser `json:"author"`
	Committer WebhookCommitUser `json:"committer"`
	Added     []string          `json:"added"`
	Removed   []string          `json:"removed"`
	Modified  []string          `json:"modified"`
}

// WebhookCommitUser is the git identity on a webhook commit. username is the
// matched account login and is omitted when the identity matches no account.
type WebhookCommitUser struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Username string `json:"username,omitempty"`
}

// WebhookPusher is the name/email pair a push delivery names the pusher by, the
// git identity form rather than the full user object the sender carries.
type WebhookPusher struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// WebhookPing is the body of a ping delivery: the zen line, the hook's id, the
// hook object itself, and the repository and sender like every other delivery.
type WebhookPing struct {
	Zen        string     `json:"zen"`
	HookID     int64      `json:"hook_id"`
	Hook       Hook       `json:"hook"`
	Repository Repository `json:"repository"`
	Sender     SimpleUser `json:"sender"`
}

// WebhookIssues is the body of an issues delivery. label is set only on
// labeled and unlabeled actions, naming the label the action moved.
type WebhookIssues struct {
	Action     string     `json:"action"`
	Issue      Issue      `json:"issue"`
	Label      *Label     `json:"label,omitempty"`
	Repository Repository `json:"repository"`
	Sender     SimpleUser `json:"sender"`
}

// WebhookPullRequest is the body of a pull_request delivery. before and after
// carry the moved head shas on a synchronize delivery and are omitted on every
// other action, matching github.com.
type WebhookPullRequest struct {
	Action      string      `json:"action"`
	Number      int64       `json:"number"`
	Before      string      `json:"before,omitempty"`
	After       string      `json:"after,omitempty"`
	PullRequest PullRequest `json:"pull_request"`
	Label       *Label      `json:"label,omitempty"`
	Repository  Repository  `json:"repository"`
	Sender      SimpleUser  `json:"sender"`
}

// WebhookIssueComment is the body of an issue_comment delivery: the comment
// alongside the issue it landed on.
type WebhookIssueComment struct {
	Action     string       `json:"action"`
	Issue      Issue        `json:"issue"`
	Comment    IssueComment `json:"comment"`
	Repository Repository   `json:"repository"`
	Sender     SimpleUser   `json:"sender"`
}

// WebhookPullRequestReview is the body of a pull_request_review delivery: the
// review alongside the pull request it was left on.
type WebhookPullRequestReview struct {
	Action      string      `json:"action"`
	Review      Review      `json:"review"`
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
	Sender      SimpleUser  `json:"sender"`
}

// WebhookPullRequestReviewComment is the body of a pull_request_review_comment
// delivery: the inline comment alongside its pull request.
type WebhookPullRequestReviewComment struct {
	Action      string        `json:"action"`
	Comment     ReviewComment `json:"comment"`
	PullRequest PullRequest   `json:"pull_request"`
	Repository  Repository    `json:"repository"`
	Sender      SimpleUser    `json:"sender"`
}

// PushEventPayload is the Events-API payload object for a PushEvent. It mirrors
// the push delivery's moved tips in the feed's compact form.
type PushEventPayload struct {
	PushID       int64             `json:"push_id"`
	Size         int               `json:"size"`
	DistinctSize int               `json:"distinct_size"`
	Ref          string            `json:"ref"`
	Head         string            `json:"head"`
	Before       string            `json:"before"`
	Commits      []PushEventCommit `json:"commits"`
}

// PushEventCommit is the compact commit object a PushEvent feed entry carries.
type PushEventCommit struct {
	SHA      string               `json:"sha"`
	Author   PushEventCommitIdent `json:"author"`
	Message  string               `json:"message"`
	Distinct bool                 `json:"distinct"`
	URL      string               `json:"url"`
}

// PushEventCommitIdent is the email/name pair on a feed commit.
type PushEventCommitIdent struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

// IssuesEventPayload is the Events-API payload object for an IssuesEvent.
type IssuesEventPayload struct {
	Action string `json:"action"`
	Issue  Issue  `json:"issue"`
}

// IssueCommentEventPayload is the Events-API payload object for an
// IssueCommentEvent.
type IssueCommentEventPayload struct {
	Action  string       `json:"action"`
	Issue   Issue        `json:"issue"`
	Comment IssueComment `json:"comment"`
}

// PullRequestReviewEventPayload is the Events-API payload object for a
// PullRequestReviewEvent.
type PullRequestReviewEventPayload struct {
	Action      string      `json:"action"`
	Review      Review      `json:"review"`
	PullRequest PullRequest `json:"pull_request"`
}

// PullRequestReviewCommentEventPayload is the Events-API payload object for a
// PullRequestReviewCommentEvent.
type PullRequestReviewCommentEventPayload struct {
	Action      string        `json:"action"`
	Comment     ReviewComment `json:"comment"`
	PullRequest PullRequest   `json:"pull_request"`
}

// PullRequestEventPayload is the Events-API payload object for a
// PullRequestEvent.
type PullRequestEventPayload struct {
	Action      string      `json:"action"`
	Number      int64       `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
}

// WebhookCreate is the body of a create delivery (branch or tag created).
type WebhookCreate struct {
	Ref          string     `json:"ref"`
	RefType      string     `json:"ref_type"` // "branch" or "tag"
	MasterBranch string     `json:"master_branch"`
	Description  *string    `json:"description"`
	PusherType   string     `json:"pusher_type"`
	Repository   Repository `json:"repository"`
	Sender       SimpleUser `json:"sender"`
}

// WebhookDelete is the body of a delete delivery (branch or tag deleted).
type WebhookDelete struct {
	Ref        string     `json:"ref"`
	RefType    string     `json:"ref_type"` // "branch" or "tag"
	PusherType string     `json:"pusher_type"`
	Repository Repository `json:"repository"`
	Sender     SimpleUser `json:"sender"`
}

// CreateEventPayload is the Events-API payload for a CreateEvent.
type CreateEventPayload struct {
	Ref          string `json:"ref"`
	RefType      string `json:"ref_type"`
	MasterBranch string `json:"master_branch"`
	Description  string `json:"description"`
}

// DeleteEventPayload is the Events-API payload for a DeleteEvent.
type DeleteEventPayload struct {
	Ref     string `json:"ref"`
	RefType string `json:"ref_type"`
}
