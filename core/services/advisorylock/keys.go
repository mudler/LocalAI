package advisorylock

// Advisory lock keys for distributed coordination via PostgreSQL.
// These keys are global across the database — avoid collisions with
// other applications sharing the same PostgreSQL instance.
const (
	// 100, 101 and 102 were the cron scheduler, the stale node cleanup and the
	// gallery dedup of earlier releases. Nothing takes them now; do not reuse
	// them while an older frontend can still run in the same database.

	// KeyAgentScheduler elects the one frontend that removes the agent jobs
	// whose retention has passed.
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
	// KeyChatsConnectors elects the one frontend that polls the bots of the chat
	// connectors. It is held for as long as the frontend lives.
	KeyChatsConnectors int64 = 112
	// KeyChatsConvert makes one frontend at a time convert the data of the old
	// chat, agents and skills. It is held for the length of one conversion.
	KeyChatsConvert int64 = 113
)
