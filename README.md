# mulint

A Go linter that detects mutex misuses (or _self-deadlocks_) such as reentrant lock and missing Unlock()-s.

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
> If you found a false positive, please, [open an issue](https://github.com/palkan/mulint/issues)!

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

- Locks without unlock (potential self-deadlock)

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

- Missing unlocks on return.

  The linter detects return statements in branches that exit while a mutex is still held:

  ```go
  func (s *Service) Process(task string) error {
      s.mu.Lock()
  
      if _, ok := s.cache[task]; ok {
          s.mu.Unlock()
          return nil // OK: unlocked before return
      }
  
      result, err := s.doWork(task)
      if err != nil {
          return err // ERROR: mutex still held
      }
  
      s.cache[task] = result
      s.mu.Unlock()
      return nil
  }
  ```

- Deferred re-locks that run while the lock is still held (defers run LIFO):

  ```go
  func (s *Service) Process() {
      s.mu.Lock()
      defer s.mu.Unlock() // registered first — runs LAST

      defer func() {
          s.mu.Lock() // ERROR: runs before the deferred Unlock above
          s.stats++
          s.mu.Unlock()
      }()
  }
  ```

  ...while a deferred re-lock after a manual release is fine:

  ```go
  s.mu.Lock()
  defer func() {
      s.mu.Lock() // OK: runs at exit, after the Unlock below
      s.saveWriteError()
      s.mu.Unlock()
  }()
  // ...
  s.mu.Unlock()
  ```

- Conditional locks guarded by a bool parameter are tracked (at any nesting depth):

  ```go
  func (s *Service) update(hasLock bool) {
      if !hasLock {
          s.mu.Lock()
      }
      // ...
      if !hasLock {
          s.mu.Unlock()
      }
  }

  s.mu.Lock()
  s.update(true)  // OK: no lock is taken with hasLock=true
  s.update(false) // ERROR: recursive lock
  ```

- Guarded defers (defer safety + early release):

  ```go
  s.mu.Lock()
  unlocked := false
  defer func() {
      if !unlocked {
          s.mu.Unlock()
      }
  }()

  if s.cached() {
      return nil // OK: covered by the deferred unlock
  }

  s.mu.Unlock()
  unlocked = true
  ```

- Local unlock closures on error paths:

  ```go
  s.mu.Lock()
  cleanup := func() {
      s.dirty = false
      s.mu.Unlock()
  }

  if err != nil {
      cleanup()
      return err // OK: released by the closure
  }
  ```

- Other recognized-safe shapes: a lock released on both branches of an
  if/else, `Acquire`/`Release`-style wrapper methods, and locks on distinct
  objects that share a field name (`c.mu` vs `c.conn.mu`).

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

## Known False Positives

Patterns mulint cannot prove safe yet — expect a report and judge for
yourself:

- Cross-function lock ownership. Acquiring in one function and releasing in another can be reported as a missing unlock
- Conditional locks keyed on struct fields, such as `if !p.nodeScope { p.mu.Lock() }`
- Closure cascades.

Found another? Please [open an issue](https://github.com/palkan/mulint/issues)!

## Limitations

- Analysis is performed per package; cross-package recursive locks are not detected
- Mutexes passed as function arguments are not tracked
- Dynamic dispatch (interface method calls) is not analyzed

## License

MIT

## Acknowledgments

- This project has started as a fork of [mulint](https://github.com/gnieto/mulint) by @gnieto.
