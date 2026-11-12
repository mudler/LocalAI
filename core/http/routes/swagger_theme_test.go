// SPDX-License-Identifier: MIT

package routes

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The API reference restates the app palette in its own variables, because the
// page never loads the app's CSS. This test reads the one palette source of the
// React UI and fails when a value in the skin differs from it.
func TestSwaggerThemeFollowsThePalette(t *testing.T) {
	raw, err := os.ReadFile("../react-ui/src/theme/theme-localai.json")
	if err != nil {
		t.Fatal(err)
	}
	var theme struct {
		Light map[string]string `json:"light"`
		Dark  map[string]string `json:"dark"`
	}
	if err := json.Unmarshal(raw, &theme); err != nil {
		t.Fatal(err)
	}
	modes := map[string]map[string]string{"light": theme.Light, "dark": theme.Dark}

	roles := map[string]string{
		"bg": "canvas", "surface": "card", "sunken": "inset", "line": "edge",
		"ink": "text", "muted": "muted", "accent": "accent", "accent-text": "accent-text",
		"on-accent": "on-accent", "ok": "ok", "ok-text": "ok-text", "warn": "warn",
		"warn-text": "warn-text", "error": "error", "error-text": "error-text",
	}
	// The light block is the first :root, the dark one is [data-theme="dark"].
	light := swaggerThemeCSS[:strings.Index(swaggerThemeCSS, "@media")]
	darkStart := strings.Index(swaggerThemeCSS, `:root[data-theme="dark"]`)
	dark := swaggerThemeCSS[darkStart:]
	// The same dark values sit in the media block; it must hold them too.
	media := swaggerThemeCSS[strings.Index(swaggerThemeCSS, "@media"):darkStart]

	for mode, block := range map[string]string{"light": light, "dark": dark} {
		for name, role := range roles {
			re := regexp.MustCompile(`--lai-` + regexp.QuoteMeta(name) + `: (#[0-9a-f]{6});`)
			m := re.FindStringSubmatch(block)
			if m == nil {
				t.Errorf("%s: --lai-%s is missing", mode, name)
				continue
			}
			if want := modes[mode][role]; m[1] != want {
				t.Errorf("%s: --lai-%s is %s, the theme role %s is %s", mode, name, m[1], role, want)
			}
		}
	}
	for name, role := range roles {
		re := regexp.MustCompile(`--lai-` + regexp.QuoteMeta(name) + `: (#[0-9a-f]{6});`)
		if m := re.FindStringSubmatch(media); m == nil || m[1] != theme.Dark[role] {
			t.Errorf("media block: --lai-%s does not match the dark role %s", name, role)
		}
	}
	for _, old := range []string{"--lai-blue", "--lai-mint", "--lai-amber", "--lai-red"} {
		if strings.Contains(swaggerThemeCSS, old) {
			t.Errorf("%s is left over from the old palette", old)
		}
	}
}
