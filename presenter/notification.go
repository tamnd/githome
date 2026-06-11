package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// NotificationThread renders one thread for the notifications endpoints. The
// subject URL points at the issue or pull request API object, and the latest
// comment URL falls back to the subject itself when no comment is newer, the
// same fallback GitHub uses.
func (b *URLBuilder) NotificationThread(t *domain.NotificationThread, repo *domain.Repo, format nodeid.Format) restmodel.NotificationThread {
	id := strconv.FormatInt(t.ID, 10)
	subjectType, segment := "Issue", "issues"
	if t.SubjectIsPull {
		subjectType, segment = "PullRequest", "pulls"
	}
	subjectURL := b.RepoAPI(repo.Owner.Login, repo.Name) + "/" + segment + "/" + strconv.FormatInt(t.SubjectNumber, 10)
	return restmodel.NotificationThread{
		ID:         id,
		Repository: b.minimalRepo(repo, format),
		Subject: restmodel.NotificationSubject{
			Title:            t.SubjectTitle,
			URL:              subjectURL,
			LatestCommentURL: subjectURL,
			Type:             subjectType,
		},
		Reason:          t.Reason,
		Unread:          t.Unread,
		UpdatedAt:       restmodel.NewTime(t.UpdatedAt),
		LastReadAt:      timePtr(t.LastReadAt),
		URL:             b.API("notifications", "threads", id),
		SubscriptionURL: b.API("notifications", "threads", id, "subscription"),
	}
}

// NotificationSubscription renders the thread's subscription state.
func (b *URLBuilder) NotificationSubscription(t *domain.NotificationThread) restmodel.NotificationSubscription {
	id := strconv.FormatInt(t.ID, 10)
	return restmodel.NotificationSubscription{
		Subscribed: t.Subscribed,
		Ignored:    t.Ignored,
		Reason:     nil,
		CreatedAt:  restmodel.NewTime(t.CreatedAt),
		URL:        b.API("notifications", "threads", id, "subscription"),
		ThreadURL:  b.API("notifications", "threads", id),
	}
}
