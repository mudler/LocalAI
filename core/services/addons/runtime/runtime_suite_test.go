// SPDX-License-Identifier: MIT
package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/packages"
	"github.com/mudler/LocalAI/pkg/addons/contract"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestRuntime runs the addon runtime specs. The suite bootstrap lives apart
// from the supervisor specs, which are POSIX-only, so the client-only specs
// still run on the platforms that have no supervisor.
func TestRuntime(t *testing.T) {
	// The owner helper runs as this test binary with ADDON_OWNER_HELPER set:
	// it stands in for an add-on owner so a spec can kill it.
	if root := os.Getenv("ADDON_OWNER_HELPER"); root != "" {
		b, e := os.ReadFile(os.Getenv("ADDON_HELPER_RECEIPT"))
		if e != nil {
			os.Exit(20)
		}
		var p packages.PackageReceipt
		if json.Unmarshal(b, &p) != nil {
			os.Exit(21)
		}
		c := Config{Limits: contract.DefaultLimits(), ReadyTimeout: time.Second, StableHealth: time.Second}
		setStateRoot(&c, root)
		s, e := NewSupervisor(c)
		if e != nil {
			os.Exit(22)
		}
		i, e := s.Start(context.Background(), p, 1)
		if e != nil {
			os.Exit(23)
		}
		if os.WriteFile(filepath.Join(root, "helper-pid"), []byte(strconv.Itoa(i.PID)), 0600) != nil {
			os.Exit(24)
		}
		select {}
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "Addon runtime")
}

func setStateRoot(c *Config, root string) { c.StateRoot = root }
