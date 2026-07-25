package tests

import (
	"sync"
)

type ignored struct {
	m sync.Mutex
}

// Trailing //mulint:ignore on the reported line suppresses the diagnostic.
func (i *ignored) TrailingIgnore() {
	i.m.Lock()
	defer i.m.Unlock()

	i.m.Lock() //mulint:ignore
	i.m.Unlock()
}

// A standalone //mulint:ignore suppresses the next line.
func (i *ignored) StandaloneIgnore() {
	i.m.Lock()
	defer i.m.Unlock()

	//mulint:ignore
	i.m.Lock()
	i.m.Unlock()
}

// //nolint:mulint is honored outside of golangci-lint too.
func (i *ignored) NolintMulint() {
	i.m.Lock()
	defer i.m.Unlock()

	i.m.Lock() //nolint:mulint
	i.m.Unlock()
}

// //nolint with a list containing mulint is honored.
func (i *ignored) NolintList() {
	i.m.Lock()
	defer i.m.Unlock()

	i.m.Lock() //nolint:errcheck,mulint
	i.m.Unlock()
}

// Bare //nolint suppresses everything on the line.
func (i *ignored) NolintBare() {
	i.m.Lock()
	defer i.m.Unlock()

	i.m.Lock() //nolint
	i.m.Unlock()
}

// A directive on the origin lock line suppresses diagnostics anchored there.
func (i *ignored) OriginIgnore() {
	i.m.Lock() //mulint:ignore
	defer i.m.Unlock()

	i.m.Lock()
	i.m.Unlock()
}

// Missing-unlock diagnostics are suppressed on the return line.
func (i *ignored) MissingUnlockIgnore(val bool) {
	i.m.Lock()

	if val {
		return //mulint:ignore
	}

	i.m.Unlock()
}

// Violations without a directive are still reported.
func (i *ignored) NotIgnored() {
	i.m.Lock()
	defer i.m.Unlock()

	i.m.Lock() // want "Mutex lock is acquired on this line"
	i.m.Unlock()
}

// A nolint list that does not include mulint does not suppress diagnostics.
func (i *ignored) OtherLinterNolint() {
	i.m.Lock() //nolint:errcheck
	defer i.m.Unlock()

	i.m.Lock() // want "Mutex lock is acquired on this line"
	i.m.Unlock()
}
