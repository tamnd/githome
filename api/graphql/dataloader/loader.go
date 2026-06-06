// Package dataloader provides a generic per-request batch loader. The Loader
// accumulates keys from concurrent Load calls that arrive within a short window
// and fires one fetch per window, distributing results back to all waiters.
// This is the standard gqlgen dataloader pattern.
package dataloader

import (
	"context"
	"sync"
	"time"
)

// Loader batches concurrent Load calls into one fetch call. K is the key type
// and V is the value type. The zero value is not usable; construct with New.
type Loader[K comparable, V any] struct {
	fetch func(ctx context.Context, keys []K) (map[K]V, error)
	wait  time.Duration

	mu       sync.Mutex
	primed   map[K]result[V]        // values seeded by Prime
	pending  map[K][]chan result[V] // in-flight waiters per key
	keys     []K                    // ordered, deduped keys for next batch
	timer    *time.Timer
	batchCtx context.Context
}

type result[V any] struct {
	val V
	err error
}

// New returns a Loader that coalesces Load calls arriving within wait into a
// single fetch call.
func New[K comparable, V any](fetch func(context.Context, []K) (map[K]V, error), wait time.Duration) *Loader[K, V] {
	return &Loader[K, V]{
		fetch:  fetch,
		wait:   wait,
		primed: make(map[K]result[V]),
	}
}

// Load registers key in the current batch window and blocks until the batch
// fires. If key was seeded via Prime the cached value is returned immediately
// without touching the fetch function.
func (l *Loader[K, V]) Load(ctx context.Context, key K) (V, error) {
	l.mu.Lock()
	if r, ok := l.primed[key]; ok {
		l.mu.Unlock()
		return r.val, r.err
	}
	ch := make(chan result[V], 1)
	if l.pending == nil {
		l.pending = make(map[K][]chan result[V])
	}
	newKey := len(l.pending[key]) == 0
	l.pending[key] = append(l.pending[key], ch)
	if newKey {
		l.keys = append(l.keys, key)
	}
	if l.timer == nil {
		l.batchCtx = ctx
		l.timer = time.AfterFunc(l.wait, l.fire)
	}
	l.mu.Unlock()

	r := <-ch
	return r.val, r.err
}

// Prime seeds the value for key so that future Load calls return it immediately
// without calling fetch. Any goroutines already waiting on key are unblocked.
func (l *Loader[K, V]) Prime(key K, val V) {
	l.mu.Lock()
	r := result[V]{val: val}
	l.primed[key] = r
	waiters := l.pending[key]
	delete(l.pending, key)
	// Remove from the pending keys slice.
	for i, k := range l.keys {
		if k == key {
			l.keys = append(l.keys[:i], l.keys[i+1:]...)
			break
		}
	}
	l.mu.Unlock()

	for _, ch := range waiters {
		ch <- r
	}
}

// fire is called by the timer; it snapshots and clears the pending batch, then
// calls fetch and distributes results to all waiting goroutines.
func (l *Loader[K, V]) fire() {
	l.mu.Lock()
	keys := l.keys
	pending := l.pending
	l.keys = nil
	l.pending = nil
	l.timer = nil
	ctx := l.batchCtx
	l.mu.Unlock()

	if len(keys) == 0 {
		return
	}

	results, err := l.fetch(ctx, keys)

	l.mu.Lock()
	for _, k := range keys {
		var r result[V]
		if err != nil {
			r.err = err
		} else {
			r.val = results[k]
		}
		l.primed[k] = r
	}
	l.mu.Unlock()

	for k, waiters := range pending {
		var r result[V]
		if err != nil {
			r.err = err
		} else {
			r.val = results[k]
		}
		for _, ch := range waiters {
			ch <- r
		}
	}
}
