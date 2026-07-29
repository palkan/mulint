package tests

import "sync"

// customLocker has Lock/Unlock methods but is NOT a sync.Mutex.
// The wrapper-aware tracker must not create scopes for it.
type customLocker struct {
	acquired bool
}

func (l *customLocker) Lock()   { l.acquired = true }
func (l *customLocker) Unlock() { l.acquired = false }

type wrapperWithCustomLocker struct {
	m  sync.Mutex
	cl customLocker
}

func (w *wrapperWithCustomLocker) Acquire() {
	w.m.Lock()
}

func (w *wrapperWithCustomLocker) Release() {
	w.m.Unlock()
}

// WrapperTypeFilterNoFP uses Acquire/Release (wrapper around m) and calls
// cl.Lock — which is NOT a mutex. No diagnostics should be reported.
func (w *wrapperWithCustomLocker) WrapperTypeFilterNoFP() {
	w.Acquire()
	defer w.Release()

	w.cl.Lock()
	w.cl.Unlock()
}
