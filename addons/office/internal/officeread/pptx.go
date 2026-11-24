package officeread

import (
	"encoding/xml"
	"fmt"
	"strings"
)

func readPPTX(p parts, main string) ([]Section, error) {
	var order []string
	err := walkPart(p, main, func(t xml.Token) {
		if se, ok := t.(xml.StartElement); ok && se.Name.Local == "sldId" {
			order = append(order, relID(se))
		}
	})
	if err != nil {
		return nil, err
	}
	rels, err := relsOf(p, main)
	if err != nil {
		return nil, err
	}
	var out []Section
	for i, rid := range order {
		r, ok := rels[rid]
		if !ok {
			return nil, fmt.Errorf("officeread: slide %d has no relationship %q", i+1, rid)
		}
		lines, err := slideLines(p, r.target)
		if err != nil {
			return nil, err
		}
		slideRels, err := relsOf(p, r.target)
		if err != nil {
			return nil, err
		}
		for _, sr := range slideRels {
			if !strings.HasSuffix(sr.typ, "/notesSlide") {
				continue
			}
			notes, err := slideLines(p, sr.target)
			if err != nil {
				return nil, err
			}
			if len(notes) > 0 {
				lines = append(lines, "Notes:")
				lines = append(lines, notes...)
			}
			break
		}
		out = append(out, Section{Title: fmt.Sprintf("Slide %d", i+1), Text: strings.Join(lines, "\n")})
	}
	return out, nil
}

// slideLines returns the non-empty paragraphs of a slide or notes part. Text
// in slide number, date, footer, header and slide image placeholders is left out.
func slideLines(p parts, name string) ([]string, error) {
	var (
		lines []string
		para  strings.Builder
		inP   bool
		inT   bool
		skip  bool
	)
	err := walkPart(p, name, func(t xml.Token) {
		switch t := t.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "sp":
				skip = false
			case "ph":
				switch v, _ := attr(t, "type"); v {
				case "sldNum", "dt", "ftr", "hdr", "sldImg":
					skip = true
				}
			case "p":
				if t.Name.Space != "" && strings.HasSuffix(t.Name.Space, "drawingml/2006/main") {
					inP = true
					para.Reset()
				}
			case "t":
				inT = inP
			case "br":
				if inP {
					para.WriteByte('\n')
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				if inP && strings.HasSuffix(t.Name.Space, "drawingml/2006/main") {
					inP = false
					if s := para.String(); s != "" && !skip {
						lines = append(lines, s)
					}
				}
			case "sp":
				skip = false
			}
		case xml.CharData:
			if inT {
				para.Write(t)
			}
		}
	})
	return lines, err
}
