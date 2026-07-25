package mulint

import (
	"bufio"
	"go/token"
	"os"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// ignoreSet records source lines on which mulint diagnostics are suppressed,
// keyed by filename and line number.
type ignoreSet map[string]map[int]bool

// Ignored reports whether diagnostics at the given position are suppressed.
func (s ignoreSet) Ignored(fset *token.FileSet, pos token.Pos) bool {
	position := fset.Position(pos)
	return s[position.Filename][position.Line]
}

func (s ignoreSet) add(filename string, line int) {
	lines := s[filename]
	if lines == nil {
		lines = make(map[int]bool)
		s[filename] = lines
	}
	lines[line] = true
}

// buildIgnoreSet scans the comments of all files in the pass for suppression
// directives: //mulint:ignore, //nolint and //nolint:<list> where the list
// contains "mulint". A directive suppresses diagnostics on its own line; a
// directive on a line of its own also suppresses the line that follows it.
func buildIgnoreSet(pass *analysis.Pass) ignoreSet {
	ignores := make(ignoreSet)
	fileLines := make(map[string][]string)

	for _, file := range pass.Files {
		for _, group := range file.Comments {
			for _, comment := range group.List {
				if !isIgnoreDirective(comment.Text) {
					continue
				}

				start := pass.Fset.Position(comment.Pos())
				ignores.add(start.Filename, start.Line)

				if isStandalone(fileLines, start) {
					end := pass.Fset.Position(comment.End())
					ignores.add(end.Filename, end.Line+1)
				}
			}
		}
	}

	return ignores
}

// isStandalone reports whether the comment starting at position is the first
// non-whitespace text on its line.
func isStandalone(cache map[string][]string, position token.Position) bool {
	lines, ok := cache[position.Filename]
	if !ok {
		lines = readLines(position.Filename)
		cache[position.Filename] = lines
	}

	if position.Line > len(lines) {
		return false
	}

	prefix := lines[position.Line-1][:position.Column-1]
	return strings.TrimSpace(prefix) == ""
}

// isIgnoreDirective reports whether the comment text is a suppression
// directive recognized by mulint.
func isIgnoreDirective(text string) bool {
	text = strings.TrimPrefix(text, "//")
	text = strings.TrimPrefix(text, "/*")
	text = strings.TrimSpace(text)

	if strings.HasPrefix(text, "mulint:ignore") {
		return true
	}

	if text == "nolint" || strings.HasPrefix(text, "nolint ") {
		return true
	}

	if rest, ok := strings.CutPrefix(text, "nolint:"); ok {
		list, _, _ := strings.Cut(rest, " ")
		for _, name := range strings.Split(list, ",") {
			if strings.TrimSpace(name) == "mulint" {
				return true
			}
		}
	}

	return false
}

func readLines(filename string) []string {
	f, err := os.Open(filename)
	if err != nil {
		return nil
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}
