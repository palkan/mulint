package tests

import "sync"

type deferStore struct {
	mu    sync.Mutex
	dirty bool
	data  map[string]string
}

func (ds *deferStore) SentinelDefer(task string) int {
	ds.mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			ds.mu.Unlock()
		}
	}()

	if _, ok := ds.data[task]; ok {
		return 0 // covered by the sentinel defer: no diagnostic
	}

	ds.data[task] = "done"
	ds.mu.Unlock()
	unlocked = true

	return 1
}

func (ds *deferStore) RelockingDefer(task string) {
	ds.mu.Lock()

	defer func() {
		ds.mu.Lock() // runs after the manual release: no diagnostic
		ds.dirty = false
		ds.mu.Unlock()
	}()

	ds.data[task] = "done"
	ds.mu.Unlock()
}

func (ds *deferStore) RelockingDeferStillLeaks(task string) int {
	ds.mu.Lock()

	defer func() {
		ds.mu.Lock()
		ds.dirty = false
		ds.mu.Unlock()
	}()

	if task == "" {
		return 0 // want "Mutex lock must be released before this line"
	}

	ds.data[task] = "done"
	ds.mu.Unlock()
	return 1
}

func (ds *deferStore) RelockingDeferLIFODeadlock(task string) {
	ds.mu.Lock()
	defer ds.mu.Unlock()

	defer func() {
		ds.mu.Lock() // want "Mutex lock is acquired on this line"
		ds.dirty = false
		ds.mu.Unlock()
	}()

	ds.data[task] = "done"
}

func (ds *deferStore) RelockingDeferSafeOrdering(task string) {
	ds.mu.Lock()

	defer func() {
		ds.mu.Lock() // deferred unlock below runs first: no diagnostic
		ds.dirty = false
		ds.mu.Unlock()
	}()

	defer ds.mu.Unlock()

	ds.data[task] = "done"
}

func (ds *deferStore) RelockingDeferLeakedScope(task string) int {
	ds.mu.Lock()

	defer func() {
		ds.mu.Lock() // want "Mutex lock is acquired on this line"
		ds.dirty = false
		ds.mu.Unlock()
	}()

	ds.data[task] = "done"
	return len(ds.data) // want "Mutex lock must be released before this line"
}
