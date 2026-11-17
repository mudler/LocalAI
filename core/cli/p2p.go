package cli

// P2PCMD is the `local-ai p2p` command group: the gated relay, the relays a
// server uses, and the helpers for pairing and forwarding.
type P2PCMD struct {
	Relay   P2PRelayCMD   `cmd:"" help:"Run a gated relay: a small public node that carries the traffic of your paired servers and devices when they cannot reach each other directly"`
	Relays  P2PRelaysCMD  `cmd:"" help:"Manage the relays this server uses (add, remove, list, check, sync, claim)"`
	ID      P2PIDCMD      `cmd:"" name:"id" help:"Show the peer ID and addresses of this server"`
	Status  P2PStatusCMD  `cmd:"" help:"Show the P2P state of this server: mode, reachability and the path to each peer"`
	Pair    P2PPairCMD    `cmd:"" help:"Redeem an invite link on this machine and pair it with a server"`
	Forward P2PForwardCMD `cmd:"" help:"Forward a local port to a service of a paired peer"`
}

// P2PRelaysCMD manages the relays of a running server through its REST API.
type P2PRelaysCMD struct {
	Add    P2PRelaysAddCMD    `cmd:"" help:"Add a relay to this server"`
	Remove P2PRelaysRemoveCMD `cmd:"" help:"Remove a relay from this server"`
	List   P2PRelaysListCMD   `cmd:"" help:"List the relays of this server"`
	Check  P2PRelaysCheckCMD  `cmd:"" help:"Check that a relay is reachable and in sync"`
	Sync   P2PRelaysSyncCMD   `cmd:"" help:"Push the paired devices to a relay now"`
	Claim  P2PRelaysClaimCMD  `cmd:"" help:"Claim an unclaimed relay with the code it printed"`
}
