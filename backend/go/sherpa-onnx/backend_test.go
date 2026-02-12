package main

import (
	"testing"
)

func TestSherpaBackendStruct(t *testing.T) {
	b := &SherpaBackend{}
	if b.Locking() {
		t.Fatal("new backend should not be locking")
	}
}
