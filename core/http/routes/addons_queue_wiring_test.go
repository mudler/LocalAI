// SPDX-License-Identifier: MIT

package routes

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The handlers that hand their work to the operation queue must receive it.
// Registered without it, the upgrade handler falls back to the inline path,
// which accepts one upgrade at a time: with "upgrade all" one request wins and
// the rest get 409 operation_conflict. Nothing else tells the two apart at
// the route, so the registration is checked directly.
func TestQueueingAddonHandlersReceiveTheOperationQueue(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "addons.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (sel.Sel.Name != "UpgradeAddon" && sel.Sel.Name != "QueueWithHostGrants") {
			return true
		}
		seen[sel.Sel.Name] = true
		if call.Ellipsis == token.NoPos {
			t.Errorf("%s is registered without the operation queue options (queue...)", sel.Sel.Name)
		}
		return true
	})
	for _, name := range []string{"UpgradeAddon", "QueueWithHostGrants"} {
		if !seen[name] {
			t.Errorf("%s is not registered in addons.go", name)
		}
	}
}
