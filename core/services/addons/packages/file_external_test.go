// SPDX-License-Identifier: MIT
package packages

import (
	"context"
	"os"
	"testing"
)

// TestInspectExternalZip lets a script hand the installer a zip that was built
// outside this repository (scripts/addons/outoftree_test.sh). It runs only when
// ADDON_ZIP_UNDER_TEST names the file, and it takes the add-on id and version
// from ADDON_ID_UNDER_TEST and ADDON_VERSION_UNDER_TEST.
func TestInspectExternalZip(t *testing.T) {
	file := os.Getenv("ADDON_ZIP_UNDER_TEST")
	if file == "" {
		t.Skip("ADDON_ZIP_UNDER_TEST is not set")
	}
	sum, err := InspectFile(context.Background(), file, 0, 0)
	if err != nil {
		t.Fatalf("the installer refuses the zip: %v", err)
	}
	if want := os.Getenv("ADDON_ID_UNDER_TEST"); want != "" && sum.ID != want {
		t.Fatalf("add-on id %q, want %q", sum.ID, want)
	}
	if want := os.Getenv("ADDON_VERSION_UNDER_TEST"); want != "" && sum.Version != want {
		t.Fatalf("add-on version %q, want %q", sum.Version, want)
	}
	if sum.Package.Executable == "" {
		t.Fatal("the manifest names no executable")
	}
}
