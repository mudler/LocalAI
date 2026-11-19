//go:build windows

package application

import (
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/core/services/addons/wsl"
)

// addonWSL runs the add-ons whose manifest has osSettings.windows.execution
// "wsl2" in the WSL 2 distribution distro (empty: the default distribution).
func addonWSL(distro string) addonruntime.WSL {
	return wsl.New(wsl.Options{Distro: distro})
}
