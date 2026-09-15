package cluster

import "testing"

func TestConformanceStagingEnvironmentIsExplicit(t *testing.T) {
	if got := workerStagingEnv(Options{}, "/worker"); len(got) != 0 {
		t.Fatalf("default cluster changed worker staging environment: %v", got)
	}
	got := workerStagingEnv(Options{ConformanceStaging: true}, "/worker")
	if len(got) != 3 || got[0] != "LOCALAI_EPHEMERAL_STAGING_BYTE_LIMIT=1073741824" || got[1] != "LOCALAI_EPHEMERAL_STAGING_MIN_FREE_BYTES=1" || got[2] != "LOCALAI_MOCK_EXPECT_STAGING_ROOT=/worker" {
		t.Fatalf("unexpected conformance staging environment: %v", got)
	}
}
