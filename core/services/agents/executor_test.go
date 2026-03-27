package agents

import "testing"

func TestStripThinkingTags(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "no tags",
			input: "Hello, world!",
			want:  "Hello, world!",
		},
		{
			name:  "single tag pair",
			input: "before<thinking>secret thoughts</thinking>after",
			want:  "beforeafter",
		},
		{
			name:  "multiple tag pairs",
			input: "a<thinking>one</thinking>b<thinking>two</thinking>c",
			want:  "abc",
		},
		{
			name:  "nested tags",
			input: "<thinking>outer<thinking>inner</thinking>still outer</thinking>visible",
			want:  "still outer</thinking>visible",
		},
		{
			name:  "unclosed opening tag",
			input: "hello<thinking>this is unclosed",
			want:  "hello<thinking>this is unclosed",
		},
		{
			name:  "only closing tag",
			input: "hello</thinking>world",
			want:  "hello</thinking>world",
		},
		{
			name:  "tags with whitespace around content",
			input: "before<thinking> spaced out </thinking>after",
			want:  "beforeafter",
		},
		{
			name:  "empty thinking block",
			input: "before<thinking></thinking>after",
			want:  "beforeafter",
		},
		{
			name:  "multiline thinking block",
			input: "before<thinking>\nline1\nline2\n</thinking>after",
			want:  "beforeafter",
		},
		{
			name:  "adjacent tag pairs",
			input: "<thinking>a</thinking><thinking>b</thinking>",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripThinkingTags(tc.input)
			if got != tc.want {
				t.Errorf("stripThinkingTags(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
