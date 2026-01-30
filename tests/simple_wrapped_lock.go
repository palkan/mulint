package tests

import (
	"sync"
)

type wrapper struct {
	m sync.Mutex

	count int
}

func (w *wrapper) Acquire() {
	w.m.Lock()
}

func (w *wrapper) Release() {
	w.m.Unlock()
}

func (w *wrapper) Test() {
	w.Acquire()
	defer w.Release()

	if w.count > 0 {
		w.Acquire() // want "Mutex lock is acquired on this line"
		w.count = 0
		w.Release()
	}

}

func (w *wrapper) TestNoErrors() {
	w.doSomeWork()
	w.doMoreWork()
}

func (w *wrapper) doSomeWork() {
	w.m.Lock()
	defer w.m.Unlock()

	w.count = 1
}

func (w *wrapper) doMoreWork() {
	w.m.Lock()
	defer w.m.Unlock()

	w.count = 2
}
