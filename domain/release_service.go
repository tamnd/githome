package domain

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/tamnd/githome/git"
	"github.com/tamnd/githome/store"
	"github.com/tamnd/githome/worker"
)

// ErrReleaseNotFound is returned when a release cannot be found.
var ErrReleaseNotFound = errors.New("domain: release not found")

// ErrReleaseAssetNotFound is returned when a release asset cannot be found.
var ErrReleaseAssetNotFound = errors.New("domain: release asset not found")

// ErrReleaseTagTaken is returned when a release tag already exists.
var ErrReleaseTagTaken = errors.New("domain: release tag already exists")

// ReleaseStore is the store slice the release service needs.
type ReleaseStore interface {
	UserByPK(ctx context.Context, pk int64) (*store.UserRow, error)
	UsersByPKs(ctx context.Context, pks []int64) (map[int64]*store.UserRow, error)

	EnqueueJob(ctx context.Context, j *store.JobRow) (bool, error)
	InsertEvent(ctx context.Context, e *store.EventRow) error

	GetReleaseByID(ctx context.Context, repoPK, dbID int64) (*store.ReleaseRow, error)
	GetReleaseByPK(ctx context.Context, pk int64) (*store.ReleaseRow, error)
	GetReleaseByTag(ctx context.Context, repoPK int64, tag string) (*store.ReleaseRow, error)
	GetLatestRelease(ctx context.Context, repoPK int64) (*store.ReleaseRow, error)
	ListReleases(ctx context.Context, repoPK int64, includeDrafts bool, limit, offset int) ([]store.ReleaseRow, error)
	InsertRelease(ctx context.Context, r *store.ReleaseRow) error
	UpdateRelease(ctx context.Context, r *store.ReleaseRow) error
	DeleteRelease(ctx context.Context, pk int64) error

	ListReleaseAssets(ctx context.Context, releasePK int64) ([]store.ReleaseAssetRow, error)
	GetReleaseAssetByID(ctx context.Context, dbID int64) (*store.ReleaseAssetRow, error)
	InsertReleaseAsset(ctx context.Context, a *store.ReleaseAssetRow) error
	UpdateReleaseAsset(ctx context.Context, a *store.ReleaseAssetRow) error
	IncrementAssetDownloadCount(ctx context.Context, pk int64) error
	DeleteReleaseAsset(ctx context.Context, pk int64) error
}

// ReleaseService manages releases and their assets.
type ReleaseService struct {
	store    ReleaseStore
	repos    *RepoService
	assetDir string // DataDir/assets; binary files live here as {pk}
	enq      worker.Enqueuer
}

// NewReleaseService builds a ReleaseService. assetDir is the directory where
// uploaded binary files are stored (one file per asset pk).
func NewReleaseService(st ReleaseStore, repos *RepoService, assetDir string) *ReleaseService {
	return &ReleaseService{store: st, repos: repos, assetDir: assetDir, enq: worker.NewStoreEnqueuer(st)}
}

// ReleaseInput is the create/update payload for a release.
type ReleaseInput struct {
	TagName         string
	TargetCommitish string
	Name            *string
	Body            *string
	Draft           bool
	Prerelease      bool
	MakeLatest      string // "true"|"false"|"legacy"; handled outside the DB
}

// ListReleases returns a page of releases for a repository.
func (s *ReleaseService) ListReleases(ctx context.Context, actorPK int64, owner, name string, page, perPage int) ([]*Release, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if perPage <= 0 {
		perPage = 30
	}
	offset := (page - 1) * perPage
	rows, err := s.store.ListReleases(ctx, repo.PK, true, perPage, offset)
	if err != nil {
		return nil, err
	}
	return s.hydrateReleases(ctx, rows)
}

// GetRelease returns a single release by its GitHub id within the repository.
func (s *ReleaseService) GetRelease(ctx context.Context, actorPK int64, owner, name string, id int64) (*Release, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReleaseByID(ctx, repo.PK, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateRelease(ctx, row)
}

// GetReleaseByTag returns a release by its tag name.
func (s *ReleaseService) GetReleaseByTag(ctx context.Context, actorPK int64, owner, name, tag string) (*Release, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReleaseByTag(ctx, repo.PK, tag)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateRelease(ctx, row)
}

// GetLatestRelease returns the latest non-draft, non-prerelease release.
func (s *ReleaseService) GetLatestRelease(ctx context.Context, actorPK int64, owner, name string) (*Release, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetLatestRelease(ctx, repo.PK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateRelease(ctx, row)
}

// CreateRelease creates a new release. The actor must have write access to the
// repository.
func (s *ReleaseService) CreateRelease(ctx context.Context, actorPK int64, owner, name string, in ReleaseInput) (*Release, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if in.TagName == "" {
		return nil, ErrValidation
	}
	if in.TargetCommitish == "" {
		in.TargetCommitish = repo.DefaultBranch
	}
	now := time.Now().UTC()
	var publishedAt *time.Time
	if !in.Draft {
		publishedAt = &now
	}
	row := &store.ReleaseRow{
		RepoPK:          repo.PK,
		TagName:         in.TagName,
		TargetCommitish: in.TargetCommitish,
		Name:            in.Name,
		Body:            in.Body,
		Draft:           in.Draft,
		Prerelease:      in.Prerelease,
		AuthorPK:        &actorPK,
		PublishedAt:     publishedAt,
	}
	if err := s.store.InsertRelease(ctx, row); err != nil {
		return nil, err
	}
	if !in.Draft {
		s.recordPublished(ctx, actorPK, repo, row.PK)
	}
	return s.hydrateRelease(ctx, row)
}

// UpdateRelease edits an existing release.
func (s *ReleaseService) UpdateRelease(ctx context.Context, actorPK int64, owner, name string, id int64, in ReleaseInput) (*Release, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReleaseByID(ctx, repo.PK, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	if in.TagName != "" {
		row.TagName = in.TagName
	}
	if in.TargetCommitish != "" {
		row.TargetCommitish = in.TargetCommitish
	}
	if in.Name != nil {
		row.Name = in.Name
	}
	if in.Body != nil {
		row.Body = in.Body
	}
	wasDraft := row.Draft
	row.Draft = in.Draft
	row.Prerelease = in.Prerelease
	if wasDraft && !in.Draft && row.PublishedAt == nil {
		now := time.Now().UTC()
		row.PublishedAt = &now
	}
	if err := s.store.UpdateRelease(ctx, row); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrReleaseNotFound
		}
		return nil, err
	}
	if wasDraft && !in.Draft {
		s.recordPublished(ctx, actorPK, repo, row.PK)
	}
	return s.hydrateRelease(ctx, row)
}

// recordPublished records the release event a publish emits: creating a
// release live and flipping a draft live both deliver action published, the
// one release action Githome's release lifecycle produces. The release pk
// rides the event detail so the delivery body can embed the release object.
func (s *ReleaseService) recordPublished(ctx context.Context, actorPK int64, repo *Repo, releasePK int64) {
	recordEventFull(ctx, s.store, s.enq, &store.EventRow{
		Event:   EventRelease,
		Action:  "published",
		ActorPK: actorPK,
		RepoPK:  repo.PK,
		Public:  !repo.Private,
	}, nil, nil, &EventDetail{ReleasePK: releasePK})
}

// ReleaseForEvent loads a release by pk for the webhook renderer, with no
// visibility gate: the event was authorized when it was recorded.
func (s *ReleaseService) ReleaseForEvent(ctx context.Context, releasePK int64) (*Release, error) {
	row, err := s.store.GetReleaseByPK(ctx, releasePK)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateRelease(ctx, row)
}

// DeleteRelease removes a release from the repository.
func (s *ReleaseService) DeleteRelease(ctx context.Context, actorPK int64, owner, name string, id int64) error {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	row, err := s.store.GetReleaseByID(ctx, repo.PK, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrReleaseNotFound
	}
	if err != nil {
		return err
	}
	return s.store.DeleteRelease(ctx, row.PK)
}

// GenerateNotesInput is the request for automatic release-notes generation.
type GenerateNotesInput struct {
	TagName         string
	TargetCommitish string
	PreviousTagName string
}

// GeneratedNotes carries the material for automatic release notes: the commits
// landing in the release and the previous tag the range was cut against, empty
// when the repository has no earlier release to diff from.
type GeneratedNotes struct {
	TagName         string
	PreviousTagName string
	Commits         []git.Commit
}

// GenerateReleaseNotes collects the commit range automatic release notes are
// written from. The actor must have write access. The tag usually does not
// exist yet (gh generates notes before creating the release), so resolution
// falls back from the tag to the requested target commitish to the default
// branch, and the previous tag defaults to the latest published release.
func (s *ReleaseService) GenerateReleaseNotes(ctx context.Context, actorPK int64, owner, name string, in GenerateNotesInput) (*GeneratedNotes, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	if in.TagName == "" {
		return nil, ErrValidation
	}
	target := in.TagName
	if _, err := s.repos.GetCommit(repo, target); err != nil {
		target = in.TargetCommitish
		if target == "" {
			target = repo.DefaultBranch
		}
	}
	prev := in.PreviousTagName
	if prev == "" {
		if latest, err := s.store.GetLatestRelease(ctx, repo.PK); err == nil && latest.TagName != in.TagName {
			prev = latest.TagName
		}
	}
	out := &GeneratedNotes{TagName: in.TagName, PreviousTagName: prev}
	if prev != "" {
		cmp, err := s.repos.Compare(ctx, repo, prev, target)
		if err == nil {
			out.Commits = cmp.Commits
			return out, nil
		}
		// The previous tag no longer resolves; note from plain history instead.
		out.PreviousTagName = ""
	}
	commits, err := s.repos.ListCommits(repo, git.LogOpts{From: target})
	if err != nil {
		return nil, err
	}
	out.Commits = commits
	return out, nil
}

// ListReleaseAssets returns all assets for a release.
func (s *ReleaseService) ListReleaseAssets(ctx context.Context, actorPK int64, owner, name string, releaseID int64) ([]*ReleaseAsset, error) {
	repo, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReleaseByID(ctx, repo.PK, releaseID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.hydrateAssets(ctx, row.PK)
}

// GetReleaseAsset returns a single release asset.
func (s *ReleaseService) GetReleaseAsset(ctx context.Context, actorPK int64, owner, name string, assetID int64) (*ReleaseAsset, error) {
	_, err := s.repos.GetRepo(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReleaseAssetByID(ctx, assetID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseAssetNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.assetFromRow(ctx, row)
}

// UploadReleaseAsset writes the binary to disk and records the asset row.
// The caller must close r; the service reads from it until EOF.
func (s *ReleaseService) UploadReleaseAsset(ctx context.Context, actorPK int64, owner, name string, releaseID int64, assetName, label, contentType string, r io.Reader) (*ReleaseAsset, error) {
	repo, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	releaseRow, err := s.store.GetReleaseByID(ctx, repo.PK, releaseID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseNotFound
	}
	if err != nil {
		return nil, err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	var labelPtr *string
	if label != "" {
		labelPtr = &label
	}
	row := &store.ReleaseAssetRow{
		ReleasePK:   releaseRow.PK,
		Name:        assetName,
		Label:       labelPtr,
		ContentType: contentType,
		UploaderPK:  &actorPK,
		State:       "open",
	}
	if err := s.store.InsertReleaseAsset(ctx, row); err != nil {
		return nil, err
	}
	// Write the binary to disk.
	if err := os.MkdirAll(s.assetDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(s.assetDir, i64Str(row.PK))
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	n, copyErr := io.Copy(f, r)
	if closeErr := f.Close(); closeErr != nil && copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(path)
		_ = s.store.DeleteReleaseAsset(ctx, row.PK)
		return nil, copyErr
	}
	row.Size = n
	row.State = "uploaded"
	if err := s.store.UpdateReleaseAsset(ctx, row); err != nil {
		return nil, err
	}
	return s.assetFromRow(ctx, row)
}

// UpdateReleaseAsset edits the name or label of an asset.
func (s *ReleaseService) UpdateReleaseAsset(ctx context.Context, actorPK int64, owner, name string, assetID int64, assetName, label *string) (*ReleaseAsset, error) {
	_, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return nil, err
	}
	row, err := s.store.GetReleaseAssetByID(ctx, assetID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrReleaseAssetNotFound
	}
	if err != nil {
		return nil, err
	}
	if assetName != nil {
		row.Name = *assetName
	}
	if label != nil {
		row.Label = label
	}
	if err := s.store.UpdateReleaseAsset(ctx, row); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrReleaseAssetNotFound
		}
		return nil, err
	}
	return s.assetFromRow(ctx, row)
}

// DeleteReleaseAsset removes an asset and its binary file.
func (s *ReleaseService) DeleteReleaseAsset(ctx context.Context, actorPK int64, owner, name string, assetID int64) error {
	_, err := s.repos.AuthorizeWrite(ctx, actorPK, owner, name)
	if err != nil {
		return err
	}
	row, err := s.store.GetReleaseAssetByID(ctx, assetID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrReleaseAssetNotFound
	}
	if err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.assetDir, i64Str(row.PK)))
	return s.store.DeleteReleaseAsset(ctx, row.PK)
}

// ServeAsset increments the download counter and returns the file path so the
// handler can serve it (http.ServeFile handles range requests and ETags).
func (s *ReleaseService) ServeAsset(ctx context.Context, assetID int64) (string, error) {
	row, err := s.store.GetReleaseAssetByID(ctx, assetID)
	if errors.Is(err, store.ErrNotFound) {
		return "", ErrReleaseAssetNotFound
	}
	if err != nil {
		return "", err
	}
	_ = s.store.IncrementAssetDownloadCount(ctx, row.PK)
	return filepath.Join(s.assetDir, i64Str(row.PK)), nil
}

// --- hydration helpers ------------------------------------------------------

func (s *ReleaseService) hydrateRelease(ctx context.Context, row *store.ReleaseRow) (*Release, error) {
	rel := releaseFromRow(row)
	if row.AuthorPK != nil {
		u, err := s.store.UserByPK(ctx, *row.AuthorPK)
		if err == nil {
			rel.Author = userFromRow(u)
		}
	}
	assets, err := s.hydrateAssets(ctx, row.PK)
	if err != nil {
		return nil, err
	}
	rel.Assets = assets
	return rel, nil
}

func (s *ReleaseService) hydrateReleases(ctx context.Context, rows []store.ReleaseRow) ([]*Release, error) {
	if len(rows) == 0 {
		return []*Release{}, nil
	}
	pks := make([]int64, 0, len(rows))
	for i := range rows {
		if rows[i].AuthorPK != nil {
			pks = append(pks, *rows[i].AuthorPK)
		}
	}
	users, _ := s.store.UsersByPKs(ctx, pks)
	out := make([]*Release, len(rows))
	for i := range rows {
		r := releaseFromRow(&rows[i])
		if rows[i].AuthorPK != nil {
			if u, ok := users[*rows[i].AuthorPK]; ok {
				r.Author = userFromRow(u)
			}
		}
		assets, err := s.hydrateAssets(ctx, rows[i].PK)
		if err != nil {
			return nil, err
		}
		r.Assets = assets
		out[i] = r
	}
	return out, nil
}

func (s *ReleaseService) hydrateAssets(ctx context.Context, releasePK int64) ([]*ReleaseAsset, error) {
	rows, err := s.store.ListReleaseAssets(ctx, releasePK)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []*ReleaseAsset{}, nil
	}
	pks := make([]int64, 0, len(rows))
	for i := range rows {
		if rows[i].UploaderPK != nil {
			pks = append(pks, *rows[i].UploaderPK)
		}
	}
	users, _ := s.store.UsersByPKs(ctx, pks)
	out := make([]*ReleaseAsset, len(rows))
	for i := range rows {
		a, err := s.assetFromRow(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		if rows[i].UploaderPK != nil {
			if u, ok := users[*rows[i].UploaderPK]; ok {
				a.Uploader = userFromRow(u)
			}
		}
		out[i] = a
	}
	return out, nil
}

func (s *ReleaseService) assetFromRow(ctx context.Context, row *store.ReleaseAssetRow) (*ReleaseAsset, error) {
	a := assetFromRow(row)
	if row.UploaderPK != nil {
		u, err := s.store.UserByPK(ctx, *row.UploaderPK)
		if err == nil {
			a.Uploader = userFromRow(u)
		}
	}
	return a, nil
}

func releaseFromRow(r *store.ReleaseRow) *Release {
	return &Release{
		PK:              r.PK,
		ID:              r.DBID,
		RepoPK:          r.RepoPK,
		TagName:         r.TagName,
		TargetCommitish: r.TargetCommitish,
		Name:            r.Name,
		Body:            r.Body,
		Draft:           r.Draft,
		Prerelease:      r.Prerelease,
		CreatedAt:       r.CreatedAt,
		PublishedAt:     r.PublishedAt,
		UpdatedAt:       r.UpdatedAt,
	}
}

func assetFromRow(a *store.ReleaseAssetRow) *ReleaseAsset {
	return &ReleaseAsset{
		PK:            a.PK,
		ID:            a.DBID,
		ReleasePK:     a.ReleasePK,
		ReleaseID:     a.ReleaseDBID,
		Name:          a.Name,
		Label:         a.Label,
		ContentType:   a.ContentType,
		Size:          a.Size,
		DownloadCount: a.DownloadCount,
		State:         a.State,
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
	}
}

// i64Str formats an int64 as a decimal string for use as a filename.
func i64Str(n int64) string {
	var buf [20]byte
	pos := len(buf)
	if n == 0 {
		return "0"
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
