package tests

import "sync"

// Known-limitation corpus distilled from nats-server. Each shape below is
// legal, deliberate production Go that mulint currently reports. The `want`
// comments document these as KNOWN false positives: if a future change makes
// one of them disappear, the test fails and the comment should be removed —
// that's a precision improvement worth celebrating in the changelog.

type fpStore struct {
	mu    sync.Mutex
	dirty bool
	data  map[string]string
}

// Sentinel-guarded defer (nats-server filestore.go:549): defer safety plus
// manual early release. The unlock is nested inside the deferred closure's
// if statement, which deferred-unlock detection does not descend into.
func (fs *fpStore) SentinelDefer(task string) int {
	fs.mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			fs.mu.Unlock()
		}
	}()

	if _, ok := fs.data[task]; ok {
		return 0 // want "Mutex lock must be released before this line"
	}

	fs.data[task] = "done"
	fs.mu.Unlock()
	unlocked = true

	// The direct-unlock path is understood — no diagnostic here. Only the
	// early return above, which relies on the sentinel defer, is reported.
	return 1
}

// Unlock hidden in a named local closure (nats-server jetstream_cluster.go:4139
// clearAndUnlock): error paths release state + lock through a closure.
func (fs *fpStore) UnlockClosure(task string) int {
	fs.mu.Lock()

	clearAndUnlock := func() {
		fs.dirty = false
		fs.mu.Unlock()
	}

	if _, ok := fs.data[task]; ok {
		clearAndUnlock()
		return 0 // want "Mutex lock must be released before this line"
	}

	fs.data[task] = "done"
	clearAndUnlock()
	return 1 // want "Mutex lock must be released before this line"
}

// Defer that re-acquires the lock (nats-server filestore.go:10479): a deferred
// closure that runs after all paths released, lexically inside the critical
// section. Reads as a recursive lock to positional analysis.
func (fs *fpStore) RelockingDefer(task string) {
	fs.mu.Lock()

	defer func() {
		fs.mu.Lock() // want "Mutex lock is acquired on this line"
		fs.dirty = false
		fs.mu.Unlock()
	}()

	fs.data[task] = "done"
	fs.mu.Unlock()
}

// hasLock boolean parameter (nats-server client.go:4150 pubAllowedFullCheck).
// A top-level `if !hasLock { Lock }` guard IS recognized (see putSimple), but
// nats nests the guard inside another condition — and nested guards are not:
// the conditional-lock scan only reads top-level statements.
func (fs *fpStore) putSimple(task string, hasLock bool) {
	if !hasLock {
		fs.mu.Lock()
	}
	fs.data[task] = "done"
	if !hasLock {
		fs.mu.Unlock()
	}
}

func (fs *fpStore) putNested(task string, fullCheck, hasLock bool) {
	if fullCheck && task != "" {
		if !hasLock {
			fs.mu.Lock()
		}
		fs.data[task] = "done"
		if !hasLock {
			fs.mu.Unlock()
		}
	}
}

func (fs *fpStore) PutLocked(task string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.putSimple(task, true) // recognized: no diagnostic

	fs.putNested(task, true, true) // want "Mutex lock is acquired on this line"
}
