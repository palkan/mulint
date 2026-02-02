package mulint

import (
	"bufio"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// relativePath returns the path relative to the current working directory.
// Falls back to the original path if relative path cannot be computed.
func relativePath(filename string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return filename
	}
	rel, err := filepath.Rel(cwd, filename)
	if err != nil {
		return filename
	}
	return rel
}

type LintError struct {
	origin        Location
	secondLock    Location
	originWrapper *WrapperInfo // non-nil if origin lock was via wrapper
}

func NewLintError(origin Location, secondLock Location) LintError {
	return LintError{
		origin:        origin,
		secondLock:    secondLock,
		originWrapper: nil,
	}
}

func NewLintErrorWithWrapper(origin Location, secondLock Location, wrapper *WrapperInfo) LintError {
	return LintError{
		origin:        origin,
		secondLock:    secondLock,
		originWrapper: wrapper,
	}
}

func (le LintError) Origin() Location {
	return le.origin
}

func (le LintError) SecondLock() Location {
	return le.secondLock
}

func (le LintError) Report(pass *analysis.Pass) {
	secondLockPosition := pass.Fset.Position(le.secondLock.pos)
	secondLockLine := le.GetLine(pass, secondLockPosition)
	originLockPosition := pass.Fset.Position(le.origin.pos)
	originLine := le.GetLine(pass, originLockPosition)

	// Add wrapper info if the origin lock was via a wrapper
	originSuffix := ""
	if le.originWrapper != nil {
		wrapperLockPosition := pass.Fset.Position(le.originWrapper.LockPos)
		originSuffix = fmt.Sprintf(" (via %s at %s:%d)",
			le.originWrapper.FQN.ShortName(),
			relativePath(wrapperLockPosition.Filename),
			wrapperLockPosition.Line,
		)
	}

	pass.Reportf(le.secondLock.Pos(),
		"Mutex lock is acquired on this line: %s\n\t%s:%d: But the same lock was acquired here: %s%s\n",
		strings.TrimSpace(secondLockLine),
		relativePath(originLockPosition.Filename),
		originLockPosition.Line,
		strings.TrimSpace(originLine),
		originSuffix,
	)
}

func (le LintError) GetLine(pass *analysis.Pass, position token.Position) string {
	lines := le.readfile(position.Filename)

	return lines[position.Line-1]
}

func (le LintError) readfile(filename string) []string {
	var f, err = os.Open(filename)
	if err != nil {
		return nil
	}

	var lines []string
	var scanner = bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

type Location struct {
	pos token.Pos
}

func NewLocation(pos token.Pos) Location {
	return Location{
		pos: pos,
	}
}

func (l Location) Pos() token.Pos {
	return l.pos
}

// MissingUnlockError reports a return statement without releasing a held lock.
type MissingUnlockError struct {
	lockPos   Location
	returnPos Location
	wrapper   *WrapperInfo // non-nil if the lock was acquired via wrapper
}

func NewMissingUnlockError(lockPos, returnPos Location) MissingUnlockError {
	return MissingUnlockError{
		lockPos:   lockPos,
		returnPos: returnPos,
		wrapper:   nil,
	}
}

func NewMissingUnlockErrorWithWrapper(lockPos, returnPos Location, wrapper *WrapperInfo) MissingUnlockError {
	return MissingUnlockError{
		lockPos:   lockPos,
		returnPos: returnPos,
		wrapper:   wrapper,
	}
}

func (e MissingUnlockError) Report(pass *analysis.Pass) {
	lockPosition := pass.Fset.Position(e.lockPos.pos)
	lockLine := e.GetLine(pass, lockPosition)

	// Add wrapper info if the lock was via a wrapper
	lockSuffix := ""
	if e.wrapper != nil {
		wrapperLockPosition := pass.Fset.Position(e.wrapper.LockPos)
		lockSuffix = fmt.Sprintf(" (via %s at %s:%d)",
			e.wrapper.FQN.ShortName(),
			relativePath(wrapperLockPosition.Filename),
			wrapperLockPosition.Line,
		)
	}

	pass.Reportf(e.returnPos.Pos(),
		"Mutex lock must be released before this line\n\t%s:%d: Lock was acquired here: %s%s\n",
		relativePath(lockPosition.Filename),
		lockPosition.Line,
		strings.TrimSpace(lockLine),
		lockSuffix,
	)
}

func (e MissingUnlockError) GetLine(pass *analysis.Pass, position token.Position) string {
	lines := e.readfile(position.Filename)
	if position.Line > len(lines) {
		return ""
	}
	return lines[position.Line-1]
}

func (e MissingUnlockError) readfile(filename string) []string {
	var f, err = os.Open(filename)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	var scanner = bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}
