// SPDX-License-Identifier: MIT
package launcher

import (
	"bufio"
	"io"
	"regexp"
	"strings"
)

const maxLogLine = 16 * 1024
const maxLogFile = 1024 * 1024
const maxLogBuffer = 100000
const omittedLog = "[unsafe or incomplete log line omitted]"

var sensitiveLog = regexp.MustCompile(`(?i)authorization|bearer\s|(?:token|password|secret|nats_jwt|user_seed)["']?\s*[=:]|[a-z][a-z0-9+.-]*://\S*[@?#]|eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+|S[UA][A-Z2-7]{40,}`)

type logFilter struct{ secrets []string }

func newLogFilter(secrets []string) logFilter { return logFilter{append([]string(nil), secrets...)} }
func (f logFilter) clean(line string) string {
	if controls(line) || sensitiveLog.MatchString(line) {
		return omittedLog
	}
	for _, s := range f.secrets {
		if s != "" && strings.Contains(line, s) {
			return omittedLog
		}
	}
	return line
}

// ReadSlice bounds memory and holds fragments until the newline. Overlong and
// unterminated records are discarded, never emitted as independently safe pieces.
func (f logFilter) consume(r io.Reader, emit func(string)) {
	b := bufio.NewReaderSize(r, maxLogLine)
	dropping := false
	for {
		line, e := b.ReadSlice('\n')
		if e == bufio.ErrBufferFull {
			dropping = true
			continue
		}
		if e != nil {
			if len(line) > 0 || dropping {
				emit(omittedLog)
			}
			return
		}
		if dropping {
			emit(omittedLog)
			dropping = false
			continue
		}
		emit(f.clean(strings.TrimSuffix(strings.TrimSuffix(string(line), "\n"), "\r")))
	}
}
