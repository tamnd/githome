package rest

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-mizu/mizu"

	"github.com/tamnd/githome/auth"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/etag"
	"github.com/tamnd/githome/presenter/restmodel"
	"github.com/tamnd/githome/store"
)

// issueCreateBody is the POST /issues request. milestone is the milestone
// number, omitted to leave the issue unscheduled. assignee is the legacy
// singular form, honored when assignees is absent.
type issueCreateBody struct {
	Title     string    `json:"title"`
	Body      *string   `json:"body"`
	Labels    labelList `json:"labels"`
	Assignee  *string   `json:"assignee"`
	Assignees []string  `json:"assignees"`
	Milestone *int64    `json:"milestone"`
}

// labelList decodes a JSON array whose members are label names, either plain
// strings or objects carrying a "name" field. GitHub accepts both shapes,
// mixed freely, on issue create and edit.
type labelList []string

func (l *labelList) UnmarshalJSON(data []byte) error {
	var arr []json.RawMessage
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	out := make([]string, 0, len(arr))
	for _, raw := range arr {
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			out = append(out, s)
			continue
		}
		var obj struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &obj); err != nil {
			return err
		}
		out = append(out, obj.Name)
	}
	*l = out
	return nil
}

// issueEditBody is the PATCH /issues/{number} request. A nil field is left
// unchanged; a present field is written. milestone present-but-null clears the
// milestone, matching GitHub's "set to null to remove" behavior.
type issueEditBody struct {
	Title        *string   `json:"title"`
	Body         *string   `json:"body"`
	State        *string   `json:"state"`
	StateReason  *string   `json:"state_reason"`
	Labels       *[]string `json:"labels"`
	Assignees    *[]string `json:"assignees"`
	Milestone    *int64    `json:"milestone"`
	milestoneSet bool
}

// commentBody is the create/edit comment request.
type commentBody struct {
	Body string `json:"body"`
}

// labelBody is the create/edit label request. NewName renames on edit.
type labelBody struct {
	Name        string  `json:"name"`
	NewName     string  `json:"new_name"`
	Color       string  `json:"color"`
	Description *string `json:"description"`
}

// milestoneBody is the create/edit milestone request.
type milestoneBody struct {
	Title       *string `json:"title"`
	State       *string `json:"state"`
	Description *string `json:"description"`
	DueOn       *string `json:"due_on"`
}

// reactionBody is the create reaction request.
type reactionBody struct {
	Content string `json:"content"`
}

// handleIssuesList serves GET /repos/{owner}/{repo}/issues. The state, labels,
// creator, assignee, milestone, sort, and direction queries narrow the page.
// An opaque ?cursor= token (from a previous response's Link rel="next") switches
// the store query to a keyset seek instead of OFFSET, so deep-page walks are
// O(1) in page depth rather than degrading linearly.
func handleIssuesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		page, perr := parsePageFor(c, "Issue")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		q := domain.IssueQuery{
			State:         c.Query("state"),
			Labels:        splitCSV(c.Query("labels")),
			CreatorLogin:  c.Query("creator"),
			AssigneeLogin: c.Query("assignee"),
			Sort:          c.Query("sort"),
			Direction:     c.Query("direction"),
			Page:          page.Page,
			PerPage:       page.PerPage,
			Cursor:        c.Query("cursor"),
		}
		if n, ok := queryInt64(c, "milestone"); ok {
			q.MilestoneNumber = &n
		}

		// Flat read path: a cursor follow-up on the default newest-first order
		// seeks straight to the page and skips the COUNT that page-number
		// navigation needs for rel="last", so deep walks of a
		// several-hundred-thousand-issue repo cost the page, not a full count
		// plus a deep OFFSET scan. The forward hop stays a cursor; prev and
		// first appear as page-number links from the page hint the cursor
		// link carries.
		if q.Cursor != "" && issueCursorEligible(q) {
			issues, hasMore, err := d.Issues.ListIssuesPage(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), q)
			if issueError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			out := make([]restmodel.Issue, 0, len(issues))
			for _, iss := range issues {
				out = append(out, d.URLs.Issue(c.Param("owner"), c.Param("repo"), iss, d.NodeFormat))
			}
			var nextCursor string
			if hasMore && len(issues) > 0 {
				last := issues[len(issues)-1]
				nextCursor = store.EncodeCursor(store.IssueCursor{CreatedAt: last.CreatedAt, Number: last.Number})
			}
			writeNextCursorLink(c.Writer(), c.Request(), d.URLs, page, nextCursor)
			conditionalJSON(c.Writer(), c.Request(), http.StatusOK, out)
			return nil
		}

		// Seed a version ETag from one aggregate over the filtered window
		// (count + latest updated_at) and short-circuit a polling client's
		// If-None-Match hit before the page is fetched, assembled, or
		// marshaled. Every write that changes the list body bumps an issue's
		// updated_at or the count, so the seed tracks the representation.
		total, marker, err := d.Issues.ListIssuesVersion(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), q)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		tag := etag.Version(c.Request().URL.RequestURI()+"|"+marker, int64(total))
		if notModified(c.Writer(), c.Request(), tag) {
			return nil
		}

		issues, err := d.Issues.ListIssuesWindow(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), q)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.Issue, 0, len(issues))
		for _, iss := range issues {
			out = append(out, d.URLs.Issue(c.Param("owner"), c.Param("repo"), iss, d.NodeFormat))
		}
		page.Total = total

		// Emit a cursor-based next URL when using the default sort (created
		// DESC): following it uses a keyset seek instead of OFFSET.
		// For explicit custom sorts or reverse direction, fall back to page numbers.
		var nextCursor string
		if len(issues) > 0 && page.HasNextPage() && issueCursorEligible(q) {
			last := issues[len(issues)-1]
			nextCursor = store.EncodeCursor(store.IssueCursor{
				CreatedAt: last.CreatedAt,
				Number:    last.Number,
			})
		}
		writeLinkHeaderCursor(c.Writer(), c.Request(), d.URLs, page, nextCursor)
		conditionalVersioned(c.Writer(), c.Request(), http.StatusOK, out, tag)
		return nil
	}
}

// issueCursorEligible reports whether an issue query can be served by the keyset
// seek and so advertise a cursor next-link: it uses the default newest-first
// created order the seek index covers. Custom sorts and ascending direction fall
// back to OFFSET with page-number links.
func issueCursorEligible(q domain.IssueQuery) bool {
	return (q.Sort == "" || q.Sort == "created") &&
		(q.Direction == "" || strings.EqualFold(q.Direction, "desc"))
}

// handleIssueCreate serves POST /repos/{owner}/{repo}/issues.
func handleIssueCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body issueCreateBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Title == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Issue", Field: "title", Code: "missing_field"}))
			return nil
		}
		assignees := body.Assignees
		if len(assignees) == 0 && body.Assignee != nil && *body.Assignee != "" {
			assignees = []string{*body.Assignee}
		}
		in := domain.IssueInput{
			Title:           body.Title,
			Body:            body.Body,
			Labels:          body.Labels,
			AssigneeLogins:  assignees,
			MilestoneNumber: body.Milestone,
		}
		actor := auth.ActorFrom(c.Request().Context())
		iss, err := d.Issues.CreateIssue(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), in)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Notifications != nil {
			text := ""
			if iss.Body != nil {
				text = *iss.Body
			}
			d.Notifications.NotifyIssueOpened(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), iss.Number, text)
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Issue(c.Param("owner"), c.Param("repo"), iss, d.NodeFormat))
		return nil
	}
}

// handleIssueGet serves GET /repos/{owner}/{repo}/issues/{number}.
func handleIssueGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		iss, err := d.Issues.GetIssue(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		tag := etag.Version("issue", iss.ID, iss.UpdatedAt.UnixNano())
		conditionalVersioned(c.Writer(), c.Request(), http.StatusOK, d.URLs.Issue(c.Param("owner"), c.Param("repo"), iss, d.NodeFormat), tag)
		return nil
	}
}

// handleIssueEdit serves PATCH /repos/{owner}/{repo}/issues/{number}.
func handleIssueEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		body, ok := decodeIssueEdit(c)
		if !ok {
			return nil
		}
		patch := domain.IssuePatch{
			Title:          body.Title,
			Body:           body.Body,
			State:          body.State,
			StateReason:    body.StateReason,
			Labels:         body.Labels,
			AssigneeLogins: body.Assignees,
		}
		if body.milestoneSet {
			if body.Milestone == nil {
				patch.ClearMilestone = true
			} else {
				patch.MilestoneNumber = body.Milestone
			}
		}
		actor := auth.ActorFrom(c.Request().Context())
		iss, err := d.Issues.EditIssue(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, patch)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Notifications != nil && body.Assignees != nil {
			d.Notifications.NotifyAssigned(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, *body.Assignees)
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Issue(c.Param("owner"), c.Param("repo"), iss, d.NodeFormat))
		return nil
	}
}

// handleIssueCommentsGet dispatches the two GET shapes that share the
// /issues/{seg1}/{seg2} space and that net/http's mux cannot tell apart on its
// own, because neither "/issues/{number}/comments" nor "/issues/comments/{id}"
// is strictly more specific than the other. When the first segment is the
// literal "comments" the request is a comment fetched by id; when the second
// segment is "comments" it is an issue's comment list. Anything else under this
// two-segment shape that a more specific route did not claim is a 404.
func handleIssueCommentsGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		seg1, seg2 := c.Param("seg1"), c.Param("seg2")
		switch {
		case seg1 == "comments":
			id, ok := parseInt64(seg2)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return commentGet(d, c, id)
		case seg2 == "comments":
			number, ok := parseInt64(seg1)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			return commentsList(d, c, number)
		default:
			writeError(c.Writer(), errNotFound())
			return nil
		}
	}
}

// commentsList serves the issue's comment list, oldest first.
func commentsList(d Deps, c *mizu.Ctx, number int64) error {
	actor := auth.ActorFrom(c.Request().Context())
	page, perr := parsePageFor(c, "Issue")
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	since, perr := timeQuery(c, "since")
	if perr != nil {
		writeError(c.Writer(), perr)
		return nil
	}
	comments, err := d.Issues.ListComments(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, int64(page.Page), int64(page.PerPage))
	if issueError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	// A full page might be the last one; peek at the next page so rel="next"
	// only appears when another comment exists.
	hasNext := false
	if len(comments) == page.PerPage {
		peek, err := d.Issues.ListComments(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, int64(page.Page+1), int64(page.PerPage))
		if err == nil {
			hasNext = sinceFilter(peek, since) > 0
		}
	}
	comments = comments[:sinceFilter(comments, since)]
	out := make([]restmodel.IssueComment, 0, len(comments))
	for _, cm := range comments {
		out = append(out, d.URLs.IssueComment(c.Param("owner"), c.Param("repo"), cm, d.NodeFormat))
	}
	writeLinkHeaderUncounted(c.Writer(), c.Request(), d.URLs, page, hasNext)
	writeJSON(c.Writer(), http.StatusOK, out)
	return nil
}

// sinceFilter compacts comments whose last update is at or after since to the
// front of the slice in place, preserving order, and returns how many survived.
// A nil since keeps every comment. GitHub's since filter on the comment list is
// "updated at or after", which this applies to the already-fetched page.
func sinceFilter(comments []*domain.Comment, since *time.Time) int {
	if since == nil {
		return len(comments)
	}
	n := 0
	for _, cm := range comments {
		if !cm.UpdatedAt.Before(*since) {
			comments[n] = cm
			n++
		}
	}
	return n
}

// commentGet serves a single comment fetched by its public id.
func commentGet(d Deps, c *mizu.Ctx, id int64) error {
	actor := auth.ActorFrom(c.Request().Context())
	cm, err := d.Issues.GetComment(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), id)
	if issueError(c.Writer(), err) {
		return nil
	}
	if err != nil {
		return err
	}
	writeJSON(c.Writer(), http.StatusOK, d.URLs.IssueComment(c.Param("owner"), c.Param("repo"), cm, d.NodeFormat))
	return nil
}

// handleIssueCommentCreate serves POST /repos/{owner}/{repo}/issues/{number}/comments.
func handleIssueCommentCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body commentBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Body == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "IssueComment", Field: "body", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		cm, err := d.Issues.CreateComment(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, body.Body)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.Notifications != nil {
			d.Notifications.NotifyIssueComment(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, body.Body)
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.IssueComment(c.Param("owner"), c.Param("repo"), cm, d.NodeFormat))
		return nil
	}
}

// handleCommentEdit serves PATCH /repos/{owner}/{repo}/issues/comments/{id}.
func handleCommentEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body commentBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Body == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "IssueComment", Field: "body", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		cm, err := d.Issues.EditComment(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), id, body.Body)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.IssueComment(c.Param("owner"), c.Param("repo"), cm, d.NodeFormat))
		return nil
	}
}

// handleCommentDelete serves DELETE /repos/{owner}/{repo}/issues/comments/{id}.
func handleCommentDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		err := d.Issues.DeleteComment(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), id)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleIssueDeleteDispatch dispatches the two DELETE shapes that share
// /issues/{seg1}/{seg2} and that mizu cannot tell apart without a dispatcher,
// because neither "/issues/comments/{id}" nor "/issues/{number}/labels" is
// strictly more specific than the other in the router's eyes.
//
// Routing table:
//
//	seg1 == "comments"             → delete issue comment by id
//	seg1 is a number && seg2 == "labels" → remove all labels from issue
func handleIssueDeleteDispatch(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		seg1, seg2 := c.Param("seg1"), c.Param("seg2")
		switch {
		case seg1 == "comments":
			id, ok := parseInt64(seg2)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			ctx := c.Request().Context()
			actor := auth.ActorFrom(ctx)
			err := d.Issues.DeleteComment(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), id)
			if issueError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		case seg2 == "labels":
			number, ok := parseInt64(seg1)
			if !ok {
				writeError(c.Writer(), errNotFound())
				return nil
			}
			ctx := c.Request().Context()
			actor := auth.ActorFrom(ctx)
			if !actor.IsUser() {
				writeError(c.Writer(), errRequiresAuth())
				return nil
			}
			empty := []string{}
			_, err := d.Issues.EditIssue(ctx, actor.UserID, c.Param("owner"), c.Param("repo"), number, domain.IssuePatch{
				Labels: &empty,
			})
			if issueError(c.Writer(), err) {
				return nil
			}
			if err != nil {
				return err
			}
			c.Writer().WriteHeader(http.StatusNoContent)
			return nil
		default:
			writeError(c.Writer(), errNotFound())
			return nil
		}
	}
}

// handleLabelsList serves GET /repos/{owner}/{repo}/labels.
func handleLabelsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		labels, err := d.Issues.ListLabels(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"))
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		out := make([]restmodel.Label, 0, len(labels))
		for _, l := range labels {
			out = append(out, d.URLs.Label(c.Param("owner"), c.Param("repo"), l, d.NodeFormat))
		}
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, out)
		return nil
	}
}

// handleLabelCreate serves POST /repos/{owner}/{repo}/labels.
func handleLabelCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body labelBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Name == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Label", Field: "name", Code: "missing_field"}))
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		l, err := d.Issues.CreateLabel(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), domain.LabelInput{Name: body.Name, Color: body.Color, Description: body.Description})
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Label(c.Param("owner"), c.Param("repo"), l, d.NodeFormat))
		return nil
	}
}

// handleLabelGet serves GET /repos/{owner}/{repo}/labels/{name}.
func handleLabelGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		l, err := d.Issues.GetLabel(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), c.Param("name"))
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, d.URLs.Label(c.Param("owner"), c.Param("repo"), l, d.NodeFormat))
		return nil
	}
}

// handleLabelEdit serves PATCH /repos/{owner}/{repo}/labels/{name}.
func handleLabelEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body labelBody
		if !decodeJSON(c, &body) {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		l, err := d.Issues.UpdateLabel(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), c.Param("name"), domain.LabelInput{Name: body.NewName, Color: body.Color, Description: body.Description})
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Label(c.Param("owner"), c.Param("repo"), l, d.NodeFormat))
		return nil
	}
}

// handleLabelDelete serves DELETE /repos/{owner}/{repo}/labels/{name}.
func handleLabelDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		err := d.Issues.DeleteLabel(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), c.Param("name"))
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleMilestonesList serves GET /repos/{owner}/{repo}/milestones. The list is
// ordered by the sort/direction pair GitHub accepts (sort=due_on|completeness,
// direction=asc|desc, defaulting to due_on ascending) and then paged with a Link
// header, so a milestone client that walks pages sees a stable order.
func handleMilestonesList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		actor := auth.ActorFrom(c.Request().Context())
		page, perr := parsePageFor(c, "Milestone")
		if perr != nil {
			writeError(c.Writer(), perr)
			return nil
		}
		ms, err := d.Issues.ListMilestones(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), c.Query("state"))
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		sortMilestones(ms, c.Query("sort"), c.Query("direction"))
		out := make([]restmodel.Milestone, 0, len(ms))
		for _, m := range ms {
			out = append(out, d.URLs.Milestone(c.Param("owner"), c.Param("repo"), m, d.NodeFormat))
		}
		paged := paginateSlice(&page, out)
		writeLinkHeader(c.Writer(), c.Request(), d.URLs, page)
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, paged)
		return nil
	}
}

// sortMilestones orders milestones in place by the GitHub sort/direction pair.
// sort=due_on (the default) orders by the due date, with milestones that have no
// due date sorting after the dated ones; sort=completeness orders by the share
// of closed issues. direction=desc reverses the ascending default. A milestone's
// number breaks ties so the order is deterministic.
func sortMilestones(ms []*domain.Milestone, sortKey, direction string) {
	desc := direction == "desc"
	less := func(a, b *domain.Milestone) bool {
		switch sortKey {
		case "completeness":
			ca, cb := completeness(a), completeness(b)
			if ca != cb {
				return ca < cb
			}
		default: // due_on
			switch {
			case a.DueOn == nil && b.DueOn == nil:
				// both undated: fall through to the number tie-break
			case a.DueOn == nil:
				return false // undated sorts after dated
			case b.DueOn == nil:
				return true
			case !a.DueOn.Equal(*b.DueOn):
				return a.DueOn.Before(*b.DueOn)
			}
		}
		return a.Number < b.Number
	}
	sort.SliceStable(ms, func(i, j int) bool {
		if desc {
			return less(ms[j], ms[i])
		}
		return less(ms[i], ms[j])
	})
}

// completeness is the fraction of a milestone's issues that are closed, used as
// the sort=completeness key. A milestone with no issues is zero.
func completeness(m *domain.Milestone) float64 {
	total := m.OpenIssues + m.ClosedIssues
	if total == 0 {
		return 0
	}
	return float64(m.ClosedIssues) / float64(total)
}

// handleMilestoneCreate serves POST /repos/{owner}/{repo}/milestones.
func handleMilestoneCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		var body milestoneBody
		if !decodeJSON(c, &body) {
			return nil
		}
		if body.Title == nil || *body.Title == "" {
			writeError(c.Writer(), errValidation(FieldError{Resource: "Milestone", Field: "title", Code: "missing_field"}))
			return nil
		}
		in, ok := milestoneInput(c, body)
		if !ok {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		m, err := d.Issues.CreateMilestone(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), in)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Milestone(c.Param("owner"), c.Param("repo"), m, d.NodeFormat))
		return nil
	}
}

// handleMilestoneGet serves GET /repos/{owner}/{repo}/milestones/{number}.
func handleMilestoneGet(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		m, err := d.Issues.GetMilestone(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		conditionalJSON(c.Writer(), c.Request(), http.StatusOK, d.URLs.Milestone(c.Param("owner"), c.Param("repo"), m, d.NodeFormat))
		return nil
	}
}

// handleMilestoneEdit serves PATCH /repos/{owner}/{repo}/milestones/{number}.
func handleMilestoneEdit(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		var body milestoneBody
		if !decodeJSON(c, &body) {
			return nil
		}
		in, ok := milestoneInput(c, body)
		if !ok {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		m, err := d.Issues.UpdateMilestone(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, in)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.URLs.Milestone(c.Param("owner"), c.Param("repo"), m, d.NodeFormat))
		return nil
	}
}

// handleMilestoneDelete serves DELETE /repos/{owner}/{repo}/milestones/{number}.
func handleMilestoneDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		err := d.Issues.DeleteMilestone(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleIssueReactionsList serves GET /repos/{owner}/{repo}/issues/{number}/reactions.
func handleIssueReactionsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		rs, err := d.Issues.ListIssueReactions(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.reactions(rs))
		return nil
	}
}

// handleIssueReactionCreate serves POST /repos/{owner}/{repo}/issues/{number}/reactions.
func handleIssueReactionCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		content, ok := reactionContent(c)
		if !ok {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		r, err := d.Issues.CreateIssueReaction(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, content)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Reaction(r, d.NodeFormat))
		return nil
	}
}

// handleIssueReactionDelete serves
// DELETE /repos/{owner}/{repo}/issues/{number}/reactions/{id}.
func handleIssueReactionDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		number, ok := pathInt64(c, "number")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		err := d.Issues.DeleteIssueReaction(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), number, id)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// handleCommentReactionsList serves
// GET /repos/{owner}/{repo}/issues/comments/{id}/reactions.
func handleCommentReactionsList(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		rs, err := d.Issues.ListCommentReactions(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), id)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusOK, d.reactions(rs))
		return nil
	}
}

// handleCommentReactionCreate serves
// POST /repos/{owner}/{repo}/issues/comments/{id}/reactions.
func handleCommentReactionCreate(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		content, ok := reactionContent(c)
		if !ok {
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		r, err := d.Issues.CreateCommentReaction(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), id, content)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		writeJSON(c.Writer(), http.StatusCreated, d.URLs.Reaction(r, d.NodeFormat))
		return nil
	}
}

// handleCommentReactionDelete serves
// DELETE /repos/{owner}/{repo}/issues/comments/{id}/reactions/{reaction_id}.
func handleCommentReactionDelete(d Deps) mizu.Handler {
	return func(c *mizu.Ctx) error {
		id, ok := pathInt64(c, "id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		reactionID, ok := pathInt64(c, "reaction_id")
		if !ok {
			writeError(c.Writer(), errNotFound())
			return nil
		}
		actor := auth.ActorFrom(c.Request().Context())
		err := d.Issues.DeleteCommentReaction(c.Request().Context(), actor.UserID, c.Param("owner"), c.Param("repo"), id, reactionID)
		if issueError(c.Writer(), err) {
			return nil
		}
		if err != nil {
			return err
		}
		c.Writer().WriteHeader(http.StatusNoContent)
		return nil
	}
}

// reactions renders a reaction list.
func (d Deps) reactions(rs []*domain.Reaction) []restmodel.Reaction {
	out := make([]restmodel.Reaction, 0, len(rs))
	for _, r := range rs {
		out = append(out, d.URLs.Reaction(r, d.NodeFormat))
	}
	return out
}

// decodeIssueEdit decodes the issue patch, tracking whether milestone was
// present in the body so a present-but-null value clears the milestone while an
// absent one leaves it unchanged.
func decodeIssueEdit(c *mizu.Ctx) (issueEditBody, bool) {
	var raw map[string]any
	if !decodeJSON(c, &raw) {
		return issueEditBody{}, false
	}
	var body issueEditBody
	if v, ok := raw["title"]; ok {
		if s, ok := v.(string); ok {
			body.Title = &s
		}
	}
	if v, ok := raw["body"]; ok {
		if s, ok := v.(string); ok {
			body.Body = &s
		}
	}
	if v, ok := raw["state"]; ok {
		if s, ok := v.(string); ok {
			body.State = &s
		}
	}
	if v, ok := raw["state_reason"]; ok {
		if s, ok := v.(string); ok {
			body.StateReason = &s
		}
	}
	if v, ok := raw["labels"]; ok {
		body.Labels = toLabelNames(v)
	}
	if v, ok := raw["assignees"]; ok {
		body.Assignees = toStrings(v)
	}
	if v, ok := raw["assignee"]; ok && body.Assignees == nil {
		// The legacy singular form: a login assigns that one user, null or the
		// empty string clears the assignees.
		switch t := v.(type) {
		case string:
			one := []string{}
			if t != "" {
				one = append(one, t)
			}
			body.Assignees = &one
		case nil:
			empty := []string{}
			body.Assignees = &empty
		}
	}
	if v, ok := raw["milestone"]; ok {
		body.milestoneSet = true
		if f, ok := v.(float64); ok {
			n := int64(f)
			body.Milestone = &n
		}
	}
	return body, true
}

// milestoneInput maps the milestone request to the domain input, parsing the
// optional due_on timestamp. A malformed due_on is a 422.
func milestoneInput(c *mizu.Ctx, body milestoneBody) (domain.MilestoneInput, bool) {
	in := domain.MilestoneInput{Title: body.Title, State: body.State, Description: body.Description}
	if body.DueOn != nil {
		if *body.DueOn == "" {
			in.ClearDueOn = true
		} else {
			t, err := time.Parse(time.RFC3339, *body.DueOn)
			if err != nil {
				writeError(c.Writer(), errValidation(FieldError{Resource: "Milestone", Field: "due_on", Code: "invalid"}))
				return in, false
			}
			t = t.UTC()
			in.DueOn = &t
		}
	}
	return in, true
}

// reactionContent reads and validates the reaction content from the request,
// writing the 422 and returning false when it is missing or unknown.
func reactionContent(c *mizu.Ctx) (string, bool) {
	var body reactionBody
	if !decodeJSON(c, &body) {
		return "", false
	}
	if body.Content == "" {
		writeError(c.Writer(), errValidation(FieldError{Resource: "Reaction", Field: "content", Code: "missing_field"}))
		return "", false
	}
	if !validReaction(body.Content) {
		writeError(c.Writer(), errValidation(FieldError{Resource: "Reaction", Field: "content", Code: "invalid"}))
		return "", false
	}
	return body.Content, true
}

// validReaction reports whether content is one of GitHub's eight reaction names.
func validReaction(content string) bool {
	switch content {
	case "+1", "-1", "laugh", "confused", "heart", "hooray", "rocket", "eyes":
		return true
	default:
		return false
	}
}

// toLabelNames coerces a decoded JSON labels array whose members are plain
// strings or {"name": ...} objects, the two shapes GitHub accepts mixed.
func toLabelNames(v any) *[]string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		switch t := e.(type) {
		case string:
			out = append(out, t)
		case map[string]any:
			if s, ok := t["name"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return &out
}

// toStrings coerces a decoded JSON array of strings, dropping non-string members.
func toStrings(v any) *[]string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return &out
}

// issueError maps an issue-subsystem domain error to its API response, returning
// true when it wrote one. A nil error or an unrecognized error returns false so
// the caller falls through to its own success path or the central 500 handler.
func issueError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, domain.ErrRepoNotFound),
		errors.Is(err, domain.ErrIssueNotFound),
		errors.Is(err, domain.ErrCommentNotFound),
		errors.Is(err, domain.ErrLabelNotFound),
		errors.Is(err, domain.ErrMilestoneNotFound):
		writeError(w, errNotFound())
	case errors.Is(err, domain.ErrForbidden):
		writeError(w, errForbidden("Write access to the repository is required."))
	case errors.Is(err, domain.ErrLabelExists):
		writeError(w, errValidation(FieldError{Resource: "Label", Field: "name", Code: "already_exists"}))
	case errors.Is(err, domain.ErrValidation):
		writeError(w, errValidation())
	default:
		return false
	}
	return true
}

// splitCSV splits a comma-separated query value, trimming spaces and dropping
// empties. An empty input yields nil.
func splitCSV(v string) []string {
	if v == "" {
		return nil
	}
	var out []string
	for _, part := range splitComma(v) {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitComma(v string) []string {
	var out []string
	start := 0
	for i := 0; i < len(v); i++ {
		if v[i] == ',' {
			out = append(out, trimSpace(v[start:i]))
			start = i + 1
		}
	}
	out = append(out, trimSpace(v[start:]))
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

// pathInt64 parses a numeric path parameter, reporting false when it is absent
// or not a non-negative integer.
func pathInt64(c *mizu.Ctx, name string) (int64, bool) {
	return parseInt64(c.Param(name))
}

// parseInt64 parses a non-negative integer, reporting false on a malformed or
// negative value.
func parseInt64(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// queryInt64 parses a numeric query parameter, reporting false when absent or
// malformed.
func queryInt64(c *mizu.Ctx, name string) (int64, bool) {
	v := c.Query(name)
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// pageNum reads the 1-based page query, defaulting to 1.
func pageNum(c *mizu.Ctx) int {
	n, err := strconv.Atoi(c.Query("page"))
	if err != nil || n <= 0 {
		return 1
	}
	return n
}
