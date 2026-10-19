package schema_test

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/schema"
)

// TestConversationKeepsEveryFieldTheUISends guards against the server
// silently dropping a chat setting: every key serializeChat in the React
// UI sends must have a JSON field on schema.Conversation. reasoningEffort
// and localaiAssistant were each lost this way once.
func TestConversationKeepsEveryFieldTheUISends(t *testing.T) {
	src, err := os.ReadFile("../http/react-ui/src/hooks/useChat.js")
	if err != nil {
		t.Fatalf("read useChat.js: %v", err)
	}
	body := regexp.MustCompile(`(?s)function serializeChat\(chat\) \{\s*return \{(.*?)\n  \}`).FindSubmatch(src)
	if body == nil {
		t.Fatal("serializeChat not found in useChat.js")
	}
	sent := regexp.MustCompile(`(?m)^\s*(\w+):`).FindAllSubmatch(body[1], -1)
	if len(sent) < 10 {
		t.Fatalf("only %d keys found in serializeChat; the pattern no longer matches", len(sent))
	}

	known := map[string]bool{}
	ty := reflect.TypeOf(schema.Conversation{})
	for i := 0; i < ty.NumField(); i++ {
		if name := strings.Split(ty.Field(i).Tag.Get("json"), ",")[0]; name != "" {
			known[name] = true
		}
	}
	for _, m := range sent {
		if key := string(m[1]); !known[key] {
			t.Errorf("serializeChat sends %q but schema.Conversation has no field for it; the server drops it", key)
		}
	}
}
