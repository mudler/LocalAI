// SPDX-License-Identifier: MIT
package cluster

// The fixture packages. All run the protocol fixture; they differ only in what
// the manifest declares, so the three shapes the host must support are covered:
// a UI-bearing package, a tool package without a UI and a service only package.

// UIModule is a minimal add-on interface in the compiled form of the SDK
// contract: it draws a title and a button that reads through the host.
const UIModule = `export function createAddon(sdk) {
  const h = sdk.React.createElement
  return {
    Component: () => h('section', null,
      h('h2', { 'data-testid': 'fnd-title' }, 'Foundation fixture'),
      h('button', { 'data-testid': 'fnd-read', onClick: () => sdk.api.read({ arguments: { tool: 'echo' } }).then(
        (out) => { window.__fnd = { ok: out } }, (e) => { window.__fnd = { error: e && e.status } }) }, 'Read')),
    dispose() {},
  }
}
`

// UIPackage is the UI-bearing add-on with tools.
func UIPackage(version string, fixture map[string]any) Package {
	return Package{
		ID: "found-ui", Version: version, Effects: "yes", Fixture: fixture,
		Capabilities: map[string]any{
			"service": true, "tools": []string{"echo", "export"},
			"ui": map[string]any{"entry": "ui.js", "css": []string{"ui.css"}, "sdk": map[string]int{"min": 1, "max": 2}, "pages": []string{"home"}, "mainPage": "home", "title": "Foundation", "description": "Foundation fixture."},
		},
		Files: map[string]string{"ui.js": UIModule, "ui.css": "body{}\n", "private.txt": "not declared"},
	}
}

// ToolPackage is a tool add-on without a UI.
func ToolPackage(version string, fixture map[string]any) Package {
	return Package{ID: "found-tool", Version: version, Effects: "yes", Fixture: fixture, Capabilities: map[string]any{"service": true, "tools": []string{"echo", "export"}}}
}

// ServicePackage declares only a service: no tool, no page.
func ServicePackage(version string, fixture map[string]any) Package {
	return Package{ID: "found-svc", Version: version, Effects: "yes", Fixture: fixture, Capabilities: map[string]any{"service": true}}
}
