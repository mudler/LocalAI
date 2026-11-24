// SPDX-License-Identifier: MIT
package imapc

import (
	"strconv"
	"strings"

	"github.com/emersion/go-imap/v2"

	"github.com/mudler/LocalAI/addons/mail/internal/mimetext"
)

// Role says what a leaf part is for.
type Role int

const (
	// RoleBody is text the person reads as the message.
	RoleBody Role = iota
	// RoleAttachment is anything else a message carries.
	RoleAttachment
	// RoleSkip is a part that is neither: a signature.
	RoleSkip
)

// Part is a leaf of the body structure.
type Part struct {
	Path      []int
	Type      string // "text/plain"
	Encoding  string
	Charset   string
	Size      uint32 // as stored, before the transfer encoding is undone
	Name      string
	Inline    bool
	Role      Role
	IsMessage bool
}

// PathString is the part number as IMAP writes it: "1.2".
func (p Part) PathString() string {
	s := make([]string, len(p.Path))
	for i, n := range p.Path {
		s[i] = strconv.Itoa(n)
	}
	return strings.Join(s, ".")
}

// DecodedSize estimates the size once the transfer encoding is undone.
func (p Part) DecodedSize() int64 {
	if strings.EqualFold(p.Encoding, "base64") {
		return int64(p.Size) / 4 * 3
	}
	return int64(p.Size)
}

// Text tells whether read-attachment shows the part: text types only.
func (p Part) Text() bool {
	t := p.Type
	return strings.HasPrefix(t, "text/") || t == "application/json" || t == "application/xml" ||
		strings.HasSuffix(t, "+json") || strings.HasSuffix(t, "+xml")
}

// Parts lists the leaf parts of a body structure in order, with the role of
// each. A text part without a file name and not marked as an attachment is the
// body; a signature is skipped; everything else is an attachment.
func Parts(bs imap.BodyStructure) []Part {
	var out []Part
	bs.Walk(func(path []int, part imap.BodyStructure) bool {
		sp, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		p := Part{Path: append([]int(nil), path...), Type: sp.MediaType(), Encoding: sp.Encoding, Charset: sp.Params["charset"], Size: sp.Size,
			Name: mimetext.Line(mimetext.DecodeWords(sp.Filename()), 200), IsMessage: sp.MessageRFC822 != nil}
		disp := ""
		if d := sp.Disposition(); d != nil {
			disp = strings.ToLower(d.Value)
		}
		p.Inline = disp == "inline"
		switch {
		case strings.Contains(p.Type, "signature"):
			p.Role = RoleSkip
		case disp == "attachment" || p.Name != "" || !strings.HasPrefix(p.Type, "text/") || p.IsMessage:
			p.Role = RoleAttachment
		case p.Type == "text/plain" || p.Type == "text/html":
			p.Role = RoleBody
		default:
			p.Role = RoleAttachment
		}
		out = append(out, p)
		return true
	})
	return out
}

// BodyParts picks the parts that make the body: all text/plain parts when the
// message has any, else the text/html parts.
func BodyParts(all []Part) (parts []Part, html bool) {
	for _, p := range all {
		if p.Role == RoleBody && p.Type == "text/plain" {
			parts = append(parts, p)
		}
	}
	if len(parts) > 0 {
		return parts, false
	}
	for _, p := range all {
		if p.Role == RoleBody && p.Type == "text/html" {
			parts = append(parts, p)
		}
	}
	return parts, len(parts) > 0
}
