// SPDX-License-Identifier: MIT
package cli

import (
	"reflect"
	"testing"

	"github.com/mudler/LocalAI/core/config"
)

func TestTheAddonHostOpsFlagDefaultIsTheDocumentedSetting(t *testing.T) {
	f, ok := reflect.TypeOf(RunCMD{}).FieldByName("AddonHostOps")
	if !ok {
		t.Fatal("the flag is missing")
	}
	if got := f.Tag.Get("default"); got != config.DefaultAddonHostOps {
		t.Fatalf("flag default %q differs from %q", got, config.DefaultAddonHostOps)
	}
	if f.Tag.Get("env") != "LOCALAI_ADDON_HOST_OPS" {
		t.Fatalf("env %q", f.Tag.Get("env"))
	}
}
