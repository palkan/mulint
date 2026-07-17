package tests

import "sync"

type closureStore struct {
	mu    sync.Mutex
	clMu  sync.Mutex
	dirty bool
	data  map[string]string
}

func (cs *closureStore) UnlockClosure(task string) int {
	cs.mu.Lock()

	clearAndUnlock := func() {
		cs.dirty = false
		cs.mu.Unlock()
	}

	if _, ok := cs.data[task]; ok {
		clearAndUnlock()
		return 0 // released by the closure: no diagnostic
	}

	cs.data[task] = "done"
	clearAndUnlock()
	return 1
}

func (cs *closureStore) MultiUnlockClosure(task string) int {
	cs.mu.Lock()
	cs.clMu.Lock()

	releaseAll := func() {
		cs.mu.Unlock()
		cs.clMu.Unlock()
	}

	if _, ok := cs.data[task]; ok {
		releaseAll()
		return 0 // both released: no diagnostic
	}

	cs.data[task] = "done"
	releaseAll()
	return 1
}

func (cs *closureStore) DeferredClosureCall(task string) int {
	cs.mu.Lock()

	cleanup := func() {
		cs.dirty = false
		cs.mu.Unlock()
	}
	defer cleanup()

	if _, ok := cs.data[task]; ok {
		return 0 // covered by the deferred closure: no diagnostic
	}

	cs.data[task] = "done"
	return 1
}

func (cs *closureStore) ConditionalUnlockClosure(task string) int {
	cs.mu.Lock()

	maybeUnlock := func(really bool) {
		if really {
			cs.mu.Unlock()
		}
	}

	maybeUnlock(task != "")
	return len(cs.data) // want "Mutex lock must be released before this line"
}

func (cs *closureStore) ReassignedClosure(task string) int {
	cs.mu.Lock()

	cleanup := func() {
		cs.mu.Unlock()
	}
	cleanup = func() {
		cs.dirty = false
	}

	cleanup()
	return len(cs.data) // want "Mutex lock must be released before this line"
}

func runLater(f func()) { _ = f }

func (cs *closureStore) EscapingClosure(task string) int {
	cs.mu.Lock()

	cleanup := func() {
		cs.mu.Unlock()
	}
	runLater(cleanup)

	return len(cs.data) // want "Mutex lock must be released before this line"
}

func (cs *closureStore) BalancedClosure(task string) int {
	cs.mu.Lock()

	cycle := func() {
		cs.mu.Lock()
		cs.dirty = false
		cs.mu.Unlock()
	}
	cycle()

	return len(cs.data) // want "Mutex lock must be released before this line"
}
