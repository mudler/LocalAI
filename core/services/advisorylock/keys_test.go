package advisorylock

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Two features that take the same key elect one leader between them: while
// one holds it, the other never runs. Branches that each add the next free
// key merge without a conflict that git can see, so the values are checked
// here.
var _ = Describe("Advisory lock keys", func() {
	It("gives every key its own value and never reuses a retired one", func() {
		file, err := parser.ParseFile(token.NewFileSet(), "keys.go", nil, 0)
		Expect(err).NotTo(HaveOccurred())

		retired := map[int64]bool{100: true, 101: true, 102: true}
		byValue := map[int64]string{}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					lit, ok := vs.Values[i].(*ast.BasicLit)
					Expect(ok).To(BeTrue(), "%s must be a literal so that this check can read it", name.Name)
					value, err := strconv.ParseInt(lit.Value, 0, 64)
					Expect(err).NotTo(HaveOccurred())
					Expect(retired).NotTo(HaveKey(value), "%s takes the retired key %d", name.Name, value)
					Expect(byValue).NotTo(HaveKey(value), "%s and %s share the key %d", name.Name, byValue[value], value)
					byValue[value] = name.Name
				}
			}
		}
		Expect(byValue).NotTo(BeEmpty())
	})
})

var _ = Describe("The advisory lock keys", func() {
	// Add every new key here: two features that share a key would wait on
	// each other, or one would never run, and nothing else would tell.
	It("are all different", func() {
		keys := map[string]int64{
			"KeyAgentScheduler":      KeyAgentScheduler,
			"KeyHealthCheck":         KeyHealthCheck,
			"KeySchemaMigrate":       KeySchemaMigrate,
			"KeyBackendUpgradeCheck": KeyBackendUpgradeCheck,
			"KeyStateReconciler":     KeyStateReconciler,
			"KeyFailoverProber":      KeyFailoverProber,
			"KeyAddonPinSweep":       KeyAddonPinSweep,
			"KeyChatsReaper":         KeyChatsReaper,
			"KeyChatsTriggers":       KeyChatsTriggers,
			"KeyChatsConnectors":     KeyChatsConnectors,
			"KeyChatsConvert":        KeyChatsConvert,
			"KeyChatsEgressPrune":    KeyChatsEgressPrune,
			"KeyChatsFileIndex":      KeyChatsFileIndex,
			"KeyChatsLoops":          KeyChatsLoops,
			"KeyOpenResponsesSweep":  KeyOpenResponsesSweep,
			"KeyRemotesRefresh":      KeyRemotesRefresh,
			"KeyModelUpdateCheck":    KeyModelUpdateCheck,
			"KeyCarrierSwitch":       KeyCarrierSwitch,
		}
		seen := map[int64]string{}
		for name, key := range keys {
			other, dup := seen[key]
			Expect(dup).To(BeFalse(), "%s and %s share the key %d", name, other, key)
			seen[key] = name
		}
	})
})
