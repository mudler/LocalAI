//go:build !linux

// SPDX-License-Identifier: MIT
package application

import (
	"context"
	"path/filepath"

	"github.com/mudler/LocalAI/core/config"
	addonruntime "github.com/mudler/LocalAI/core/services/addons/runtime"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("addon owner where the runtime is unavailable", func() {
	It("fails closed instead of starting an owner without a process ledger", func() {
		owner := config.AddonConfig{Enabled: true, InstanceID: "owner", OwnerID: "owner", OwnerAddress: "https://owner.example", StorageID: "local", Root: filepath.Join(realTempDir(), "addons"), AdminCheck: func(context.Context, contract.Actor) error { return nil }}
		a := &Application{applicationConfig: &config.ApplicationConfig{Context: context.Background(), Addons: owner}}
		Expect(a.startAddons()).To(MatchError(addonruntime.ErrRuntime))
		Expect(a.AddonManager()).To(BeNil())
	})
})
