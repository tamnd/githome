package presenter

import (
	"strconv"

	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter/restmodel"
)

// i64 renders an int64 id for a url segment.
func i64(n int64) string { return strconv.FormatInt(n, 10) }

// Status renders one commit status for owner/repo. The node id encodes the
// status's own id under the StatusContext kind; the url addresses the statuses
// collection at the sha the status anchors to.
func (b *URLBuilder) Status(owner, repo string, s *domain.CommitStatus, format nodeid.Format) restmodel.Status {
	out := restmodel.Status{
		URL:         b.RepoAPI(owner, repo) + "/statuses/" + s.SHA,
		ID:          s.ID,
		NodeID:      nodeid.Encode(nodeid.KindStatusContext, s.ID, format),
		State:       s.State,
		Description: s.Description,
		TargetURL:   s.TargetURL,
		Context:     s.Context,
		CreatedAt:   restmodel.NewTime(s.CreatedAt),
		UpdatedAt:   restmodel.NewTime(s.UpdatedAt),
	}
	if s.Creator != nil {
		u := b.SimpleUser(s.Creator, format)
		out.Creator = &u
		out.AvatarURL = &u.AvatarURL
	}
	return out
}

// CombinedStatus renders the combined status for owner/repo at a sha, folding the
// contributing statuses and a minimal repository.
func (b *URLBuilder) CombinedStatus(owner, repo string, cs *domain.CombinedStatus, format nodeid.Format) restmodel.CombinedStatus {
	statuses := make([]restmodel.Status, 0, len(cs.Statuses))
	for _, s := range cs.Statuses {
		statuses = append(statuses, b.Status(owner, repo, s, format))
	}
	return restmodel.CombinedStatus{
		State:      cs.State,
		Statuses:   statuses,
		SHA:        cs.SHA,
		TotalCount: cs.TotalCount,
		Repository: b.minimalRepo(cs.Repo, format),
		CommitURL:  b.RepoAPI(owner, repo) + "/commits/" + cs.SHA,
		URL:        b.RepoAPI(owner, repo) + "/commits/" + cs.SHA + "/status",
	}
}

// minimalRepo renders the trimmed repository the combined status embeds.
func (b *URLBuilder) minimalRepo(r *domain.Repo, format nodeid.Format) restmodel.MinimalRepo {
	out := restmodel.MinimalRepo{
		ID:       r.ID,
		NodeID:   nodeid.Encode(nodeid.KindRepository, r.ID, format),
		Name:     r.Name,
		FullName: r.FullName(),
		Private:  r.Private,
		HTMLURL:  b.RepoHTML(r.Owner.Login, r.Name),
		URL:      b.RepoAPI(r.Owner.Login, r.Name),
	}
	if r.Owner != nil {
		out.Owner = b.SimpleUser(r.Owner, format)
	}
	return out
}

// CheckRun renders one check run for owner/repo. The node id encodes the run's own
// id under the CheckRun kind; the urls address the run and its details.
func (b *URLBuilder) CheckRun(owner, repo string, r *domain.CheckRun, format nodeid.Format) restmodel.CheckRun {
	external := ""
	if r.ExternalID != nil {
		external = *r.ExternalID
	}
	details := ""
	if r.DetailsURL != nil {
		details = *r.DetailsURL
	}
	self := b.RepoAPI(owner, repo) + "/check-runs/" + i64(r.ID)
	actions := make([]restmodel.CheckRunAction, 0, len(r.Actions))
	for _, a := range r.Actions {
		actions = append(actions, restmodel.CheckRunAction{
			Label: a.Label, Description: a.Description, Identifier: a.Identifier,
		})
	}
	return restmodel.CheckRun{
		ID:          r.ID,
		NodeID:      nodeid.Encode(nodeid.KindCheckRun, r.ID, format),
		HeadSHA:     r.HeadSHA,
		ExternalID:  external,
		URL:         self,
		HTMLURL:     b.RepoHTML(owner, repo) + "/runs/" + i64(r.ID),
		DetailsURL:  details,
		Status:      r.Status,
		Conclusion:  r.Conclusion,
		StartedAt:   timePtr(r.StartedAt),
		CompletedAt: timePtr(r.CompletedAt),
		Output: restmodel.CheckRunOutput{
			Title:            r.OutputTitle,
			Summary:          r.OutputSummary,
			Text:             r.OutputText,
			AnnotationsCount: r.AnnotationsCount,
			AnnotationsURL:   self + "/annotations",
		},
		Name:         r.Name,
		CheckSuite:   restmodel.CheckSuiteRef{ID: r.SuitePK},
		PullRequests: []any{},
		Actions:      actions,
	}
}

// CheckRunAnnotation renders one check run annotation for owner/repo. The blob
// href addresses the annotated file at the run's head sha.
func (b *URLBuilder) CheckRunAnnotation(owner, repo, headSHA string, a *domain.CheckRunAnnotation) restmodel.CheckRunAnnotation {
	return restmodel.CheckRunAnnotation{
		Path:            a.Path,
		StartLine:       a.StartLine,
		EndLine:         a.EndLine,
		StartColumn:     a.StartColumn,
		EndColumn:       a.EndColumn,
		AnnotationLevel: a.AnnotationLevel,
		Title:           a.Title,
		Message:         a.Message,
		RawDetails:      a.RawDetails,
		BlobHRef:        b.RepoHTML(owner, repo) + "/blob/" + headSHA + "/" + a.Path,
	}
}

// CheckRunList renders the check-runs collection for owner/repo at a ref.
func (b *URLBuilder) CheckRunList(owner, repo string, runs []*domain.CheckRun, format nodeid.Format) restmodel.CheckRunList {
	out := restmodel.CheckRunList{TotalCount: len(runs), CheckRuns: make([]restmodel.CheckRun, 0, len(runs))}
	for _, r := range runs {
		out.CheckRuns = append(out.CheckRuns, b.CheckRun(owner, repo, r, format))
	}
	return out
}

// CheckSuite renders one check suite for owner/repo.
func (b *URLBuilder) CheckSuite(owner, repo string, s *domain.CheckSuite, format nodeid.Format) restmodel.CheckSuite {
	return restmodel.CheckSuite{
		ID:                   s.ID,
		NodeID:               nodeid.Encode(nodeid.KindCheckSuite, s.ID, format),
		HeadSHA:              s.HeadSHA,
		Status:               s.Status,
		Conclusion:           s.Conclusion,
		URL:                  b.RepoAPI(owner, repo) + "/check-suites/" + i64(s.ID),
		CreatedAt:            restmodel.NewTime(s.CreatedAt),
		UpdatedAt:            restmodel.NewTime(s.UpdatedAt),
		LatestCheckRunsCount: len(s.Runs),
	}
}

// CheckSuiteList renders the check-suites collection for owner/repo at a ref.
func (b *URLBuilder) CheckSuiteList(owner, repo string, suites []*domain.CheckSuite, format nodeid.Format) restmodel.CheckSuiteList {
	out := restmodel.CheckSuiteList{TotalCount: len(suites), CheckSuites: make([]restmodel.CheckSuite, 0, len(suites))}
	for _, s := range suites {
		out.CheckSuites = append(out.CheckSuites, b.CheckSuite(owner, repo, s, format))
	}
	return out
}
