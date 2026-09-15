package cluster

import "testing"

func TestConformanceStagingEnvironmentIsExplicit(t *testing.T) {
	if got := workerStagingEnv(Options{}); len(got) != 0 {
		t.Fatalf("default cluster changed worker staging environment: %v", got)
	}
	got := workerStagingEnv(Options{ConformanceStaging: true})
	if len(got) != 2 || got[0] != "LOCALAI_EPHEMERAL_STAGING_BYTE_LIMIT=1073741824" || got[1] != "LOCALAI_EPHEMERAL_STAGING_MIN_FREE_BYTES=1" {
		t.Fatalf("unexpected conformance staging environment: %v", got)
	}
}
