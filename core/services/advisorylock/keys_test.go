package advisorylock

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

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
		}
		seen := map[int64]string{}
		for name, key := range keys {
			other, dup := seen[key]
			Expect(dup).To(BeFalse(), "%s and %s share the key %d", name, other, key)
			seen[key] = name
		}
	})
})
