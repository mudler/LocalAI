// SPDX-License-Identifier: MIT
package config

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sync"
)

var _ = Describe("shared selection ownership", func() {
	It("shares synchronized selection ownership across config value copies", func() {
		o := NewApplicationConfig()
		_ = o.ChatSelectionSnapshot()
		copied := ApplicationConfig{chatSelection: o.selectionState()}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 100 {
				WithChatsHostExec("on")(&copied)
				WithChatsHostExec("off")(&copied)
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				_ = o.ChatSelectionSnapshot()
			}
		}()
		wg.Wait()
		Expect(o.ChatSelectionSnapshot().HostExec).To(Equal("off"))
	})
})
