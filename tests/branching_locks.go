package tests

import (
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

func (b *branch) Acqure() {
	b.m.Lock()
}

func (b *branch) Release() {
	b.m.Unlock()
}
