package preview

import (
	"encoding/xml"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/schema"
)

// presentation reads the slides of a .pptx in presentation order: the title
// (the title placeholder), the other text one line per paragraph with its
// outline level, and the speaker notes.
func (p *pkg) presentation() (schema.ChatsFilePreview, error) {
	main := p.mainPart("ppt/presentation.xml")
	if !p.has(main) {
		return schema.ChatsFilePreview{}, ErrInvalid
	}
	rels, err := p.rels(main)
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	var parts []string
	err = p.decode(main, func(_ *xml.Decoder, se xml.StartElement) error {
		if se.Name.Local == "sldId" {
			parts = append(parts, rels[relID(se)])
		}
		return nil
	})
	if err != nil {
		return schema.ChatsFilePreview{}, err
	}
	out := schema.ChatsFilePreview{Kind: kindSlides, Slides: []schema.ChatsFilePreviewSlide{}}
	budget := textBudget{left: p.limits.Text}
	for i, part := range parts {
		if !p.has(part) {
			continue
		}
		s := schema.ChatsFilePreviewSlide{N: i + 1}
		shapes, err := p.shapes(part)
		if err != nil {
			return schema.ChatsFilePreview{}, err
		}
		for _, sh := range shapes {
			if sh.ph == "title" || sh.ph == "ctrTitle" {
				if s.Title == "" && len(sh.lines) > 0 {
					titles := make([]string, len(sh.lines))
					for j, l := range sh.lines {
						titles[j] = l.Text
					}
					s.Title = strings.Join(titles, " ")
				}
				continue
			}
			if isChrome(sh.ph) {
				continue
			}
			s.Lines = append(s.Lines, sh.lines...)
		}
		if notes := p.notesOf(part); notes != "" {
			s.Notes = notes
		}
		n := utf8.RuneCountInString(s.Title) + utf8.RuneCountInString(s.Notes)
		for _, l := range s.Lines {
			n += utf8.RuneCountInString(l.Text)
		}
		if !budget.take(n) {
			break
		}
		out.Slides = append(out.Slides, s)
	}
	out.Truncated = budget.truncated
	return out, nil
}

// isChrome is a placeholder that repeats on every slide: the slide number,
// the date, the footer, the picture of the slide in the notes.
func isChrome(ph string) bool {
	switch ph {
	case "sldNum", "dt", "ftr", "hdr", "sldImg":
		return true
	}
	return false
}

type textShape struct {
	ph    string
	lines []schema.ChatsFilePreviewLine
}

// shapes reads the text shapes of a slide or notes part.
func (p *pkg) shapes(part string) ([]textShape, error) {
	var out []textShape
	err := p.decode(part, func(d *xml.Decoder, se xml.StartElement) error {
		if se.Name.Local != "sp" {
			return nil
		}
		sh, err := readShape(d)
		if err != nil {
			return err
		}
		out = append(out, sh)
		return nil
	})
	return out, err
}

func readShape(d *xml.Decoder) (textShape, error) {
	var sh textShape
	var sb strings.Builder
	depth, lvl, inP, inText := 1, 0, false, false
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			return sh, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch t.Name.Local {
			case "ph":
				sh.ph = attr(t, "type")
				if sh.ph == "" {
					// A placeholder with no type is a body.
					sh.ph = "body"
				}
			case "p":
				inP, lvl = true, 0
				sb.Reset()
			case "pPr":
				if inP {
					lvl, _ = strconv.Atoi(attr(t, "lvl"))
				}
			case "t":
				inText = true
			case "br":
				if inP {
					sb.WriteByte('\n')
				}
			}
		case xml.EndElement:
			depth--
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				if inP {
					if s := strings.TrimSpace(sb.String()); s != "" {
						sh.lines = append(sh.lines, schema.ChatsFilePreviewLine{Text: s, Level: lvl})
					}
					inP = false
				}
			}
		case xml.CharData:
			if inText && inP {
				sb.Write(t)
			}
		}
	}
	return sh, nil
}

// notesOf is the speaker notes of a slide: the text of the body of its notes
// slide. A slide without notes, or with notes that cannot be read, has none.
func (p *pkg) notesOf(slide string) string {
	rels, err := p.rels(slide)
	if err != nil {
		return ""
	}
	for _, target := range rels {
		if !strings.Contains(target, "notesSlide") || !p.has(target) {
			continue
		}
		shapes, err := p.shapes(target)
		if err != nil {
			return ""
		}
		var lines []string
		for _, sh := range shapes {
			if sh.ph != "body" {
				continue
			}
			for _, l := range sh.lines {
				lines = append(lines, l.Text)
			}
		}
		return strings.Join(lines, "\n")
	}
	return ""
}
