package tests

import (
	"fmt"
	"sync"
)

type some struct {
	m sync.RWMutex

	sm map[string]int
	ms map[int]string
}

func lalala() {}

func (s *some) Entry() {
	s.m.RLock()
	defer s.m.RUnlock()

	s.sm["lalala"] = 2
	noneStructMethod()
	s.recursiveRLock() // want "Mutex lock is acquired on this line"
	s.deepLock()       // want "Mutex lock is acquired on this line"
}

func (s *some) ShouldNotDetectDeadLock() {
	s.m.RLock()
	noneStructMethod()
	s.m.Unlock()

	s.deepLock()
}

func (s *some) ShouldDetectDeadLockWithNoUnlock() {
	s.m.RLock()
	s.nonUnlockingMethod() // want "Mutex lock is acquired on this line"
	s.m.Unlock()
}

func (s *some) ShouldNotDetectAfterUnlock() {
	s.m.RLock()
	if s.sm["test"] > 0 {
		s.m.Unlock()
		s.recursiveRLock()
	}

	s.m.Unlock()
}

func (s some) test() {}

func (s *some) deepLock() {
	s.recursiveRLock()
}

func (s *some) recursiveRLock() {
	s.m.RLock()
	s.ms[24322] = "this is very bad!"
	s.m.RUnlock()
}

func (s *some) nonUnlockingMethod() {
	s.m.RLock()
	s.ms[323] = "where is Unlock()?"
}

func noneStructMethod() {
	fmt.Println("I'm not doing anything")
}

// Conditional lock tests - lock is guarded by bool parameter

func (s *some) ConditionalLockCaller() {
	s.m.Lock()
	defer s.m.Unlock()

	s.conditionalLockHelper(false) // Should NOT be flagged - lock param is false
}

func (s *some) conditionalLockHelper(lock bool) {
	if lock {
		s.m.Lock()
		defer s.m.Unlock()
	}
	s.sm["conditional"] = 1
}

func (s *some) ConditionalLockCallerWithTrue() {
	s.m.Lock()
	defer s.m.Unlock()

	s.conditionalLockHelper(true) // want "Mutex lock is acquired on this line"
}

func (s *some) NegatedConditionalLockCaller() {
	s.m.Lock()
	defer s.m.Unlock()

	s.negatedConditionalHelper(true) // Should NOT be flagged - !lock is false when lock is true
}

func (s *some) negatedConditionalHelper(lock bool) {
	if !lock {
		s.m.Lock()
		defer s.m.Unlock()
	}
	s.sm["negated"] = 1
}

func (s *some) NegatedConditionalCallerWithFalse() {
	s.m.Lock()
	defer s.m.Unlock()

	s.negatedConditionalHelper(false) // want "Mutex lock is acquired on this line"
}

// Propagated conditional lock tests - conditional lock through intermediate function

func (s *some) PropagatedConditionalLockCaller() {
	s.m.Lock()
	defer s.m.Unlock()

	s.intermediateHelper(false) // Should NOT be flagged - lock propagates as false
}

func (s *some) intermediateHelper(lock bool) {
	// This function passes the lock param through to conditionalLockHelper
	s.sm["intermediate"] = 1
	s.conditionalLockHelper(lock)
}

func (s *some) PropagatedConditionalLockCallerWithTrue() {
	s.m.Lock()
	defer s.m.Unlock()

	s.intermediateHelper(true) // want "Mutex lock is acquired on this line"
}
