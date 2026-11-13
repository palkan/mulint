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

### Running via go vet (cached, incremental)

The `mulint-vet` binary is a [unitchecker](https://pkg.go.dev/golang.org/x/tools/go/analysis/unitchecker) variant meant to be driven by `go vet`:

```bash
go install github.com/palkan/mulint/cmd/mulint-vet@latest

go vet -vettool=$(which mulint-vet) ./...
```

Results are the same as with the standalone binary, but `go vet` caches per-package analysis results in the Go build cache, so repeated runs are near-instant and incremental runs only re-analyze changed packages and their dependents. Prefer this mode for large projects and CI.

### GitHub Action

We provide a GitHub Action, so you can quickly drop the mulint check to your workflow:

```yaml
steps:
  - uses: actions/checkout@v4
  - uses: actions/setup-go@v6
    with:
      go-version-file: go.mod
  - uses: palkan/mulint@v1.1.0
```

**NOTE:** `setup-go` is required (or any other form of installing Go).

Inputs (and their defaults):

```yaml
  - uses: palkan/mulint@v1.1.0
    with:
      args: ./... # package patterns and flags
      working-directory: . # where to run
      annotations: 'false' # PR annotations via problem matcher
```

### golangci-lint plugin

mulint can be included in a custom [golangci-lint](https://golangci-lint.run) binary as a [module plugin](https://golangci-lint.run/docs/plugins/module-plugins/).

Create `.custom-gcl.yml` next to your `.golangci.yml`:

```yaml
version: v2.11.2 # golangci-lint version to build with
plugins:
  - module: 'github.com/palkan/mulint'
    import: 'github.com/palkan/mulint/gclplugin'
    version: latest
```

Build the custom binary (requires Go and git):

```bash
golangci-lint custom # produces ./custom-gcl
```

Then enable mulint in `.golangci.yml` and use `custom-gcl` instead of `golangci-lint`:

```yaml
version: "2"
linters:
  enable:
    - mulint
  settings:
    custom:
      mulint:
        type: module
        description: Detects mutex misuses (reentrant locks, missing unlocks)
        original-url: github.com/palkan/mulint
```

### Suppressing violations

To ignore a specific violation (e.g., a false positive), annotate the reported line
with a `//mulint:ignore` comment, either trailing or on the line right above:

```go
mu.Lock() //mulint:ignore

//mulint:ignore
mu.Lock()
```

The directive could also be put on the line of the origin lock (the one shown in
the report as "the same lock was acquired here" / "lock was acquired here").

You can also use `//nolint` and `//nolint:mulint` (or any nolint list containing `mulint`). (Not only with golangci-lint but with any other mode.)

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
In the meantime, silence it with a [`//mulint:ignore` directive](#suppressing-violations).

## Limitations

- Analysis is performed per package; cross-package recursive locks are not detected
- Mutexes passed as function arguments are not tracked
- Only locks on the method receiver are tracked across calls. A lock on another struct's mutex (for example, a `*Session` parameter of a `Node` method) is not followed into callees. This also applies to deferred calls in callees. Locking a mutex that belongs to another struct is a design smell: keep the lock and the code that needs it on the same type.
- Dynamic dispatch (interface method calls) is not analyzed

## License

MIT

## Acknowledgments

- This project has started as a fork of [mulint](https://github.com/gnieto/mulint) by @gnieto.
