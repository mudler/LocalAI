package preview

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"io"

	"github.com/mudler/LocalAI/core/schema"
)

// readDelimited reads a CSV or TSV file as one sheet named after the file. It
// is lenient: a stray quote is kept as text and rows may have any length. It
// reads to the end to count the rows; the caller caps the file's size.
func readDelimited(ctx context.Context, r io.Reader, name string, sep rune, l Limits) (schema.ChatsFilePreviewSheet, error) {
	s := schema.ChatsFilePreviewSheet{Name: name, Rows: [][]string{}}
	br := bufio.NewReader(r)
	// A byte order mark is not part of the first header.
	if b, err := br.Peek(3); err == nil && string(b) == "\xef\xbb\xbf" {
		_, _ = br.Discard(3)
	}
	cr := csv.NewReader(br)
	cr.Comma = sep
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = true
	for {
		if s.TotalRows%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return s, err
			}
		}
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			return s, nil
		}
		if err != nil {
			return s, ErrInvalid
		}
		s.TotalRows++
		s.TotalCols = max(s.TotalCols, len(rec))
		if s.TotalRows <= l.Rows {
			row := make([]string, min(len(rec), l.Cols))
			copy(row, rec)
			s.Rows = append(s.Rows, row)
		}
	}
}
