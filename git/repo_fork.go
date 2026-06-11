package git

import "context"

// ForkFrom populates the bare repository for dstPK with the refs and objects
// of the repository at srcPK: one local fetch over the filesystem, so objects
// land in a pack without an intermediate worktree. headOnly restricts the copy
// to the source's default branch (the fork API's default_branch_only flag);
// otherwise every branch and tag comes across. The destination's HEAD is
// pointed at defaultBranch so the fork opens on the same branch as its source.
// Forking an empty source succeeds and leaves an empty fork, matching GitHub.
func (s *Store) ForkFrom(ctx context.Context, srcPK, dstPK int64, defaultBranch string, headOnly bool) error {
	if _, err := s.Init(dstPK); err != nil {
		return err
	}
	refspecs := []string{"refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*"}
	if headOnly && defaultBranch != "" {
		refspecs = []string{"refs/heads/" + defaultBranch + ":refs/heads/" + defaultBranch}
	}
	// --no-tags keeps fetch from auto-following tags, so the refspecs alone
	// decide what comes across; the full copy names refs/tags/* explicitly.
	args := append([]string{"fetch", "--quiet", "--no-tags", s.runDir(srcPK)}, refspecs...)
	r, err := s.run(ctx, dstPK, nil, args...)
	if err != nil {
		return err
	}
	if r.code != 0 {
		return fail(args, r)
	}
	if defaultBranch != "" {
		args = []string{"symbolic-ref", "HEAD", "refs/heads/" + defaultBranch}
		r, err = s.run(ctx, dstPK, nil, args...)
		if err != nil {
			return err
		}
		if r.code != 0 {
			return fail(args, r)
		}
	}
	s.InvalidateRepo(dstPK)
	return nil
}
