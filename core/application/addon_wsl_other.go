//go:build !windows

package application

import addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"

// addonWSL is nil outside Windows: a package with execution "wsl2" is a
// Windows package, which these hosts never install.
func addonWSL(string) addonruntime.WSL { return nil }
