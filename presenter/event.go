package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// Event renders one activity-feed entry. id is the database id as a string, and
// actor and repo are the compact forms the Events API embeds rather than the
// full objects the payload carries. The payload is served verbatim: it was
// rendered and stored when the event fanned out.
func (b *URLBuilder) Event(e *domain.Event) restmodel.Event {
	return restmodel.Event{
		ID:        strconv.FormatInt(e.ID, 10),
		Type:      e.Type,
		Actor:     b.eventActor(e.Actor),
		Repo:      b.eventRepo(e.Repo),
		Payload:   eventPayload(e.Payload),
		Public:    e.Public,
		CreatedAt: restmodel.NewTime(e.CreatedAt),
	}
}

// eventActor builds the compact actor object an event embeds.
func (b *URLBuilder) eventActor(u *domain.User) restmodel.EventActor {
	return restmodel.EventActor{
		ID:           u.ID,
		Login:        u.Login,
		DisplayLogin: u.Login,
		GravatarID:   "",
		URL:          b.UserAPI(u.Login),
		AvatarURL:    b.HTML("avatars", "u", strconv.FormatInt(u.ID, 10)),
	}
}

// eventRepo builds the compact repository reference an event embeds: the
// owner/name pair and the API URL, not the site URL.
func (b *URLBuilder) eventRepo(r *domain.Repo) restmodel.EventRepo {
	return restmodel.EventRepo{
		ID:   r.ID,
		Name: r.FullName(),
		URL:  b.RepoAPI(r.Owner.Login, r.Name),
	}
}

// eventPayload returns the stored payload, defaulting an empty value to an empty
// JSON object so the field is always present and well formed.
func eventPayload(raw []byte) []byte {
	if len(raw) == 0 {
		return []byte("{}")
	}
	return raw
}
