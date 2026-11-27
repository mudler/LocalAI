package plugins

import "net/url"

// ReviewPath returns the path of the web UI page where an administrator
// reviews and installs a plugin: the Plugins tab of the Add-ons page, opened
// on the plugin. A plugin that runs code is installed only there (spec 9.2),
// so the CLI and the assistant point to it instead of installing.
func ReviewPath(pluginID string) string {
	return "/app/addons?" + url.Values{"tab": {"plugins"}, "plugin": {pluginID}}.Encode()
}
