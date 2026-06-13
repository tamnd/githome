package restmodel

// NotificationThread is one element of GET /notifications and the body of
// GET /notifications/threads/{id}. GitHub renders the thread id as a string,
// unlike every other numeric id on the surface.
type NotificationThread struct {
	ID              string              `json:"id"`
	Repository      MinimalRepo         `json:"repository"`
	Subject         NotificationSubject `json:"subject"`
	Reason          string              `json:"reason"`
	Unread          bool                `json:"unread"`
	UpdatedAt       Time                `json:"updated_at"`
	LastReadAt      *Time               `json:"last_read_at"`
	URL             string              `json:"url"`
	SubscriptionURL string              `json:"subscription_url"`
}

// RepoSubscription is the body of the repository subscription endpoints
// (GET/PUT/DELETE /repos/{owner}/{repo}/subscription). Reason is always null,
// matching GitHub, which only sets it on notification thread subscriptions.
type RepoSubscription struct {
	Subscribed    bool    `json:"subscribed"`
	Ignored       bool    `json:"ignored"`
	Reason        *string `json:"reason"`
	CreatedAt     Time    `json:"created_at"`
	URL           string  `json:"url"`
	RepositoryURL string  `json:"repository_url"`
}

// NotificationSubject names what the thread is about: the issue or pull
// request title plus the API URLs a client follows to show it.
type NotificationSubject struct {
	Title            string `json:"title"`
	URL              string `json:"url"`
	LatestCommentURL string `json:"latest_comment_url"`
	Type             string `json:"type"`
}

// NotificationSubscription is the body of the thread subscription endpoints.
// Reason is always null for a thread subscription, matching GitHub.
type NotificationSubscription struct {
	Subscribed bool    `json:"subscribed"`
	Ignored    bool    `json:"ignored"`
	Reason     *string `json:"reason"`
	CreatedAt  Time    `json:"created_at"`
	URL        string  `json:"url"`
	ThreadURL  string  `json:"thread_url"`
}
