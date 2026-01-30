package tests

import (
	"fmt"
	"sync"
)

type another struct {
	m sync.RWMutex
}

func (a *another) Test() {
	a.m.RLock()
	defer a.m.RUnlock()

	a.m.Lock() // want "Mutex lock is acquired on this line"
	a.m.Unlock()
}

func (a *another) TestWithSwitch(val int) string {
	switch val {
	case 1:
		a.m.RLock()
		defer a.m.RUnlock()

		return "uno"
	case 2:
		a.m.RLock()
		defer a.m.RUnlock()

		return "due"
	}

	return ""
}

func (a *another) isGood() bool {
	a.m.RLock()
	defer a.m.RUnlock()

	return true
}

func (a *another) TestExpression() {
	a.m.RLock()
	v := a.isGood() // want "Mutex lock is acquired on this line"
	fmt.Println(v)
	a.m.RUnlock()
}

func (a *another) TestIf() {
	a.m.RLock()
	if a.isGood() { // want "Mutex lock is acquired on this line"
		return
	}
	a.m.RUnlock()
}

func (a *another) TestRoutine() {
	a.m.RLock()

	res := make(chan bool)

	go func() {
		res <- a.isGood()
	}()

	a.m.RUnlock()

	<-res
}
