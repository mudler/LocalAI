package skills

import (
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	long := strings.Repeat("x", MaxDescription+1)
	cases := []struct {
		name     string
		in       string
		wantName string
		wantDesc string
		wantErr  error
	}{
		{"plain", "---\nname: invoices\ndescription: How we file supplier invoices.\n---\nBody.\n", "invoices", "How we file supplier invoices.", nil},
		{"double quotes", "---\nname: \"invoices\"\ndescription: \"Use when: filing \\\"paid\\\" invoices\"\n---\n", "invoices", `Use when: filing "paid" invoices`, nil},
		{"single quotes", "---\nname: 'invoices'\ndescription: 'It''s a procedure: step one'\n---\n", "invoices", "It's a procedure: step one", nil},
		{"literal block", "---\nname: report\ndescription: |\n  First line.\n  Second line.\nlicense: MIT\n---\n", "report", "First line. Second line.", nil},
		{"folded block", "---\nname: report\ndescription: >-\n  Folded\n  text here.\n---\n", "report", "Folded text here.", nil},
		{"unknown keys, nesting and comments are ignored", "---\n# a comment\nlicense: MIT\nmetadata:\n  version: \"2\"\nname: report\ndescription: Weekly report.\nallowed-tools: Read\n---\n", "report", "Weekly report.", nil},
		{"windows line ends and a BOM", "\xef\xbb\xbf---\r\nname: report\r\ndescription: Weekly report.\r\n---\r\n", "report", "Weekly report.", nil},
		{"the last value of a repeated key wins", "---\nname: a\nname: b\ndescription: D.\n---\n", "b", "D.", nil},
		{"no frontmatter", "# Title\nname: x\n", "", "", ErrNoFrontmatter},
		{"frontmatter never closed", "---\nname: x\ndescription: D.\n", "", "", ErrNoFrontmatter},
		{"no name", "---\ndescription: D.\n---\n", "", "", ErrNoName},
		{"empty name", "---\nname:\ndescription: D.\n---\n", "", "", ErrNoName},
		{"bad name: capital", "---\nname: Invoices\ndescription: D.\n---\n", "", "", ErrBadName},
		{"bad name: slash", "---\nname: ../x\ndescription: D.\n---\n", "", "", ErrBadName},
		{"bad name: too long", "---\nname: " + strings.Repeat("a", 65) + "\ndescription: D.\n---\n", "", "", ErrBadName},
		{"name of 64 characters", "---\nname: " + strings.Repeat("a", 64) + "\ndescription: D.\n---\n", strings.Repeat("a", 64), "D.", nil},
		{"no description", "---\nname: x\n---\n", "", "", ErrNoDescription},
		{"description of only invisible characters", "---\nname: x\ndescription: \"\xe2\x80\x8b\xe2\x80\xae\"\n---\n", "", "", ErrNoDescription},
		{"description too long", "---\nname: x\ndescription: " + long + "\n---\n", "", "", ErrDescriptionTooLong},
		{"description of exactly the limit", "---\nname: x\ndescription: " + long[1:] + "\n---\n", "x", long[1:], nil},
		{"control and bidi characters are removed", "---\nname: x\ndescription: \"a\x00b\xe2\x80\xaec\xe2\x80\x8bd\x07\"\n---\n", "x", "abcd", nil},
		{"white space runs become one space", "---\nname: x\ndescription: \"a \t\xc2\xa0 b\"\n---\n", "x", "a b", nil},
		{"too large", "---\nname: x\ndescription: D.\n---\n" + strings.Repeat("y", MaxFileSize), "", "", ErrTooLarge},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			name, desc, err := Parse([]byte(c.in))
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("error = %v, want %v", err, c.wantErr)
			}
			if name != c.wantName || desc != c.wantDesc {
				t.Fatalf("got (%q, %q), want (%q, %q)", name, desc, c.wantName, c.wantDesc)
			}
		})
	}
}

func TestCleanDropsInvalidUTF8(t *testing.T) {
	if got := Clean("a\xffb \n c"); got != "ab c" {
		t.Fatalf("Clean = %q", got)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "docx", "a-b-1", "0day"} {
		if !ValidName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-a", "A", "a_b", "a b", "a/b", ".hidden"} {
		if ValidName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
