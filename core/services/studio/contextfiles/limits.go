package contextfiles

import (
	"fmt"
	"strings"
)

// OverCapError means a write would make a file larger than its cap. Current
// holds what the file has now, so the writer can show it to the model, which
// must merge the entries instead of adding to them.
type OverCapError struct {
	What       string // "index" or "profile"
	Lines, Len int    // size of the refused text
	MaxLines   int    // 0: no line limit
	MaxBytes   int
	Current    string
}

func (e *OverCapError) Error() string {
	limit := fmt.Sprintf("%d bytes", e.MaxBytes)
	if e.MaxLines > 0 {
		limit = fmt.Sprintf("%d lines and %s", e.MaxLines, limit)
	}
	return fmt.Sprintf("the %s would be too long (%d lines, %d bytes; the limit is %s): merge or drop entries, then save the whole %s again. Current entries:\n%s",
		e.What, e.Lines, e.Len, limit, e.What, e.Current)
}

func countLines(b []byte) int {
	n := 0
	for line := range strings.SplitSeq(string(b), "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}

// CheckIndex checks a new index of the source against its caps. Over a cap it
// returns an *OverCapError that carries the current index.
func CheckIndex(src Source, current, next []byte) error {
	if lines := countLines(next); lines > MaxIndexLines || len(next) > src.limit() {
		return &OverCapError{What: "index", Lines: lines, Len: len(next), MaxLines: MaxIndexLines, MaxBytes: src.limit(), Current: string(current)}
	}
	return nil
}

// CheckProfile checks a new agent profile against MaxProfileBytes.
func CheckProfile(current, next []byte) error {
	if len(next) > MaxProfileBytes {
		return &OverCapError{What: "profile", Lines: countLines(next), Len: len(next), MaxBytes: MaxProfileBytes, Current: string(current)}
	}
	return nil
}
