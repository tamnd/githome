package domain

import (
	"context"
	"errors"
	"strings"

	"github.com/tamnd/githome/store"
)

// TeamStore is the narrow store interface TeamService depends on.
type TeamStore interface {
	UserByLogin(ctx context.Context, login string) (*store.UserRow, error)
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	TeamBySlug(ctx context.Context, orgPK int64, slug string) (*store.TeamRow, error)
	TeamByPK(ctx context.Context, pk int64) (*store.TeamRow, error)
	InsertTeam(ctx context.Context, t *store.TeamRow) error
	UpdateTeam(ctx context.Context, pk int64, name, description, privacy, permission *string) (*store.TeamRow, error)
	DeleteTeam(ctx context.Context, pk int64) error
	UpsertTeamMember(ctx context.Context, teamPK, userPK int64, role string) error
	TeamMemberRole(ctx context.Context, teamPK, userPK int64) (string, error)
	DeleteTeamMember(ctx context.Context, teamPK, userPK int64) error
	UpsertTeamRepo(ctx context.Context, teamPK, repoPK int64, permission string) error
	TeamRepoPermission(ctx context.Context, teamPK, repoPK int64) (string, error)
	DeleteTeamRepo(ctx context.Context, teamPK, repoPK int64) error
	RepoByOwnerName(ctx context.Context, owner, name string) (*store.RepoRow, error)
	UpdateRepoTopics(ctx context.Context, repoPK int64, topicsJSON string) error
	CollaboratorByRepo(ctx context.Context, repoPK, userPK int64) (*store.CollaboratorRow, error)
	CollaboratorsByRepo(ctx context.Context, repoPK int64) ([]*store.CollaboratorRow, error)
	UpsertCollaborator(ctx context.Context, repoPK, userPK int64, permission string) error
	DeleteCollaborator(ctx context.Context, repoPK, userPK int64) error
	TeamsByOrg(ctx context.Context, orgPK int64) ([]*store.TeamRow, error)
	CountTeamMembers(ctx context.Context, teamPK int64) (int, error)
	CountTeamRepos(ctx context.Context, teamPK int64) (int, error)
	UpsertOrgMember(ctx context.Context, orgPK, userPK int64, role string) error
	OrgMemberRole(ctx context.Context, orgPK, userPK int64) (string, error)
	DeleteOrgMember(ctx context.Context, orgPK, userPK int64) error
	OrgMembersByOrg(ctx context.Context, orgPK int64) ([]*store.OrgMemberRow, error)
	OrgMembersByUser(ctx context.Context, userPK int64) ([]*store.OrgMemberRow, error)
}

// TeamService manages teams, collaborators, and repository topics.
type TeamService struct {
	store TeamStore
}

// NewTeamService creates a TeamService over st.
func NewTeamService(st TeamStore) *TeamService { return &TeamService{store: st} }

// slugify converts a team name to a URL-safe slug.
func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			b.WriteRune(c)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

// CreateTeam creates a new team in an org.
func (s *TeamService) CreateTeam(ctx context.Context, orgPK int64, name, description, privacy, permission string) (*store.TeamRow, error) {
	if strings.TrimSpace(name) == "" {
		return nil, ErrNotFound
	}
	if privacy == "" {
		privacy = "secret"
	}
	if permission == "" {
		permission = "pull"
	}
	t := &store.TeamRow{
		OrgPK:      orgPK,
		Name:       name,
		Slug:       slugify(name),
		Privacy:    privacy,
		Permission: permission,
	}
	if d := strings.TrimSpace(description); d != "" {
		t.Description = &d
	}
	if err := s.store.InsertTeam(ctx, t); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "unique") {
			return nil, ErrDuplicateKey
		}
		return nil, err
	}
	return t, nil
}

// GetTeamBySlug returns a team by org pk and slug.
func (s *TeamService) GetTeamBySlug(ctx context.Context, orgPK int64, slug string) (*store.TeamRow, error) {
	t, err := s.store.TeamBySlug(ctx, orgPK, slug)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	return t, err
}

// UpdateTeam applies partial updates to a team.
func (s *TeamService) UpdateTeam(ctx context.Context, pk int64, name, description, privacy, permission *string) (*store.TeamRow, error) {
	t, err := s.store.UpdateTeam(ctx, pk, name, description, privacy, permission)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNotFound
	}
	return t, err
}

// DeleteTeam removes a team.
func (s *TeamService) DeleteTeam(ctx context.Context, pk int64) error {
	err := s.store.DeleteTeam(ctx, pk)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// AddTeamMember adds or updates a user's role in a team.
func (s *TeamService) AddTeamMember(ctx context.Context, teamPK, userPK int64, role string) error {
	if role != "maintainer" {
		role = "member"
	}
	return s.store.UpsertTeamMember(ctx, teamPK, userPK, role)
}

// GetTeamMembership returns the role of userPK in teamPK, or ErrNotFound.
func (s *TeamService) GetTeamMembership(ctx context.Context, teamPK, userPK int64) (string, error) {
	role, err := s.store.TeamMemberRole(ctx, teamPK, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotFound
	}
	return role, err
}

// RemoveTeamMember removes a user from a team.
func (s *TeamService) RemoveTeamMember(ctx context.Context, teamPK, userPK int64) error {
	err := s.store.DeleteTeamMember(ctx, teamPK, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// AddTeamRepo grants a team access to a repo.
func (s *TeamService) AddTeamRepo(ctx context.Context, teamPK, repoPK int64, permission string) error {
	if permission == "" {
		permission = "pull"
	}
	return s.store.UpsertTeamRepo(ctx, teamPK, repoPK, permission)
}

// GetTeamRepoPermission returns the permission level for a repo in a team.
func (s *TeamService) GetTeamRepoPermission(ctx context.Context, teamPK, repoPK int64) (string, error) {
	perm, err := s.store.TeamRepoPermission(ctx, teamPK, repoPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotFound
	}
	return perm, err
}

// RemoveTeamRepo removes a repo from a team.
func (s *TeamService) RemoveTeamRepo(ctx context.Context, teamPK, repoPK int64) error {
	err := s.store.DeleteTeamRepo(ctx, teamPK, repoPK)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// SetTopics replaces the topics for a repo.
func (s *TeamService) SetTopics(ctx context.Context, repoPK int64, topics []string) error {
	if topics == nil {
		topics = []string{}
	}
	// Build JSON array manually to avoid import cycle.
	var sb strings.Builder
	sb.WriteByte('[')
	for i, t := range topics {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte('"')
		sb.WriteString(strings.ReplaceAll(t, `"`, `\"`))
		sb.WriteByte('"')
	}
	sb.WriteByte(']')
	return s.store.UpdateRepoTopics(ctx, repoPK, sb.String())
}

// GetCollaboratorPermission returns the permission of userPK on repoPK.
func (s *TeamService) GetCollaboratorPermission(ctx context.Context, repoPK, userPK int64) (string, error) {
	c, err := s.store.CollaboratorByRepo(ctx, repoPK, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return c.Permission, nil
}

// RepoCollaborator pairs a collaborator with the permission they hold.
type RepoCollaborator struct {
	User       *User
	Permission string
}

// ListCollaborators returns a repo's collaborator grants with their users
// resolved, oldest grant first. A grant whose user has vanished is skipped.
func (s *TeamService) ListCollaborators(ctx context.Context, repoPK int64) ([]RepoCollaborator, error) {
	rows, err := s.store.CollaboratorsByRepo(ctx, repoPK)
	if err != nil {
		return nil, err
	}
	out := make([]RepoCollaborator, 0, len(rows))
	for _, r := range rows {
		u, err := s.store.UserByPK(ctx, r.UserPK)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, RepoCollaborator{User: userFromRow(u), Permission: r.Permission})
	}
	return out, nil
}

// AddCollaborator sets (or updates) a collaborator's permission. created
// reports whether the grant is new, the bit that decides between GitHub's 201
// invitation response and the 204 for an existing collaborator; id is the
// grant's row id, which doubles as the invitation id.
func (s *TeamService) AddCollaborator(ctx context.Context, repoPK, userPK int64, permission string) (id int64, created bool, err error) {
	if permission == "" {
		permission = "push"
	}
	_, err = s.store.CollaboratorByRepo(ctx, repoPK, userPK)
	created = errors.Is(err, store.ErrNotFound)
	if err != nil && !created {
		return 0, false, err
	}
	if err := s.store.UpsertCollaborator(ctx, repoPK, userPK, permission); err != nil {
		return 0, false, err
	}
	row, err := s.store.CollaboratorByRepo(ctx, repoPK, userPK)
	if err != nil {
		return 0, false, err
	}
	return row.PK, created, nil
}

// RemoveCollaborator removes a collaborator from a repo.
func (s *TeamService) RemoveCollaborator(ctx context.Context, repoPK, userPK int64) error {
	err := s.store.DeleteCollaborator(ctx, repoPK, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// ListTeams returns an org's teams, oldest first.
func (s *TeamService) ListTeams(ctx context.Context, orgPK int64) ([]*store.TeamRow, error) {
	return s.store.TeamsByOrg(ctx, orgPK)
}

// TeamCounts returns how many members and repos a team has.
func (s *TeamService) TeamCounts(ctx context.Context, teamPK int64) (members, repos int, err error) {
	if members, err = s.store.CountTeamMembers(ctx, teamPK); err != nil {
		return 0, 0, err
	}
	if repos, err = s.store.CountTeamRepos(ctx, teamPK); err != nil {
		return 0, 0, err
	}
	return members, repos, nil
}

// AddOrgMember adds or updates a user's org membership. Any role other than
// admin normalizes to member, GitHub's two-role vocabulary.
func (s *TeamService) AddOrgMember(ctx context.Context, orgPK, userPK int64, role string) (string, error) {
	if role != "admin" {
		role = "member"
	}
	return role, s.store.UpsertOrgMember(ctx, orgPK, userPK, role)
}

// GetOrgMembership returns the role of userPK in orgPK, or ErrNotFound.
func (s *TeamService) GetOrgMembership(ctx context.Context, orgPK, userPK int64) (string, error) {
	role, err := s.store.OrgMemberRole(ctx, orgPK, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrNotFound
	}
	return role, err
}

// RemoveOrgMember removes a user from an org.
func (s *TeamService) RemoveOrgMember(ctx context.Context, orgPK, userPK int64) error {
	err := s.store.DeleteOrgMember(ctx, orgPK, userPK)
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound
	}
	return err
}

// OrgMember pairs an org member with the role they hold.
type OrgMember struct {
	User *User
	Role string
}

// ListUserOrgs returns the organizations userPK belongs to, oldest membership
// first, each paired with the role held. The User of each entry is the org
// account (orgs share the users table). An org whose account has vanished is
// skipped.
func (s *TeamService) ListUserOrgs(ctx context.Context, userPK int64) ([]OrgMember, error) {
	rows, err := s.store.OrgMembersByUser(ctx, userPK)
	if err != nil {
		return nil, err
	}
	out := make([]OrgMember, 0, len(rows))
	for _, r := range rows {
		org, err := s.store.UserByPK(ctx, r.OrgPK)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, OrgMember{User: userFromRow(org), Role: r.Role})
	}
	return out, nil
}

// ListOrgMembers returns an org's members with their users resolved, oldest
// first. A membership whose user has vanished is skipped.
func (s *TeamService) ListOrgMembers(ctx context.Context, orgPK int64) ([]OrgMember, error) {
	rows, err := s.store.OrgMembersByOrg(ctx, orgPK)
	if err != nil {
		return nil, err
	}
	out := make([]OrgMember, 0, len(rows))
	for _, r := range rows {
		u, err := s.store.UserByPK(ctx, r.UserPK)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, OrgMember{User: userFromRow(u), Role: r.Role})
	}
	return out, nil
}
