// SPDX-License-Identifier: MIT
package textinput

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/addons/office/internal/xlsx"
)

const maxSourceBytes = 1 << 20

// validXML rejects invalid UTF-8 and characters XML 1.0 cannot represent.
func validXML(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !(r == 9 || r == 10 || r == 13 || r >= 0x20 && r <= 0xd7ff || r >= 0xe000 && r <= 0xfffd || r >= 0x10000 && r <= 0x10ffff) {
			return false
		}
	}
	return true
}

// ParseCSV creates a single sheet of explicit string cells, never formulas.
// Validate the entire bounded grid before allocating workbook cells.
func ParseCSV(ctx context.Context, source, sheet, title string) (xlsx.Workbook, error) {
	fail := func(msg string) (xlsx.Workbook, error) { return xlsx.Workbook{}, errors.New("csv: " + msg) }
	if err := ctx.Err(); err != nil {
		return xlsx.Workbook{}, err
	}
	if len(source) > maxSourceBytes {
		return fail("input exceeds 1 MiB")
	}
	if !validXML(source) || !validXML(sheet) || !validXML(title) {
		return fail("invalid XML text in content or metadata")
	}
	if sheet == "" {
		sheet = "Sheet1"
	}
	if utf8.RuneCountInString(sheet) > 31 || strings.ContainsAny(sheet, `[]:*?/\`) || strings.HasPrefix(sheet, "'") || strings.HasSuffix(sheet, "'") {
		return fail("invalid sheet name: use at most 31 characters without []:*?/\\ or boundary apostrophes")
	}
	reader := csv.NewReader(strings.NewReader(source))
	var rows [][]string
	columns, nonempty := 0, 0
	for {
		if err := ctx.Err(); err != nil {
			return xlsx.Workbook{}, err
		}
		row, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail(fmt.Sprintf("invalid quoting or row width near record %d", len(rows)+1))
		}
		if columns == 0 {
			columns = len(row)
		}
		if len(rows) >= 5000 || columns > 200 || (len(rows)+1)*columns > 100000 {
			return fail("grid exceeds 5000 rows, 200 columns or 100000 cells")
		}
		for _, s := range row {
			if s != "" {
				nonempty++
			}
		}
		rows = append(rows, row)
	}
	if nonempty == 0 {
		return fail("input has no nonempty cells")
	}
	if err := ctx.Err(); err != nil {
		return xlsx.Workbook{}, err
	}
	cells := make([]xlsx.Cell, 0, nonempty)
	for r, row := range rows {
		if err := ctx.Err(); err != nil {
			return xlsx.Workbook{}, err
		}
		for c, value := range row {
			if value != "" {
				col := ""
				for n := c + 1; n > 0; n = (n - 1) / 26 {
					col = string(rune('A'+(n-1)%26)) + col
				}
				cells = append(cells, xlsx.Cell{Ref: fmt.Sprintf("%s%d", col, r+1), Value: xlsx.Str(value)})
			}
		}
	}
	return xlsx.Workbook{Title: title, Sheets: []xlsx.Sheet{{Name: sheet, Cells: cells}}}, nil
}
