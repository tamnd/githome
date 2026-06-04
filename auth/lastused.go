package auth

import (
	"context"
	"time"
)

// lastUsedWriter debounces last_used_at writes off the request hot path.
// Touching the row on every authenticated request would write-amplify a column
// nobody reads in real time, so we coalesce touches per token and flush the
// latest timestamp for each on a ticker. A bounded channel drops touches under
// extreme load rather than blocking the request.
type lastUsedWriter struct {
	store Store
	ch    chan tokenTouch
	stop  chan struct{}
}

type tokenTouch struct {
	tokenID int64
	at      time.Time
}

// nowFunc is overridable in tests; production uses the real clock.
var nowFunc = time.Now

func newLastUsedWriter(st Store) *lastUsedWriter {
	w := &lastUsedWriter{
		store: st,
		ch:    make(chan tokenTouch, 4096),
		stop:  make(chan struct{}),
	}
	go w.run()
	return w
}

// touch records that a token was just used. It never blocks: a full queue drops
// the touch, since last_used_at is best-effort.
func (w *lastUsedWriter) touch(tokenID int64) {
	if tokenID == 0 {
		return
	}
	select {
	case w.ch <- tokenTouch{tokenID: tokenID, at: nowFunc()}:
	default:
	}
}

// Close stops the background flusher.
func (w *lastUsedWriter) Close() { close(w.stop) }

func (w *lastUsedWriter) run() {
	pending := map[int64]time.Time{}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := pending
		pending = map[int64]time.Time{}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = w.store.BumpTokenLastUsed(ctx, batch)
		cancel()
	}
	for {
		select {
		case t := <-w.ch:
			pending[t.tokenID] = t.at // keep only the latest per token
		case <-tick.C:
			flush()
		case <-w.stop:
			flush()
			return
		}
	}
}
