package graphql

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamnd/githome/config"
	"github.com/tamnd/githome/domain"
	"github.com/tamnd/githome/nodeid"
	"github.com/tamnd/githome/presenter"
	"github.com/tamnd/githome/store"
)

// countingBatchStore implements the batch-store surface domain.NewBatcher
// consumes, counting how many times each batch query fires. The loader tests
// use it to prove N concurrent parent resolutions collapse into one query.
type countingBatchStore struct {
	userCalls     atomic.Int64
	labelCalls    atomic.Int64
	assigneeCalls atomic.Int64
	commentCalls  atomic.Int64
	rollupCalls   atomic.Int64
}

func (c *countingBatchStore) UsersByPKs(ctx context.Context, pks []int64) (map[int64]*store.UserRow, error) {
	c.userCalls.Add(1)
	out := make(map[int64]*store.UserRow, len(pks))
	for _, pk := range pks {
		out[pk] = &store.UserRow{PK: pk, DBID: pk, Login: fmt.Sprintf("user%d", pk), Type: "User"}
	}
	return out, nil
}

func (c *countingBatchStore) LabelsByIssuePKs(ctx context.Context, pks []int64) (map[int64][]store.LabelRow, error) {
	c.labelCalls.Add(1)
	out := make(map[int64][]store.LabelRow, len(pks))
	for _, pk := range pks {
		out[pk] = []store.LabelRow{{PK: pk, DBID: pk, Name: fmt.Sprintf("label%d", pk), Color: "ededed"}}
	}
	return out, nil
}

func (c *countingBatchStore) AssigneesByIssuePKs(ctx context.Context, pks []int64) (map[int64][]int64, error) {
	c.assigneeCalls.Add(1)
	out := make(map[int64][]int64, len(pks))
	for _, pk := range pks {
		out[pk] = []int64{pk + 1000}
	}
	return out, nil
}

func (c *countingBatchStore) MilestonesByPKs(ctx context.Context, pks []int64) (map[int64]*store.MilestoneRow, error) {
	return map[int64]*store.MilestoneRow{}, nil
}

func (c *countingBatchStore) ReactionRollupsBySubjectPKs(ctx context.Context, subjectType string, pks []int64) (map[int64]store.ReactionRollup, error) {
	c.rollupCalls.Add(1)
	return map[int64]store.ReactionRollup{}, nil
}

func (c *countingBatchStore) CommentsByIssuePKs(ctx context.Context, issuePKs []int64, perIssue int) (map[int64][]store.CommentRow, error) {
	c.commentCalls.Add(1)
	now := time.Now()
	out := make(map[int64][]store.CommentRow, len(issuePKs))
	for _, pk := range issuePKs {
		n := perIssue
		if n > 2 {
			n = 2
		}
		for i := 0; i < n; i++ {
			out[pk] = append(out[pk], store.CommentRow{
				PK:        pk*100 + int64(i),
				DBID:      pk*100 + int64(i),
				IssuePK:   pk,
				UserPK:    pk,
				Body:      fmt.Sprintf("comment %d on issue %d", i, pk),
				CreatedAt: now,
				UpdatedAt: now,
			})
		}
	}
	return out, nil
}

func testLoaders(t *testing.T, cs *countingBatchStore) *Loaders {
	t.Helper()
	mustURL := func(s string) *url.URL {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return u
	}
	urls := config.URLs{
		API:     mustURL("https://git.test.internal/api/v3"),
		HTML:    mustURL("https://git.test.internal"),
		GraphQL: mustURL("https://git.test.internal/api/graphql"),
		SSHHost: "git.test.internal",
		SSHPort: 22,
	}
	return newLoaders(domain.NewBatcher(cs), presenter.NewURLBuilder(urls), nodeid.FormatNew)
}

// TestLoaderCommentsPreviewBatches proves the fan-out shape an issue
// connection produces, N nodes each selecting comments(first: k), reaches the
// store as one comment query plus one author query, not N of each.
func TestLoaderCommentsPreviewBatches(t *testing.T) {
	cs := &countingBatchStore{}
	loaders := testLoaders(t, cs)
	ctx := context.Background()

	const parents = 25
	var wg sync.WaitGroup
	errs := make([]error, parents)
	got := make([][]*domain.Comment, parents)
	for i := 0; i < parents; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = loaders.CommentsByIssue.Load(ctx, commentsPreviewKey{IssuePK: int64(i + 1), Limit: 5})
		}(i)
	}
	wg.Wait()

	for i := 0; i < parents; i++ {
		if errs[i] != nil {
			t.Fatalf("load %d: %v", i, errs[i])
		}
		if len(got[i]) != 2 {
			t.Fatalf("load %d: got %d comments, want 2", i, len(got[i]))
		}
		wantBody := fmt.Sprintf("comment 0 on issue %d", i+1)
		if got[i][0].Body != wantBody {
			t.Errorf("load %d: body = %q, want %q", i, got[i][0].Body, wantBody)
		}
		if got[i][0].User == nil || got[i][0].User.Login != fmt.Sprintf("user%d", i+1) {
			t.Errorf("load %d: author not resolved", i)
		}
	}

	if n := cs.commentCalls.Load(); n != 1 {
		t.Errorf("comment queries = %d, want 1 for %d parents", n, parents)
	}
	if n := cs.userCalls.Load(); n != 1 {
		t.Errorf("author queries = %d, want 1 for %d parents", n, parents)
	}
	if n := cs.rollupCalls.Load(); n != 1 {
		t.Errorf("rollup queries = %d, want 1 for %d parents", n, parents)
	}
}

// TestLoaderCommentsPreviewMixedLimits checks that distinct first: arguments
// in one wave run one query per limit, each answering its own keys.
func TestLoaderCommentsPreviewMixedLimits(t *testing.T) {
	cs := &countingBatchStore{}
	loaders := testLoaders(t, cs)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			limit := 5
			if i%2 == 0 {
				limit = 10
			}
			if _, err := loaders.CommentsByIssue.Load(ctx, commentsPreviewKey{IssuePK: int64(i + 1), Limit: limit}); err != nil {
				t.Errorf("load %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	if n := cs.commentCalls.Load(); n != 2 {
		t.Errorf("comment queries = %d, want 2 for two distinct limits", n)
	}
}

// TestLoaderLabelsAndAssigneesBatch covers the other per-parent fan-outs: N
// issues resolving labels and assignees cost one query each, with assignee
// users folded into a single author batch.
func TestLoaderLabelsAndAssigneesBatch(t *testing.T) {
	cs := &countingBatchStore{}
	loaders := testLoaders(t, cs)
	ctx := context.Background()

	const parents = 20
	var wg sync.WaitGroup
	for i := 0; i < parents; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			labels, err := loaders.LabelsByIssue.Load(ctx, int64(i+1))
			if err != nil {
				t.Errorf("labels %d: %v", i, err)
				return
			}
			if len(labels) != 1 || labels[0].Name != fmt.Sprintf("label%d", i+1) {
				t.Errorf("labels %d: unexpected result %+v", i, labels)
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			users, err := loaders.AssigneesByIssue.Load(ctx, int64(i+1))
			if err != nil {
				t.Errorf("assignees %d: %v", i, err)
				return
			}
			if len(users) != 1 {
				t.Errorf("assignees %d: got %d users, want 1", i, len(users))
			}
		}(i)
	}
	wg.Wait()

	if n := cs.labelCalls.Load(); n != 1 {
		t.Errorf("label queries = %d, want 1 for %d parents", n, parents)
	}
	if n := cs.assigneeCalls.Load(); n != 1 {
		t.Errorf("assignee queries = %d, want 1 for %d parents", n, parents)
	}
}
