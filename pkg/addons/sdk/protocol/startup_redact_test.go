// SPDX-License-Identifier: MIT
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// A Startup holds two credentials. Formatting or logging it must not show
// them, and the JSON encoding the host reads must stay as it is.
func TestStartupRedaction(t *testing.T) {
	s := Startup{Credential: "secret-one", HostCredential: "secret-two", HostEndpoint: "http://127.0.0.1:1", DataDir: "/data"}
	for name, out := range map[string]string{
		"%v":          fmt.Sprintf("%v", s),
		"%+v":         fmt.Sprintf("%+v", s),
		"%s":          fmt.Sprintf("%s", s),
		"%#v":         fmt.Sprintf("%#v", s),
		"%+v pointer": fmt.Sprintf("%+v", &s),
	} {
		if strings.Contains(out, "secret-") {
			t.Errorf("%s leaks a credential: %s", name, out)
		}
		if !strings.Contains(out, "[redacted]") || !strings.Contains(out, "/data") {
			t.Errorf("%s should mask the credentials and keep the rest: %s", name, out)
		}
	}
	var buf bytes.Buffer
	slog.New(slog.NewTextHandler(&buf, nil)).Info("start", "startup", s)
	if strings.Contains(buf.String(), "secret-") {
		t.Errorf("slog leaks a credential: %s", buf.String())
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"credential":"secret-one"`) || !strings.Contains(string(b), `"hostCredential":"secret-two"`) {
		t.Errorf("JSON encoding changed: %s", b)
	}
	if got := (Startup{}).String(); strings.Contains(got, "redacted") {
		t.Errorf("an empty credential must stay empty: %s", got)
	}
}
