package officeread

import (
	"encoding/xml"
	"strings"
)

func readDOCX(p parts, main string) ([]Section, error) {
	var (
		lines    []string
		tables   [][][]string
		para     strings.Builder
		style    string
		numbered bool
		pDepth   int
		tblDepth int
		inT      bool
		rows     [][]string
		row      []string
		cell     []string
	)
	err := walkPart(p, main, func(t xml.Token) {
		switch t := t.(type) {
		case xml.StartElement:
			if !strings.Contains(t.Name.Space, "wordprocessingml") {
				return
			}
			switch t.Name.Local {
			case "tbl":
				tblDepth++
				if tblDepth == 1 {
					rows = nil
				}
			case "tr":
				if tblDepth == 1 {
					row = nil
				}
			case "tc":
				if tblDepth == 1 {
					cell = nil
				}
			case "p":
				pDepth++
				if pDepth == 1 {
					para.Reset()
					style, numbered = "", false
				}
			case "pStyle":
				if pDepth == 1 {
					style, _ = attrNS(t, "val")
				}
			case "numPr":
				numbered = pDepth == 1
			case "numId":
				if v, _ := attrNS(t, "val"); v == "0" {
					numbered = false
				}
			case "t":
				inT = true
			case "tab":
				para.WriteByte('\t')
			case "br", "cr":
				para.WriteByte('\n')
			}
		case xml.EndElement:
			if !strings.Contains(t.Name.Space, "wordprocessingml") {
				return
			}
			switch t.Name.Local {
			case "t":
				inT = false
			case "p":
				pDepth--
				if pDepth != 0 {
					return
				}
				text := para.String()
				if tblDepth > 0 {
					cell = append(cell, text)
					return
				}
				lines = append(lines, paraPrefix(style, numbered)+text)
			case "tc":
				if tblDepth == 1 {
					row = append(row, strings.Join(cell, " "))
				}
			case "tr":
				if tblDepth == 1 {
					rows = append(rows, row)
				}
			case "tbl":
				tblDepth--
				if tblDepth == 0 {
					tables = append(tables, rows)
					for _, r := range rows {
						lines = append(lines, strings.Join(r, "\t"))
					}
				}
			}
		case xml.CharData:
			if inT {
				para.Write(t)
			}
		}
	})
	if err != nil {
		return nil, err
	}
	text := strings.TrimRight(strings.Join(lines, "\n"), "\n")
	return []Section{{Title: "Document", Text: text, Tables: tables}}, nil
}

// attrNS returns an attribute by local name, in any namespace.
func attrNS(se xml.StartElement, local string) (string, bool) {
	for _, a := range se.Attr {
		if a.Name.Local == local {
			return a.Value, true
		}
	}
	return "", false
}

func paraPrefix(style string, numbered bool) string {
	switch strings.ToLower(strings.ReplaceAll(style, " ", "")) {
	case "heading1":
		return "# "
	case "heading2":
		return "## "
	case "heading3":
		return "### "
	case "listbullet":
		return "- "
	}
	if numbered {
		return "- "
	}
	return ""
}
