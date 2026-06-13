package presenter

import (
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/presenter/restmodel"
)

// CommunityProfile renders a repository's community-health profile, expanding
// each present file into its contents-API url and blob html_url at the default
// branch.
func (b *URLBuilder) CommunityProfile(owner, repoName, ref string, p domain.CommunityProfile, description *string) restmodel.CommunityProfile {
	link := func(f *domain.CommunityFile) *restmodel.CommunityFileLink {
		if f == nil {
			return nil
		}
		return &restmodel.CommunityFileLink{
			URL:     b.RepoAPI(owner, repoName) + "/contents/" + f.Path + "?ref=" + ref,
			HTMLURL: b.RepoHTML(owner, repoName) + "/blob/" + ref + "/" + f.Path,
		}
	}
	coc := link(p.CodeOfConduct)
	return restmodel.CommunityProfile{
		HealthPercentage: p.HealthPercentage,
		Description:      description,
		Files: restmodel.CommunityFiles{
			CodeOfConduct:       coc,
			CodeOfConductFile:   coc,
			Contributing:        link(p.Contributing),
			IssueTemplate:       link(p.IssueTemplate),
			PullRequestTemplate: link(p.PullRequestTemplate),
			License:             link(p.License),
			Readme:              link(p.Readme),
		},
	}
}

// CodeownersErrors renders the validation errors found in a repository's
// CODEOWNERS file.
func (b *URLBuilder) CodeownersErrors(errs []domain.CodeownerError) restmodel.CodeownersErrors {
	out := restmodel.CodeownersErrors{Errors: make([]restmodel.CodeownerError, 0, len(errs))}
	for _, e := range errs {
		out.Errors = append(out.Errors, restmodel.CodeownerError{
			Line:       e.Line,
			Column:     e.Column,
			Kind:       e.Kind,
			Source:     e.Source,
			Suggestion: e.Suggestion,
			Message:    e.Message,
			Path:       e.Path,
		})
	}
	return out
}
