// SPDX-License-Identifier: MIT
package config

import (
	"reflect"
	"testing"
)

func TestParseAddonHostOps(t *testing.T) {
	got, err := ParseAddonHostOps(DefaultAddonHostOps)
	want := map[string][]string{"fine-tuning": {"finetune."}, "quantization": {"quantize."}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%v %v", got, err)
	}
	got, err = ParseAddonHostOps(" a-b = x. , a-b=y. ,c=z.")
	if err != nil || !reflect.DeepEqual(got, map[string][]string{"a-b": {"x.", "y."}, "c": {"z."}}) {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := ParseAddonHostOps(""); err != nil || len(got) != 0 {
		t.Fatalf("an empty setting names nobody: %v %v", got, err)
	}
	for _, bad := range []string{"fine-tuning", "=finetune.", "fine-tuning=", "fine-tuning=finetune", "Fine=finetune.", "fine-tuning=Finetune.", "a/b=finetune."} {
		if _, err := ParseAddonHostOps(bad); err == nil {
			t.Errorf("%q: accepted", bad)
		}
	}
}

func TestWithAddonHostOpsReplacesTheDefault(t *testing.T) {
	o := &ApplicationConfig{}
	if o.AddonHostOps != nil {
		t.Fatal("the field starts unset")
	}
	WithAddonHostOps(map[string][]string{"x": {"y."}})(o)
	if len(o.AddonHostOps) != 1 {
		t.Fatalf("%v", o.AddonHostOps)
	}
}
