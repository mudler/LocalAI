package advisorylock

// Advisory lock keys for distributed coordination via PostgreSQL.
// These keys are global across the database — avoid collisions with
// other applications sharing the same PostgreSQL instance.
const (
	KeyCronScheduler    int64 = 100
	KeyStaleNodeCleanup int64 = 101
	KeyGalleryDedup     int64 = 102
	KeyAgentScheduler   int64 = 103
	KeyHealthCheck      int64 = 104
	KeySchemaMigrate        int64 = 105
	KeyBackendUpgradeCheck  int64 = 106
	KeyStateReconciler      int64 = 107
)
