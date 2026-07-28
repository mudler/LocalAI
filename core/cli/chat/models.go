package chat

import "strings"

func formatChatModelList(models []string, current string) string {
	var b strings.Builder
	for _, model := range models {
		prefix := "  "
		if model == current {
			prefix = "* "
		}
		b.WriteString(prefix)
		b.WriteString(model)
		b.WriteByte('\n')
	}
	return b.String()
}
