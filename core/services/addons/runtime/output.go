package runtime

import (
	"bufio"
	"io"
	"strings"

	"github.com/mudler/xlog"
)

// maxOutputLine caps one logged line of an add-on. A program that writes
// without ever ending a line must not grow the owner's memory or flood its log.
const maxOutputLine = 4096

// outputCut marks a line that was cut at maxOutputLine.
const outputCut = "..."

// logOutput reads the standard output or error of an add-on to its end and
// hands every line to sink. The add-on's own log, such as the record of what a
// host command ran, would be lost otherwise: it only has these streams.
func logOutput(r io.Reader, id, stream string, sink func(id, stream, line string)) {
	br := bufio.NewReaderSize(r, 4096)
	for {
		// ReadLine returns long lines in pieces: keep the first maxOutputLine
		// bytes and drop the rest of that line.
		var line []byte
		cut := false
		for {
			part, more, err := br.ReadLine()
			if len(line) < maxOutputLine {
				room := maxOutputLine - len(line)
				if len(part) > room {
					part, cut = part[:room], true
				}
				line = append(line, part...)
			} else if len(part) > 0 {
				cut = true
			}
			if err != nil {
				if len(line) > 0 {
					emit(sink, id, stream, line, cut)
				}
				return
			}
			if !more {
				break
			}
		}
		emit(sink, id, stream, line, cut)
	}
}

func emit(sink func(id, stream, line string), id, stream string, line []byte, cut bool) {
	s := strings.ToValidUTF8(string(line), "?")
	if cut {
		s += outputCut
	}
	sink(id, stream, s)
}

// defaultOutput writes an add-on line to the LocalAI log, with the add-on ID
// as a prefix.
func defaultOutput(id, stream, line string) {
	if stream == "stderr" {
		xlog.Warn("[" + id + "] " + line)
		return
	}
	xlog.Info("[" + id + "] " + line)
}
