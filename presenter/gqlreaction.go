package presenter

import (
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/gqlmodel"
)

// GQLReaction renders a domain reaction into the GraphQL Reaction shape, the
// node addReaction and removeReaction return. The user is rendered through the
// shared actor path, null for a ghost user. format selects the node-ID encoding.
func (b *URLBuilder) GQLReaction(r *domain.Reaction, format nodeid.Format) *gqlmodel.Reaction {
	if r == nil {
		return nil
	}
	out := &gqlmodel.Reaction{
		ID:        nodeid.Encode(nodeid.KindReaction, r.ID, format),
		Content:   GQLReactionContent(r.Content),
		CreatedAt: gqlmodel.NewDateTime(r.CreatedAt),
	}
	if r.User != nil {
		out.User = b.GQLUser(r.User, format)
	}
	return out
}

// GQLReactionContent maps a domain reaction content (GitHub's REST emoji name)
// onto the GraphQL ReactionContent enum.
func GQLReactionContent(rest string) gqlmodel.ReactionContent {
	switch rest {
	case "+1":
		return gqlmodel.ReactionContentThumbsUp
	case "-1":
		return gqlmodel.ReactionContentThumbsDown
	case "laugh":
		return gqlmodel.ReactionContentLaugh
	case "hooray":
		return gqlmodel.ReactionContentHooray
	case "confused":
		return gqlmodel.ReactionContentConfused
	case "heart":
		return gqlmodel.ReactionContentHeart
	case "rocket":
		return gqlmodel.ReactionContentRocket
	case "eyes":
		return gqlmodel.ReactionContentEyes
	}
	return gqlmodel.ReactionContent(rest)
}

// GQLReactionGroupsFromList summarises a list of domain reactions into the
// GraphQL reaction groups, the subject-level rollup addReaction and
// removeReaction return after the change. The slice is non-nil even when empty.
func GQLReactionGroupsFromList(rs []*domain.Reaction) []*gqlmodel.ReactionGroup {
	counts := map[string]int{}
	for _, r := range rs {
		counts[r.Content]++
	}
	groups := gqlReactionGroups(domain.ReactionRollup{TotalCount: len(rs), Counts: counts})
	out := make([]*gqlmodel.ReactionGroup, len(groups))
	for i := range groups {
		g := groups[i]
		out[i] = &g
	}
	return out
}

// RESTReactionContent maps a GraphQL ReactionContent enum value onto the domain
// reaction content (GitHub's REST emoji name) the reaction service stores. An
// unknown value passes through unchanged so the domain layer rejects it.
func RESTReactionContent(c gqlmodel.ReactionContent) string {
	switch c {
	case gqlmodel.ReactionContentThumbsUp:
		return "+1"
	case gqlmodel.ReactionContentThumbsDown:
		return "-1"
	case gqlmodel.ReactionContentLaugh:
		return "laugh"
	case gqlmodel.ReactionContentHooray:
		return "hooray"
	case gqlmodel.ReactionContentConfused:
		return "confused"
	case gqlmodel.ReactionContentHeart:
		return "heart"
	case gqlmodel.ReactionContentRocket:
		return "rocket"
	case gqlmodel.ReactionContentEyes:
		return "eyes"
	}
	return string(c)
}
