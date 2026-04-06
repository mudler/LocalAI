package config

import (
	"slices"
	"strings"
	"testing"
)

func TestBackendCapabilities_AllHaveUsecases(t *testing.T) {
	for name, cap := range BackendCapabilities {
		if len(cap.PossibleUsecases) == 0 {
			t.Errorf("backend %q has no possible usecases", name)
		}
		if len(cap.DefaultUsecases) == 0 {
			t.Errorf("backend %q has no default usecases", name)
		}
		if len(cap.GRPCMethods) == 0 {
			t.Errorf("backend %q has no gRPC methods", name)
		}
	}
}

func TestBackendCapabilities_DefaultsSubsetOfPossible(t *testing.T) {
	for name, cap := range BackendCapabilities {
		for _, d := range cap.DefaultUsecases {
			if !slices.Contains(cap.PossibleUsecases, d) {
				t.Errorf("backend %q: default %q not in possible %v", name, d, cap.PossibleUsecases)
			}
		}
	}
}

func TestBackendCapabilities_UsecasesMatchFlags(t *testing.T) {
	allFlags := GetAllModelConfigUsecases()
	for name, cap := range BackendCapabilities {
		for _, u := range cap.PossibleUsecases {
			info, ok := UsecaseInfoMap[u]
			if !ok {
				t.Errorf("backend %q: usecase %q not in UsecaseInfoMap", name, u)
				continue
			}
			flagName := "FLAG_" + strings.ToUpper(u)
			if _, ok := allFlags[flagName]; !ok {
				// Try without transform — some names differ
				found := false
				for _, flag := range allFlags {
					if flag == info.Flag {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("backend %q: usecase %q flag %d not in GetAllModelConfigUsecases", name, u, info.Flag)
				}
			}
		}
	}
}

func TestUsecaseInfoMap_AllHaveFlags(t *testing.T) {
	for name, info := range UsecaseInfoMap {
		if info.Flag == FLAG_ANY {
			t.Errorf("usecase %q has FLAG_ANY (zero) — should have a real flag", name)
		}
		if info.GRPCMethod == "" {
			t.Errorf("usecase %q has no gRPC method", name)
		}
	}
}

func TestGetBackendCapability(t *testing.T) {
	cap := GetBackendCapability("llama-cpp")
	if cap == nil {
		t.Fatal("llama-cpp should be known")
	}
	if !slices.Contains(cap.PossibleUsecases, "chat") {
		t.Error("llama-cpp should support chat")
	}
}

func TestGetBackendCapability_Normalize(t *testing.T) {
	cap := GetBackendCapability("llama.cpp")
	if cap == nil {
		t.Fatal("llama.cpp should normalize to llama-cpp")
	}
}

func TestGetBackendCapability_Unknown(t *testing.T) {
	cap := GetBackendCapability("nonexistent")
	if cap != nil {
		t.Error("unknown backend should return nil")
	}
}

func TestIsValidUsecaseForBackend(t *testing.T) {
	if !IsValidUsecaseForBackend("piper", "tts") {
		t.Error("piper should support tts")
	}
	if IsValidUsecaseForBackend("piper", "chat") {
		t.Error("piper should not support chat")
	}
	// Unknown backend is permissive
	if !IsValidUsecaseForBackend("unknown", "anything") {
		t.Error("unknown backend should allow any usecase")
	}
}

func TestAllBackendNames(t *testing.T) {
	names := AllBackendNames()
	if len(names) < 30 {
		t.Errorf("expected 30+ backends, got %d", len(names))
	}
	if !slices.IsSorted(names) {
		t.Error("should be sorted")
	}
}
