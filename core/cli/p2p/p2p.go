package cli

type P2PCommonFlags struct {
	Peer2PeerNoDHT            bool     `env:"LOCALAI_P2P_DISABLE_DHT,P2P_DISABLE_DHT" name:"p2p-disable-dht" help:"Disable DHT" group:"p2p"`
	Peer2PeerLimit            bool     `env:"LOCALAI_P2P_ENABLE_LIMITS,P2P_ENABLE_LIMITS" name:"p2p-enable-limits" help:"Enable Limits" group:"p2p"`
	Peer2PeerListenAddrs      []string `env:"LOCALAI_P2P_LISTEN_MADDRS,P2P_LISTEN_MADDRS" name:"p2p-listen-maddrs" help:"A list of listen multiaddresses" group:"p2p"`
	Peer2PeerBootAddrs        []string `env:"LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS,P2P_BOOTSTRAP_PEERS_MADDRS" name:"p2p-bootstrap-peers-maddrs" help:"A list of bootstrap peers multiaddresses" group:"p2p"`
	Peer2PeerDHTAnnounceAddrs []string `env:"LOCALAI_P2P_DHT_ANNOUNCE_MADDRS,P2P_DHT_ANNOUNCE_MADDRS" name:"p2p-dht-announce-maddrs" help:"A list of DHT announce maddrs" group:"p2p"`
	Peer2PeerLibLoglevel      string   `env:"LOCALAI_P2P_LIB_LOGLEVEL,P2P_LIB_LOGLEVEL" name:"p2p-lib-loglevel" help:"libp2p specific loglevel" group:"p2p"`
	Peer2PeerDHTInterval      int      `env:"LOCALAI_P2P_DHT_INTERVAL,P2P_DHT_INTERVAL" default:"360" name:"p2p-dht-interval" help:"Interval for DHT refresh (used during token generation)" group:"p2p"`
	Peer2PeerOTPInterval      int      `env:"LOCALAI_P2P_OTP_INTERVAL,P2P_OTP_INTERVAL" default:"9000" name:"p2p-otp-interval" help:"Interval for OTP refresh (used during token generation)" group:"p2p"`
	Peer2PeerToken            string   `env:"LOCALAI_P2P_TOKEN,P2P_TOKEN,TOKEN" name:"p2ptoken" help:"Token for P2P mode (optional)" group:"p2p"`
	Peer2PeerPrivkey          string   `env:"LOCALAI_P2P_PRIVKEY,P2P_PRIVKEY" name:"p2pprivkey" help:"A base64 encoded protobuf serialized private key used for fixed ID (edgevpn can be used for generating one)" group:"p2p"`
	Peer2PeerUsePeerguard     bool     `env:"LOCALAI_P2P_PEERGUARD,P2P_PEERGUARD" name:"p2ppeerguard" help:"Enable peerguarding through ecdsa authorization of nodes" group:"p2p"`
	Peer2PeerAuthProvders     string   `env:"LOCALAI_P2P_PEERGATE_AUTH,P2P_PEERGATE_AUTH" name:"p2pauth" help:"JSON dict string with '{authProviderName: {providerOpt: value}}' structure, see edgevpn project" group:"p2p"`
	Peer2PeerNetworkID        string   `env:"LOCALAI_P2P_NETWORK_ID,P2P_NETWORK_ID" help:"Network ID for P2P mode, can be set arbitrarly by the user for grouping a set of instances" group:"p2p"`
}
