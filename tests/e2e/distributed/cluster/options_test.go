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

func TestAuthenticatedDeploymentEnvironmentIsExplicit(t *testing.T) {
	defaults := authenticatedDeploymentEnv(Options{})
	if len(defaults) != 1 || defaults[0] != "LOCALAI_AUTO_APPROVE_NODES=true" {
		t.Fatalf("default cluster auth environment changed: %v", defaults)
	}

	got := authenticatedDeploymentEnv(Options{
		RequireNodeApproval:    true,
		DistributedRequireAuth: true,
	})
	want := []string{
		"LOCALAI_AUTO_APPROVE_NODES=false",
		"LOCALAI_DISTRIBUTED_REQUIRE_AUTH=true",
	}
	if len(got) != len(want) {
		t.Fatalf("unexpected authenticated deployment environment: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("authenticated deployment environment[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
