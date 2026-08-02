package tests

import "sync"

// Shapes where a mutex is temporarily released and re-acquired inside a
// conditional branch, while every path still ends balanced. Modeled on
// teleport's recordingPlayback.createTaskContext: the helper must not be
// classified as a lock wrapper, and callers returning after it must not be
// flagged.

type relockTask struct {
	mu     sync.Mutex
	wg     sync.WaitGroup
	cancel func()
}

func (r *relockTask) createTask() func() {
	r.mu.Lock()

	if r.cancel != nil {
		r.cancel()
		r.mu.Unlock()

		// wait without holding the lock to avoid deadlock
		r.wg.Wait()

		r.mu.Lock()
	}

	cancel := func() {}
	r.cancel = cancel
	r.mu.Unlock()

	return cancel
}

func (r *relockTask) HandleFetch(valid bool) {
	cancel := r.createTask()

	if !valid {
		return
	}

	cancel()
}

// The unlock-wait-relock branch does not excuse a missing final unlock.
func (r *relockTask) createTaskLeaky(cond bool) {
	r.mu.Lock()

	if cond {
		r.mu.Unlock()
		r.wg.Wait()
		r.mu.Lock()
	}

	return // want "Mutex lock must be released before this line"
}

// Re-acquiring in mutually exclusive branches, released by the shared
// unlock after the conditional: balanced on every path.
func (r *relockTask) relockPerBranch(cond bool) {
	r.mu.Lock()

	if cond {
		r.mu.Unlock()
		r.wg.Wait()
		r.mu.Lock()
	} else {
		r.mu.Unlock()
		r.mu.Lock()
	}

	r.cancel = nil
	r.mu.Unlock()
}

func (r *relockTask) CallRelockPerBranch(cond bool) func() {
	r.relockPerBranch(cond)
	return r.cancel
}
