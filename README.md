# mulint

A Go linter that detects potential deadlocks caused by recursive mutex locks.

## Why?

Go's `sync.RWMutex` documentation states:

> If a goroutine holds a RWMutex for reading and another goroutine might call Lock, no goroutine should expect to be able to acquire a read lock until the initial read lock is released. In particular, this prohibits recursive read locking.

This isn't enforced by the compiler or runtime, making it easy to accidentally introduce deadlocks. `mulint` statically analyzes your code to detect these issues before they cause problems in production.

## Installation

```bash
go install github.com/palkan/mulint@latest
```

## Usage

```bash
mulint ./...
```

The tool uses `golang.org/x/tools/go/analysis`, so standard Go package patterns work.

### Exit Codes

- `0` - No issues found
- `1` - Potential deadlocks detected

## What It Detects

### Direct recursive locks

```go
func (s *Service) Process() {
    s.mu.RLock()
    defer s.mu.RUnlock()

    s.mu.RLock() // ERROR: recursive lock
    s.mu.RUnlock()
}
```

### Transitive recursive locks

```go
func (s *Service) Process() {
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

### Deep call chains

```go
func (s *Service) A() {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.B() // ERROR: B -> C -> locks s.mu
}

func (s *Service) B() { s.C() }

func (s *Service) C() {
    s.mu.Lock() // Called while A() holds the lock
    s.mu.Unlock()
}
```

### Lock/unlock wrapper methods

```go
func (s *Service) Lock()   { s.mu.Lock() }
func (s *Service) Unlock() { s.mu.Unlock() }

func (s *Service) Process() {
    s.Lock()
    defer s.Unlock()

    s.Lock() // ERROR: detected through wrapper
}
```

### Locks without unlock (potential deadlock source)

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

## What It Doesn't Flag

### Different struct instances

```go
func (s *Service) Process() {
    s.mu.Lock()
    defer s.mu.Unlock()

    other := &Service{}
    other.DoWork() // OK: different instance
}
```

### Lock released before nested call

```go
func (s *Service) Process() {
    s.mu.RLock()
    data := s.data
    s.mu.RUnlock()

    s.helper() // OK: lock was released
}
```

## Example Output

```
service.go:45: Mutex lock is acquired on this line: s.helper()
    service.go:42: But the same lock was acquired here: s.mu.RLock()
```

## Limitations

- Analysis is performed per package; cross-package recursive locks are not detected
- Mutexes passed as function arguments are not tracked
- Dynamic dispatch (interface method calls) is not analyzed

## License

MIT
