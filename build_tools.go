//go:build tools
// +build tools

// List of tool dependencies. It should not actually be compiled.
package ignore_me_build_tools

import (
	_ "github.com/deepmap/oapi-codegen/cmd/oapi-codegen"
	_ "github.com/vmware-tanzu/carvel-ytt/cmd/ytt"
)
