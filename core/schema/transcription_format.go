package schema

import (
	"fmt"
	"strings"
	"time"
)

func TranscriptionResponse(tr *TranscriptionResult, resFmt TranscriptionResponseFormatType) string {
	var out string
	if resFmt == TranscriptionResponseFormatLrc {
		out = "[by:LocalAI]\n[re:LocalAI]\n"
	} else if resFmt == TranscriptionResponseFormatVtt {
		out = "WEBVTT"
	}

	for i, s := range tr.Segments {
		switch resFmt {
		case TranscriptionResponseFormatLrc:
			m := s.Start.Milliseconds()
			out += fmt.Sprintf("\n[%02d:%02d:%02d] %s", m/60000, (m/1000)%60, (m%1000)/10, strings.TrimSpace(s.Text))
		case TranscriptionResponseFormatSrt:
			out += fmt.Sprintf("\n\n%d\n%s --> %s\n%s", i+1, durationStr(s.Start, ','), durationStr(s.End, ','), strings.TrimSpace(s.Text))
		case TranscriptionResponseFormatVtt:
			out += fmt.Sprintf("\n\n%s --> %s\n%s\n", durationStr(s.Start, '.'), durationStr(s.End, '.'), strings.TrimSpace(s.Text))
		case TranscriptionResponseFormatText:
			fallthrough
		default:
			out += fmt.Sprintf("\n%s", strings.TrimSpace(s.Text))
		}
	}

	return out
}

func durationStr(d time.Duration, millisSeparator rune) string {
	m := d.Milliseconds()
	return fmt.Sprintf("%02d:%02d:%02d%c%03d", m/3600000, m/60000, int(d.Seconds())%60, millisSeparator, m%1000)
}
