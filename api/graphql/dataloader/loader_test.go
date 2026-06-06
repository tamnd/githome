package dataloader_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tamnd/githome/api/graphql/dataloader"
)

func TestLoaderBatchesMultipleCalls(t *testing.T) {
	var fetchCount atomic.Int32
	l := dataloader.New(func(ctx context.Context, pks []int64) (map[int64]string, error) {
		fetchCount.Add(1)
		m := make(map[int64]string, len(pks))
		for _, pk := range pks {
			m[pk] = "user"
		}
		return m, nil
	}, 5*time.Millisecond)

	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]string, 5)
	for i := range 5 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err := l.Load(ctx, int64(idx))
			if err != nil {
				t.Errorf("Load(%d): %v", idx, err)
			}
			results[idx] = v
		}(i)
	}
	wg.Wait()

	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("expected 1 fetch call, got %d", got)
	}
	for i, r := range results {
		if r != "user" {
			t.Fatalf("results[%d] = %q, want %q", i, r, "user")
		}
	}
}

func TestLoaderPrimePreventsDBCall(t *testing.T) {
	var fetchCount atomic.Int32
	l := dataloader.New(func(ctx context.Context, pks []int64) (map[int64]string, error) {
		fetchCount.Add(1)
		m := make(map[int64]string, len(pks))
		for _, pk := range pks {
			m[pk] = "from-db"
		}
		return m, nil
	}, 5*time.Millisecond)

	ctx := context.Background()
	l.Prime(42, "primed")

	v, err := l.Load(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	if v != "primed" {
		t.Fatalf("got %q, want %q", v, "primed")
	}
	if got := fetchCount.Load(); got != 0 {
		t.Fatalf("expected 0 fetch calls after Prime, got %d", got)
	}
}

func TestLoaderMissReturnsZero(t *testing.T) {
	l := dataloader.New(func(_ context.Context, pks []int64) (map[int64]string, error) {
		return map[int64]string{}, nil // nothing found
	}, 5*time.Millisecond)

	v, err := l.Load(context.Background(), 99)
	if err != nil {
		t.Fatal(err)
	}
	if v != "" {
		t.Fatalf("missing key: got %q, want empty", v)
	}
}

func TestLoaderCachesAfterFirstLoad(t *testing.T) {
	var fetchCount atomic.Int32
	l := dataloader.New(func(_ context.Context, pks []int64) (map[int64]string, error) {
		fetchCount.Add(1)
		m := make(map[int64]string, len(pks))
		for _, pk := range pks {
			m[pk] = "v"
		}
		return m, nil
	}, 5*time.Millisecond)

	ctx := context.Background()
	if _, err := l.Load(ctx, 1); err != nil {
		t.Fatal(err)
	}
	// second Load for the same key should hit the primed cache, not fire a new batch
	time.Sleep(20 * time.Millisecond)
	if _, err := l.Load(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("expected 1 fetch call total, got %d", got)
	}
}
