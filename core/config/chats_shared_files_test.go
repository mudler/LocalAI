// SPDX-License-Identifier: MIT
package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"reflect"
)

var _ = Describe("shared filesystem configuration", func() {
	It("is opt-in and not a persisted runtime setting", func() {
		cfg := config.NewApplicationConfig()
		Expect(cfg.ChatsSharedFilesDir).To(BeEmpty())
		cfg = config.NewApplicationConfig(config.WithChatsSharedFilesDir("/srv/workspace"))
		Expect(cfg.ChatsSharedFilesDir).To(Equal("/srv/workspace"))
		_, exists := reflect.TypeOf(config.RuntimeSettings{}).FieldByName("ChatsSharedFilesDir")
		Expect(exists).To(BeFalse())
		cfg.ApplyRuntimeSettingsAtStartup(&config.RuntimeSettings{})
		Expect(cfg.ChatsSharedFilesDir).To(Equal("/srv/workspace"))
	})
})
