# mulint

A Go linter that detects potential deadlocks caused by mutexes.

## Installation

```bash
go install github.com/palkan/mulint@latest
```

## Usage

```bash
$ mulint ./...

service.go:45: Mutex lock is acquired on this line: s.helper()
  service.go:42: But the same lock was acquired here: s.mu.RLock()
```

The tool uses `golang.org/x/tools/go/analysis`, so standard Go package patterns work.

## What It Detects

> [!NOTE]
> If you found a false negative, please, [open an issue](https://github.com/palkan/mulint/issues)!

### Recursive locks

Examples:

- Direct recursive locks:

  ```go
  func (s *Service) Process() {
      s.mu.Lock()
      defer s.mu.Unlock()
  
      s.mu.Lock() // ERROR: recursive lock
      s.mu.Unlock()
  }
  ```

- Transitive recursive locks:

  ```go
  func (s *Service) Process() {
      s.mu.Lock()
      defer s.mu.Unlock()
  
      s.helper() // ERROR: helper() also locks s.mu
  }
  
  func (s *Service) helper() {
      s.mu.Lock()
      defer s.mu.Unlock()
      // ...
  }
	```

- Locks without unlock (potential deadlock source)

  ```go
  func (s *Service) Process() {
      s.mu.RLock()
      defer s.mu.RUnlock()
  
      s.leaky() // ERROR: leaky() locks but never unlocks
  }
  
  func (s *Service) leaky() {
      s.mu.RLock()
      // Missing RUnlock!
  }
  ```

- Recursive `RLock()` (see below):

  ```go
  func (s *Service) Fetch() {
      s.mu.RLock()
      defer s.mu.RUnlock()
  
      s.helper() // ERROR: helper() also locks s.mu
  }
  
  func (s *Service) helper() {
      s.mu.RLock()
      defer s.mu.RUnlock()
      // ...
  }
	```

#### Why recursive `RLock()`?

Go's `sync.RWMutex` documentation states:

> If a goroutine holds a RWMutex for reading and another goroutine might call Lock, no goroutine should expect to be able to acquire a read lock until the initial read lock is released. In particular, this prohibits recursive read locking.

This isn't enforced by the compiler or runtime, making it easy to accidentally introduce deadlocks.

Read also: [What could Go wrong with a mutex, or the Go profiling story](https://evilmartians.com/chronicles/what-could-go-wrong-with-a-mutex-or-the-go-profiling-story).

## Limitations

- Analysis is performed per package; cross-package recursive locks are not detected
- Mutexes passed as function arguments are not tracked
- Dynamic dispatch (interface method calls) is not analyzed

## License

MIT

## Acknowledgments

- This project has started as a fork of [mulint](https://github.com/gnieto/mulint) by @gnieto.
