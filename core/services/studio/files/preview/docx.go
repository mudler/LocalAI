package preview

import (
	"encoding/xml"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/schema"
)

// errStop ends a walk early without an error: the budget is spent.
var errStop = errors.New("stop")

var (
	headingName = regexp.MustCompile(`(?i)^heading\s*([1-9])$`)
	headingID   = regexp.MustCompile(`(?i)^heading([1-9])$`)
)

// document reads the paragraphs of a .docx in order: headings (from the
// paragraph style, whatever the language of its id), list items with their
// level, and plain paragraphs. A table is one block with its rows of cells.
func (p *pkg) document() (schema.ChatsFilePreview, error) {
	main := p.mainPart("word/document.xml")
	if !p.has(main) {
		return schema.ChatsFilePreview{}, ErrInvalid
	}
	levels, lists, err := p.paragraphStyles("word/styles.xml")
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	out := schema.ChatsFilePreview{Kind: kindDocument, Blocks: []schema.ChatsFilePreviewBlock{}}
	budget := textBudget{left: p.limits.Text}
	err = p.decode(main, func(d *xml.Decoder, se xml.StartElement) error {
		var b schema.ChatsFilePreviewBlock
		var err error
		switch se.Name.Local {
		case "p":
			b, err = paragraph(d, levels, lists)
		case "tbl":
			b, err = table(d, levels, lists)
		default:
			return nil
		}
		if err != nil || b.Text == "" {
			return err
		}
		if !budget.take(utf8.RuneCountInString(b.Text)) {
			return errStop
		}
		out.Blocks = append(out.Blocks, b)
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return schema.ChatsFilePreview{}, err
	}
	out.Truncated = budget.truncated
	return out, nil
}

// paragraphStyles maps a paragraph style id to its heading level (by the
// style's name, "heading 2" or "Title", or its outline level) and tells the
// list styles ("List Bullet": a style that numbers its paragraphs).
func (p *pkg) paragraphStyles(name string) (map[string]int, map[string]bool, error) {
	out := map[string]int{}
	lists := map[string]bool{}
	if !p.has(name) {
		return out, lists, nil
	}
	err := p.decode(name, func(d *xml.Decoder, se xml.StartElement) error {
		if se.Name.Local != "style" {
			return nil
		}
		id := attr(se, "styleId")
		depth := 1
		for depth > 0 {
			tok, err := d.Token()
			if err != nil {
				return err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				depth++
				switch t.Name.Local {
				case "name":
					n := attr(t, "val")
					if m := headingName.FindStringSubmatch(n); m != nil {
						out[id], _ = strconv.Atoi(m[1])
					} else if strings.EqualFold(n, "title") {
						out[id] = 1
					}
				case "outlineLvl":
					if lvl, err := strconv.Atoi(attr(t, "val")); err == nil && lvl < 9 && out[id] == 0 {
						out[id] = lvl + 1
					}
				case "numPr":
					lists[id] = true
				}
			case xml.EndElement:
				depth--
			}
		}
		return nil
	})
	return out, lists, err
}

// paragraph reads a <w:p> to its end.
func paragraph(d *xml.Decoder, levels map[string]int, lists map[string]bool) (schema.ChatsFilePreviewBlock, error) {
	var sb strings.Builder
	style, list, ilvl, outline := "", false, 0, -1
	depth, inText := 1, false
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			return schema.ChatsFilePreviewBlock{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "pStyle":
				style = attr(t, "val")
			case "numPr":
				list = true
			case "ilvl":
				ilvl, _ = strconv.Atoi(attr(t, "val"))
			case "outlineLvl":
				if v, err := strconv.Atoi(attr(t, "val")); err == nil {
					outline = v
				}
			case "t":
				inText = true
			case "tab":
				sb.WriteByte('\t')
			case "br", "cr":
				sb.WriteByte('\n')
			}
		case xml.EndElement:
			depth--
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}
	b := schema.ChatsFilePreviewBlock{Type: "paragraph", Text: strings.TrimSpace(sb.String())}
	level := levels[style]
	if level == 0 {
		if m := headingID.FindStringSubmatch(style); m != nil {
			level, _ = strconv.Atoi(m[1])
		} else if strings.EqualFold(style, "title") {
			level = 1
		} else if outline >= 0 && outline < 9 {
			level = outline + 1
		}
	}
	switch {
	case level > 0:
		b.Type, b.Level = "heading", level
	case list || lists[style]:
		b.Type, b.Level = "item", ilvl
	}
	return b, nil
}

// table reads a <w:tbl> to its end: one row per <w:tr>, one cell per <w:tc>
// with the text of its paragraphs. A table inside a cell adds its text to that
// cell. Text is the rows as tab-separated lines.
func table(d *xml.Decoder, levels map[string]int, lists map[string]bool) (schema.ChatsFilePreviewBlock, error) {
	var rows [][]string
	var row, cell []string
	for depth := 1; depth > 0; {
		tok, err := d.Token()
		if err != nil {
			return schema.ChatsFilePreviewBlock{}, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p": // paragraph and table read to their end
				b, err := paragraph(d, levels, lists)
				if err != nil {
					return schema.ChatsFilePreviewBlock{}, err
				}
				if b.Text != "" {
					cell = append(cell, b.Text)
				}
			case "tbl":
				inner, err := table(d, levels, lists)
				if err != nil {
					return schema.ChatsFilePreviewBlock{}, err
				}
				if inner.Text != "" {
					cell = append(cell, strings.ReplaceAll(inner.Text, "\n", " "))
				}
			case "tr":
				row = []string{}
				depth++
			case "tc":
				cell = nil
				depth++
			default:
				depth++
			}
		case xml.EndElement:
			depth--
			switch t.Name.Local {
			case "tc":
				row = append(row, strings.Join(cell, " "))
			case "tr":
				rows = append(rows, row)
			}
		}
	}
	lines := make([]string, 0, len(rows))
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = strings.NewReplacer("\t", " ", "\n", " ").Replace(c)
		}
		lines = append(lines, strings.Join(cells, "\t"))
	}
	text := strings.TrimSpace(strings.Join(lines, "\n"))
	return schema.ChatsFilePreviewBlock{Type: "table", Text: text, Rows: rows}, nil
}
