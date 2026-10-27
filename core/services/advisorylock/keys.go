package advisorylock

// Advisory lock keys for distributed coordination via PostgreSQL.
// These keys are global across the database — avoid collisions with
// other applications sharing the same PostgreSQL instance.
const (
	KeyCronScheduler       int64 = 100
	KeyStaleNodeCleanup    int64 = 101
	KeyGalleryDedup        int64 = 102
	KeyAgentScheduler      int64 = 103
	KeyHealthCheck         int64 = 104
	KeySchemaMigrate       int64 = 105
	KeyBackendUpgradeCheck int64 = 106
	KeyStateReconciler     int64 = 107
	// KeyFailoverProber elects the one frontend that probes failover
	// targets, decides chains and preloads warm targets.
	KeyFailoverProber int64 = 108
	// KeyAddonPinSweep elects the one frontend that expires the pins of
	// removed add-ons.
	KeyAddonPinSweep int64 = 109
	// KeyChatsReaper elects the one frontend that ends the chat turns whose
	// frontend stopped beating.
	KeyChatsReaper int64 = 110
	// KeyChatsTriggers elects the one frontend that runs the triggers of the
	// chats. It is held for as long as the frontend lives.
	KeyChatsTriggers int64 = 111
)
