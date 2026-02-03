package tests

import (
	"sync"
	"time"
)

type async struct {
	mu    sync.Mutex
	timer *time.Timer
	data  map[string]string
}

func (a *async) GoStatementCallback() {
	a.mu.Lock()
	defer a.mu.Unlock()

	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.data["go"] = "done"
	}()
}

func (a *async) DirectRecursiveLock() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.mu.Lock() // want "Mutex lock is acquired on this line"
	a.mu.Unlock()
}

func (a *async) TransitiveWithAfterFunc() {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.timer == nil {
		a.timer = time.AfterFunc(time.Second, func() {
			a.mu.Lock()
			defer a.mu.Unlock()
		})
	}

	a.helper() // want "Mutex lock is acquired on this line"
}

func (a *async) helper() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.data["helper"] = "called"
}

func (a *async) TransitiveInsideIf(condition bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if condition {
		a.helper() // want "Mutex lock is acquired on this line"
	}
}

func (a *async) TransitiveInsideFor() {
	a.mu.Lock()
	defer a.mu.Unlock()

	for i := 0; i < 10; i++ {
		a.helper() // want "Mutex lock is acquired on this line"
	}
}

func (a *async) TransitiveInsideSwitch(val int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	switch val {
	case 1:
		a.helper() // want "Mutex lock is acquired on this line"
	case 2:
		a.data["two"] = "2"
	}
}

// CentrifugePattern - exact pattern from centrifuge/internal/queue/queue.go
// No defer, manual unlock at end, with early return branch
func (a *async) CentrifugePattern(delay int) {
	a.mu.Lock()

	if delay == 0 {
		a.data["immediate"] = "done"
		a.mu.Unlock()
		return
	}

	if a.timer == nil {
		a.timer = time.AfterFunc(time.Second, func() {
			a.mu.Lock() // Should NOT be flagged - runs asynchronously
			a.data["delayed"] = "done"
			a.mu.Unlock()
		})
	} else {
		a.timer.Reset(time.Second)
	}
	a.mu.Unlock()
}
