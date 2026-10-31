package preview

import (
	"encoding/xml"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

// workbook reads the sheets of an .xlsx or .xlsm in their workbook order. A
// cell shows the value Excel stored: a formula shows its cached result.
func (p *pkg) workbook() (schema.ChatsFilePreview, error) {
	wbPath := p.mainPart("xl/workbook.xml")
	if !p.has(wbPath) {
		return schema.ChatsFilePreview{}, ErrInvalid
	}
	rels, err := p.rels(wbPath)
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	type sheetRef struct{ name, part string }
	var refs []sheetRef
	date1904 := false
	err = p.decode(wbPath, func(_ *xml.Decoder, se xml.StartElement) error {
		switch se.Name.Local {
		case "workbookPr":
			v := attr(se, "date1904")
			date1904 = v == "1" || v == "true"
		case "sheet":
			refs = append(refs, sheetRef{name: attr(se, "name"), part: rels[relID(se)]})
		}
		return nil
	})
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	dir := wbPath[:strings.LastIndex(wbPath, "/")+1]
	strs, err := p.sharedStrings(p.relOfType(rels, "sharedStrings", dir+"sharedStrings.xml"))
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	dates, err := p.dateStyles(p.relOfType(rels, "styles", dir+"styles.xml"))
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	out := schema.ChatsFilePreview{Kind: kindTable, Sheets: []schema.ChatsFilePreviewSheet{}}
	for _, ref := range refs {
		s := schema.ChatsFilePreviewSheet{Name: ref.name, Rows: [][]string{}}
		// A sheet whose part is missing (a chart sheet, a broken link) shows
		// empty rather than failing the whole workbook.
		if p.has(ref.part) {
			if s, err = p.sheet(ref.name, ref.part, strs, dates, date1904); err != nil {
				return schema.ChatsFilePreview{}, err
			}
		}
		out.Sheets = append(out.Sheets, s)
	}
	return out, nil
}

// mainPart is the main part the package's root relationships name, or def.
func (p *pkg) mainPart(def string) string {
	rels, err := p.rels("")
	if err != nil {
		return def
	}
	for _, target := range rels {
		if p.has(target) && (strings.HasSuffix(target, "workbook.xml") || strings.HasSuffix(target, "document.xml") || strings.HasSuffix(target, "presentation.xml")) && path0(target) == path0(def) {
			return target
		}
	}
	return def
}

// path0 is the first folder of a part name ("xl", "word", "ppt").
func path0(name string) string {
	if before, _, ok := strings.Cut(name, "/"); ok {
		return before
	}
	return name
}

// relOfType finds a related part by the end of its file name; a package
// without the relationship uses the conventional name def.
func (p *pkg) relOfType(rels map[string]string, base, def string) string {
	for _, target := range rels {
		if strings.EqualFold(strings.TrimSuffix(target[strings.LastIndex(target, "/")+1:], ".xml"), base) {
			return target
		}
	}
	return def
}

// sharedStrings reads the table of shared strings. Rich text is joined; the
// phonetic guides (<rPh>) are left out.
func (p *pkg) sharedStrings(name string) ([]string, error) {
	var out []string
	if !p.has(name) {
		return out, nil
	}
	err := p.decode(name, func(d *xml.Decoder, se xml.StartElement) error {
		if se.Name.Local != "si" {
			return nil
		}
		s, err := textOf(d, "t", "rPh")
		if err != nil {
			return err
		}
		out = append(out, s)
		return nil
	})
	return out, err
}

// textOf reads to the end of the element just started and joins the text of
// the elements named text inside it, leaving out the subtrees named skip.
func textOf(d *xml.Decoder, text, skip string) (string, error) {
	var sb strings.Builder
	depth, inText, skipping := 1, 0, 0
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if skipping > 0 || (skip != "" && t.Name.Local == skip) {
				skipping++
			} else if t.Name.Local == text {
				inText++
			}
		case xml.EndElement:
			depth--
			if skipping > 0 {
				skipping--
			} else if t.Name.Local == text && inText > 0 {
				inText--
			}
		case xml.CharData:
			if inText > 0 && skipping == 0 {
				sb.Write(t)
			}
		}
	}
	return sb.String(), nil
}

// dateStyles reads which cell styles (the index of an <xf> in <cellXfs>)
// format a number as a date or a time: true for a date, possibly with a time,
// and false (present) for a time of day only.
func (p *pkg) dateStyles(name string) (map[int]bool, error) {
	out := map[int]bool{}
	if !p.has(name) {
		return out, nil
	}
	custom := map[int]string{}
	var xfs []int
	err := p.decode(name, func(d *xml.Decoder, se xml.StartElement) error {
		switch se.Name.Local {
		case "numFmt":
			if id, err := strconv.Atoi(attr(se, "numFmtId")); err == nil {
				custom[id] = attr(se, "formatCode")
			}
		case "cellXfs":
			// Only the <xf> of <cellXfs> are cell styles; <cellStyleXfs> has
			// <xf> elements too.
			depth := 1
			for depth > 0 {
				tok, err := d.Token()
				if err != nil {
					return err
				}
				switch t := tok.(type) {
				case xml.StartElement:
					depth++
					if t.Name.Local == "xf" && depth == 2 {
						id, _ := strconv.Atoi(attr(t, "numFmtId"))
						xfs = append(xfs, id)
					}
				case xml.EndElement:
					depth--
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for i, id := range xfs {
		if date, timeOnly := dateFormat(id, custom[id]); date || timeOnly {
			out[i] = date
		}
	}
	return out, nil
}

// dateFormat says whether a number format shows a date (date) or only a time
// of day (timeOnly).
func dateFormat(id int, code string) (date, timeOnly bool) {
	switch {
	case id >= 18 && id <= 21, id >= 45 && id <= 47:
		return false, true
	case id >= 14 && id <= 22, id >= 27 && id <= 36, id >= 50 && id <= 58:
		return true, false
	}
	if code == "" || strings.EqualFold(code, "general") {
		return false, false
	}
	// Leave out quoted text, escaped characters and [colour] or [locale]
	// tags, then look for the letters of a date or a time.
	var sb strings.Builder
	quoted, bracket := false, false
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '\\' || c == '_' || c == '*':
			i++
		case c == '[':
			bracket = true
		case c == ']':
			bracket = false
		case bracket:
		default:
			sb.WriteByte(c)
		}
	}
	// A format with sections (positive;negative;zero;text) is judged by its
	// first.
	s := strings.ToLower(strings.SplitN(sb.String(), ";", 2)[0])
	hasDate := strings.ContainsAny(s, "dy")
	hasTime := strings.ContainsAny(s, "hs")
	if strings.Contains(s, "m") && !hasDate && !hasTime {
		// "mmm" or "mm" alone is a month.
		hasDate = true
	}
	return hasDate, hasTime && !hasDate
}

// sheet reads one worksheet: the first Rows rows and Cols columns, and the
// size of the whole sheet. Rows and columns are counted up to the last one
// with a value, so an empty styled cell does not grow the sheet.
func (p *pkg) sheet(name, part string, strs []string, dates map[int]bool, date1904 bool) (schema.ChatsFilePreviewSheet, error) {
	s := schema.ChatsFilePreviewSheet{Name: name, Rows: [][]string{}}
	kept := map[int][]string{}
	lastRow := 0
	err := p.decode(part, func(d *xml.Decoder, se xml.StartElement) error {
		if se.Name.Local != "row" {
			return nil
		}
		if err := p.ctx.Err(); err != nil {
			return err
		}
		r, err := strconv.Atoi(attr(se, "r"))
		if err != nil || r < 1 {
			r = lastRow + 1
		}
		lastRow = r
		var cells []string
		col := 0
		depth := 1
		for depth > 0 {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				depth++
				if t.Name.Local != "c" {
					continue
				}
				if c, ok := columnOf(attr(t, "r")); ok {
					col = c
				} else {
					col++
				}
				v, err := cellValue(d, t, strs, dates, date1904)
				if err != nil {
					return err
				}
				depth--
				if v == "" {
					continue
				}
				if r > s.TotalRows {
					s.TotalRows = r
				}
				if col > s.TotalCols {
					s.TotalCols = col
				}
				if r <= p.limits.Rows && col <= p.limits.Cols {
					for len(cells) < col {
						cells = append(cells, "")
					}
					cells[col-1] = v
				}
			case xml.EndElement:
				depth--
			}
		}
		if cells != nil {
			kept[r] = cells
		}
		return nil
	})
	if err != nil {
		return s, err
	}
	width := min(s.TotalCols, p.limits.Cols)
	for r := 1; r <= min(s.TotalRows, p.limits.Rows); r++ {
		row := make([]string, width)
		copy(row, kept[r])
		s.Rows = append(s.Rows, row)
	}
	return s, nil
}

// columnOf is the 1-based column of a cell reference such as "AB12".
func columnOf(ref string) (int, bool) {
	col := 0
	i := 0
	for ; i < len(ref); i++ {
		c := ref[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c < 'A' || c > 'Z' {
			break
		}
		col = col*26 + int(c-'A'+1)
		if col > 1<<20 {
			return 0, false
		}
	}
	return col, i > 0
}

// cellValue reads a <c> element to its end and gives the text it shows.
func cellValue(d *xml.Decoder, se xml.StartElement, strs []string, dates map[int]bool, date1904 bool) (string, error) {
	typ := attr(se, "t")
	var v, inline strings.Builder
	depth, in := 1, ""
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if t.Name.Local == "v" || (t.Name.Local == "t" && typ == "inlineStr") {
				in = t.Name.Local
			} else if t.Name.Local == "rPh" {
				in = "skip"
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == in || (in == "skip" && t.Name.Local == "rPh") {
				in = ""
			}
		case xml.CharData:
			switch in {
			case "v":
				v.Write(t)
			case "t":
				inline.Write(t)
			}
		}
	}
	raw := v.String()
	switch typ {
	case "inlineStr":
		return inline.String(), nil
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || i < 0 || i >= len(strs) {
			return "", nil
		}
		return strs[i], nil
	case "b":
		switch strings.TrimSpace(raw) {
		case "1", "true":
			return "TRUE", nil
		case "0", "false":
			return "FALSE", nil
		}
		return raw, nil
	case "str", "e", "d":
		return raw, nil
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return raw, nil
	}
	if style, err := strconv.Atoi(attr(se, "s")); err == nil {
		if date, ok := dates[style]; ok {
			return serialDate(f, date, date1904), nil
		}
	}
	return number(f), nil
}

// number shows a number as Excel's General format does at full width: at most
// 15 significant digits, so 0.1+0.2 shows 0.3, and no exponent.
func number(f float64) string {
	g, err := strconv.ParseFloat(strconv.FormatFloat(f, 'g', 15, 64), 64)
	if err != nil {
		g = f
	}
	return strconv.FormatFloat(g, 'f', -1, 64)
}

// serialDate turns an Excel serial number into a date, a date and time, or a
// time of day (date false). The 1900 system counts 1900 as a leap year, so
// serials from 61 count from 1899-12-30 and earlier ones from 1899-12-31.
func serialDate(f float64, date, date1904 bool) string {
	if f < 0 || f > 2958465 { // after 9999-12-31: not a date Excel shows
		return number(f)
	}
	days := math.Floor(f)
	secs := int64(math.Round((f - days) * 86400))
	if secs == 86400 {
		days++
		secs = 0
	}
	if !date {
		return clock(secs)
	}
	var base time.Time
	switch {
	case date1904:
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case days >= 61:
		base = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	default:
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	}
	day := base.AddDate(0, 0, int(days)).Format("2006-01-02")
	if secs == 0 {
		return day
	}
	return day + " " + clock(secs)
}

func clock(secs int64) string {
	h, m, s := secs/3600, secs/60%60, secs%60
	if s == 0 {
		return two(h) + ":" + two(m)
	}
	return two(h) + ":" + two(m) + ":" + two(s)
}

func two(n int64) string {
	if n < 10 {
		return "0" + strconv.FormatInt(n, 10)
	}
	return strconv.FormatInt(n, 10)
}
