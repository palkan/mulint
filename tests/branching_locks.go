package tests

import (
	"fmt"
	"sync"
)

type branch struct {
	m sync.Mutex

	nested *branch

	data map[string]string
}

func (b *branch) Work(task string) (int, error) {
	return 0, nil
}

func (b *branch) WorkHard(task string) {
	b.m.Lock()

	if _, ok := b.data[task]; ok {
		b.m.Unlock()
		return
	}

	res, err := b.Work(task)

	if err != nil {
		if res < 0 {
			return // want "Mutex lock must be released before this line"
		}
	} else {
		b.data["error"] = "none"
	}

	b.m.Unlock()

	b.doWork(task)
}

func (b *branch) WorkWithCase(task string) {
	if _, ok := b.data[task]; ok {
		b.dispatchEvent("dup")
		return
	}

	switch task {
	case "run":
		b.dispatchEvent("run")
	case "walk":
		b.dispatchEvent("walk")
	case "lock":
		b.m.Lock()
		b.dispatchEvent("lock") // want "Mutex lock is acquired on this line"
	case "lock2":
		b.m.Lock()
		b.dispatchEvent("lock2") // want "Mutex lock is acquired on this line"
	}
}

func (b *branch) WorkWithIndependentBranches(task string) {
	if _, ok := b.data[task]; ok {
		b.m.Lock()
		defer b.m.Unlock()

		b.data["one"] = "1"
	} else {
		b.dispatchEvent("new")
	}

	b.dispatchEvent("out")

	if b.data["one"] == "2" {
		if b.data["two"] == "1" {
			b.m.Lock()
			b.data["three"] = "3"
		} else {
			b.m.Lock()
			b.data["three"] = "4"
		}

		b.m.Unlock()
	} else {
		b.m.Lock()
		b.data["four"] = "3"
		b.m.Unlock()
	}
}

func (b *branch) WorkHardWithWrappers(task string) {
	b.Acqure()

	if _, ok := b.data[task]; ok {
		b.Release()
		return
	}

	res, err := b.Work(task)

	if err != nil {
		if res < 0 {
			return // want "Mutex lock must be released before this line"
		}
	} else {
		b.data["error"] = "none"
	}

	b.Release()

	b.doWork(task)
}

func (b *branch) doWork(task string) {
	b.m.Lock()
	defer b.m.Unlock()

	b.data[task] = "done"
}

func (b *branch) dispatchEvent(name string) {
	b.m.Lock()
	defer func() {
		b.m.Unlock()
		err := recover()
		if err != nil {
			fmt.Printf("Event handler panicked while: %v", err)
		}
	}()
}

func (b *branch) Acqure() {
	b.m.Lock()
}

func (b *branch) Release() {
	b.m.Unlock()
}
