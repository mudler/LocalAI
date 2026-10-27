package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func appCard(mut func(*ToolApp)) ToolCard {
	a := &ToolApp{Server: "connector:abc", Tool: "get-time", ResourceURI: "ui://clock/app.html", Visibility: []string{"model", "app"},
		Input: json.RawMessage(`{"zone":"UTC"}`), Result: json.RawMessage(`{"content":[]}`)}
	if mut != nil {
		mut(a)
	}
	return ToolCard{Kind: "mcp_app", ID: "0123456789abcdef", App: a}
}

func TestCleanCardKeepsAWellFormedApp(t *testing.T) {
	c, ok := cleanCard(appCard(nil))
	if !ok || c.App == nil || c.App.Server != "connector:abc" || string(c.App.Result) != `{"content":[]}` {
		t.Fatalf("clean = %+v %v", c, ok)
	}
}

func TestCleanCardRefusesABadReference(t *testing.T) {
	for name, mut := range map[string]func(*ToolApp){
		"no server":      func(a *ToolApp) { a.Server = "" },
		"long server":    func(a *ToolApp) { a.Server = strings.Repeat("s", 300) },
		"not a ui uri":   func(a *ToolApp) { a.ResourceURI = "https://evil.example/x" },
		"control in uri": func(a *ToolApp) { a.ResourceURI = "ui://a\nb" },
	} {
		if _, ok := cleanCard(appCard(mut)); ok {
			t.Errorf("%s: the card was accepted", name)
		}
	}
}

func TestCleanCardDropsAnOversizeInputOrResultButKeepsTheReference(t *testing.T) {
	c, ok := cleanCard(appCard(func(a *ToolApp) {
		a.Input = json.RawMessage(`"` + strings.Repeat("a", 20<<10) + `"`)
		a.Result = json.RawMessage(`"` + strings.Repeat("a", 400<<10) + `"`)
	}))
	if !ok || c.App == nil || c.App.Input != nil || c.App.Result != nil || !c.App.Cut {
		t.Fatalf("clean = %+v %v", c, ok)
	}
}

func TestCleanCardDropsInvalidJSON(t *testing.T) {
	c, ok := cleanCard(appCard(func(a *ToolApp) { a.Result = json.RawMessage(`{not json`) }))
	if !ok || c.App.Result != nil {
		t.Fatalf("clean = %+v %v", c, ok)
	}
}
