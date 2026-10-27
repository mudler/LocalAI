//go:build auth

package application

import (
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/services/studio"
	"github.com/mudler/LocalAI/core/services/studio/provider"
	"github.com/mudler/LocalAI/core/services/studio/store"
	"github.com/mudler/LocalAI/core/services/studio/studiotest"
)

// The conversion of the old data can take long, and in a cluster it waits for
// a lock another frontend holds. It must never delay the start of the server,
// also when the collections service is off.
func TestConversionWithCollectionsOffDoesNotBlockStartup(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	old := runStudioConversion
	runStudioConversion = func(*Application) {
		close(started)
		<-release
	}
	t.Cleanup(func() { runStudioConversion = old })

	a := &Application{applicationConfig: &config.ApplicationConfig{DataPath: t.TempDir()}}
	a.applicationConfig.Collections.Enabled = false

	st, err := store.NewJSON("")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := studio.New(studio.Deps{Store: st, Provider: provider.New(), StateDir: t.TempDir(), Harness: studiotest.NewGateHarness()})
	if err != nil {
		t.Fatal(err)
	}

	returned := make(chan struct{})
	go func() {
		a.prepareStudioConversion(svc)
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("prepareStudioConversion waited for the conversion")
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the conversion never started")
	}
	close(release)
}
