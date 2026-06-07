package realworld

import "encoding/json"

// renderEventPayload turns a timeline event's typed columns and data blob into
// the JSON payload the issue_events row stores and the events API and webhook
// pipeline read. It mirrors the shape GitHub puts on a timeline item: a labeled
// event carries its label, an assigned event its assignee, a milestoned event
// its milestone, a renamed event the from/to titles, a cross-referenced event
// the source. Anything not covered by a typed column falls back to the raw data
// blob so no information is dropped. The result is DERIVED provenance: it is
// computed from official columns by this documented rule.
func renderEventPayload(ev TimelineEvent) string {
	m := map[string]any{}
	switch ev.EventType {
	case "labeled", "unlabeled":
		if ev.LabelName != "" {
			m["label"] = map[string]string{"name": ev.LabelName, "color": ev.LabelColor}
		}
	case "assigned", "unassigned":
		if ev.Assignee != "" {
			m["assignee"] = map[string]string{"login": ev.Assignee}
		}
	case "milestoned", "demilestoned":
		if ev.Milestone != "" {
			m["milestone"] = map[string]string{"title": ev.Milestone}
		}
	case "renamed":
		if ev.TitleFrom != "" || ev.TitleTo != "" {
			m["rename"] = map[string]string{"from": ev.TitleFrom, "to": ev.TitleTo}
		}
	case "cross-referenced", "referenced":
		if ev.RefType != "" || ev.RefNumber != 0 {
			m["source"] = map[string]any{"type": ev.RefType, "number": ev.RefNumber}
		}
	case "locked":
		if ev.LockReason != "" {
			m["lock_reason"] = ev.LockReason
		}
	}
	// Fold any remaining data blob fields in without overwriting a typed value.
	for k, v := range ev.Data {
		if _, ok := m[k]; !ok {
			m[k] = v
		}
	}
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
