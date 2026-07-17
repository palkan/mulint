package tests

import "sync"

type fpStore struct {
	mu    sync.Mutex
	dirty bool
	data  map[string]string
}

func (fs *fpStore) applyWithClosure(task string) {
	fs.mu.Lock()
	cleanup := func() {
		fs.dirty = false
		fs.mu.Unlock()
	}
	fs.data[task] = "done"
	cleanup()
}

func (fs *fpStore) CallApplyWithClosure(task string) int {
	fs.applyWithClosure(task)
	return len(fs.data) // want "Mutex lock must be released before this line"
}
