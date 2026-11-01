// SPDX-License-Identifier: MIT
package config_test

import (
	"github.com/mudler/LocalAI/core/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"reflect"
)

var _ = Describe("automatic workspace configuration", func() {
	It("uses existing DataPath and has no additional path or coordination setting", func() {
		cfg := config.NewApplicationConfig(config.WithDataPath("existing-data"))
		Expect(cfg.DataPath).To(Equal("existing-data"))
		_, exists := reflect.TypeOf(cfg).Elem().FieldByName("ChatsSharedFilesDir")
		Expect(exists).To(BeFalse())
		_, exists = reflect.TypeOf(config.RuntimeSettings{}).FieldByName("ChatsSharedFilesDir")
		Expect(exists).To(BeFalse())
	})
})
