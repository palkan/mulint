package tests

import "sync"

type condStore struct {
	m    sync.Mutex
	data map[string]string
}

func (cs *condStore) putMixed(task string, hasLock bool) {
	if !hasLock {
		cs.m.Lock()
	}
	cs.data[task] = "guarded"
	if !hasLock {
		cs.m.Unlock()
	}

	cs.m.Lock() // unguarded acquisition — runs regardless of hasLock
	cs.data[task] = "always"
	cs.m.Unlock()
}

func (cs *condStore) PutMixedLocked(task string) {
	cs.m.Lock()
	defer cs.m.Unlock()

	cs.putMixed(task, true) // want "Mutex lock is acquired on this line"
}

func (cs *condStore) putReassigned(task string, hasLock bool) {
	if task == "force" {
		hasLock = false
	}
	if !hasLock {
		cs.m.Lock()
	}
	cs.data[task] = "done"
	if !hasLock {
		cs.m.Unlock()
	}
}

func (cs *condStore) PutReassignedLocked(task string) {
	cs.m.Lock()
	defer cs.m.Unlock()

	cs.putReassigned(task, true) // want "Mutex lock is acquired on this line"
}

func (cs *condStore) putSimple(task string, hasLock bool) {
	if !hasLock {
		cs.m.Lock()
	}
	cs.data[task] = "done"
	if !hasLock {
		cs.m.Unlock()
	}
}

func (cs *condStore) putNested(task string, fullCheck, hasLock bool) {
	if fullCheck && task != "" {
		if !hasLock {
			cs.m.Lock()
		}
		cs.data[task] = "done"
		if !hasLock {
			cs.m.Unlock()
		}
	}
}

func (cs *condStore) PutLocked(task string) {
	cs.m.Lock()
	defer cs.m.Unlock()

	cs.putSimple(task, true) // recognized: no diagnostic

	cs.putNested(task, true, true) // recognized: no diagnostic
}

func (cs *condStore) putGuardedPair(task string, lock bool) {
	if lock {
		cs.m.Lock()
	}
	cs.data[task] = "done"
	if lock {
		cs.m.Unlock()
	}
}

func (cs *condStore) CallGuardedPair(task string) int {
	cs.putGuardedPair(task, true) // self-contained: no diagnostic
	return len(cs.data)
}

func (cs *condStore) GuardedPairDoubleLock(task string) {
	cs.m.Lock()
	defer cs.m.Unlock()

	cs.putGuardedPair(task, true) // want "Mutex lock is acquired on this line"
}

func (cs *condStore) lockIf(b bool) {
	if b {
		cs.m.Lock()
	}
}

func (cs *condStore) UseLockIf(task string) int {
	cs.lockIf(true)
	cs.data[task] = "x"
	return len(cs.data) // want "Mutex lock must be released before this line"
}

func (cs *condStore) putDeep(task string, fullCheck, hasLock bool) {
	for i := 0; i < 2; i++ {
		if fullCheck {
			if !hasLock {
				cs.m.Lock()
			}
			cs.data[task] = "deep"
			if !hasLock {
				cs.m.Unlock()
			}
		}
	}
}

func (cs *condStore) PutDeepLocked(task string) {
	cs.m.Lock()
	defer cs.m.Unlock()

	cs.putDeep(task, true, true) // recognized: no diagnostic
}
