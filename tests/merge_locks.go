package tests

import "sync"

// Shapes distilled from tailscale util/singleflight DoChanContext: a lock
// released on every branch of an if/else is released after the merge point.

type merge struct {
	m    sync.Mutex
	data map[string]string
}

func (b *merge) BothBranchesUnlock(task string) int {
	b.m.Lock()
	if _, ok := b.data[task]; ok {
		b.data[task] = "dup"
		b.m.Unlock()
	} else {
		b.data[task] = "new"
		b.m.Unlock()
	}
	return len(b.data) // no diagnostic: released on both branches
}

func (b *merge) ReturningBranchDoesNotConstrain(task string) int {
	b.m.Lock()
	if _, ok := b.data[task]; ok {
		b.m.Unlock()
		return 0
	} else {
		b.m.Unlock()
	}
	return 1 // no diagnostic: fall-through only via else, which released
}

func (b *merge) OneBranchLeaks(task string) int {
	b.m.Lock()
	if _, ok := b.data[task]; ok {
		b.m.Unlock()
	} else {
		b.data[task] = "leak"
	}
	return len(b.data) // want "Mutex lock must be released before this line"
}

func (b *merge) NoElseKeepsLock(task string) int {
	b.m.Lock()
	if _, ok := b.data[task]; ok {
		b.m.Unlock()
	}
	return len(b.data) // want "Mutex lock must be released before this line"
}
