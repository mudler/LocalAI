package p2p

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mudler/LocalAI/core/schema"
)

func TestNewNodeConfigFromEnvDefaults(t *testing.T) {
	// Ensure env vars are clean
	for _, k := range []string{
		"LOCALAI_P2P_DISABLE_DHT",
		"LOCALAI_P2P_ENABLE_LIMITS",
		"LOCALAI_P2P_LISTEN_MADDRS",
		"LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS",
		"LOCALAI_P2P_DHT_ANNOUNCE_MADDRS",
		"LOCALAI_P2P_LIB_LOGLEVEL",
	} {
		os.Unsetenv(k)
	}

	nc := NewNodeConfigFromEnv("test-token")
	if nc.Token != "test-token" {
		t.Errorf("Token = %q, want %q", nc.Token, "test-token")
	}
	if nc.DisableDHT {
		t.Error("DisableDHT default should be false")
	}
	if !nc.DisableLimits {
		t.Error("DisableLimits default should be true (inverted from LOCALAI_P2P_ENABLE_LIMITS != 'true')")
	}
	if nc.DiscoveryInterval != 10*time.Second {
		t.Errorf("DiscoveryInterval = %v, want 10s", nc.DiscoveryInterval)
	}
	if nc.DefaultSyncInterval != 10*time.Second {
		t.Errorf("DefaultSyncInterval = %v, want 10s", nc.DefaultSyncInterval)
	}
	if nc.MaxConnections != 1000 {
		t.Errorf("MaxConnections = %d, want 1000", nc.MaxConnections)
	}
	if nc.Libp2pLogLevel != "fatal" {
		t.Errorf("Libp2pLogLevel = %q, want 'fatal'", nc.Libp2pLogLevel)
	}
	if nc.ListenMaddrs != nil {
		t.Error("ListenMaddrs should be nil by default")
	}
	if nc.BootstrapPeers != nil {
		t.Error("BootstrapPeers should be nil by default")
	}
	if nc.DHTAnnounceMaddrs != nil {
		t.Error("DHTAnnounceMaddrs should be nil by default")
	}
}

func TestNewNodeConfigFromEnvOverrides(t *testing.T) {
	os.Setenv("LOCALAI_P2P_DISABLE_DHT", "true")
	os.Setenv("LOCALAI_P2P_ENABLE_LIMITS", "true")
	os.Setenv("LOCALAI_P2P_LISTEN_MADDRS", "/ip4/0.0.0.0/tcp/4001,/ip4/0.0.0.0/udp/4001/quic")
	os.Setenv("LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS", "/ip4/1.2.3.4/tcp/4001")
	os.Setenv("LOCALAI_P2P_DHT_ANNOUNCE_MADDRS", "/ip4/5.6.7.8/tcp/4001")
	os.Setenv("LOCALAI_P2P_LIB_LOGLEVEL", "debug")
	defer func() {
		os.Unsetenv("LOCALAI_P2P_DISABLE_DHT")
		os.Unsetenv("LOCALAI_P2P_ENABLE_LIMITS")
		os.Unsetenv("LOCALAI_P2P_LISTEN_MADDRS")
		os.Unsetenv("LOCALAI_P2P_BOOTSTRAP_PEERS_MADDRS")
		os.Unsetenv("LOCALAI_P2P_DHT_ANNOUNCE_MADDRS")
		os.Unsetenv("LOCALAI_P2P_LIB_LOGLEVEL")
	}()

	nc := NewNodeConfigFromEnv("override-token")
	if !nc.DisableDHT {
		t.Error("DisableDHT should be true")
	}
	if nc.DisableLimits {
		t.Error("DisableLimits should be false when LOCALAI_P2P_ENABLE_LIMITS=true (limits enabled)")
	}
	if len(nc.ListenMaddrs) != 2 {
		t.Errorf("ListenMaddrs len = %d, want 2", len(nc.ListenMaddrs))
	}
	if len(nc.BootstrapPeers) != 1 {
		t.Errorf("BootstrapPeers len = %d, want 1", len(nc.BootstrapPeers))
	}
	if len(nc.DHTAnnounceMaddrs) != 1 {
		t.Errorf("DHTAnnounceMaddrs len = %d, want 1", len(nc.DHTAnnounceMaddrs))
	}
	if nc.Libp2pLogLevel != "debug" {
		t.Errorf("Libp2pLogLevel = %q, want 'debug'", nc.Libp2pLogLevel)
	}
}

func TestNewNodeConfigFromEnvEnableLimitsFalse(t *testing.T) {
	// LOCALAI_P2P_ENABLE_LIMITS != "true" means limits are disabled
	os.Setenv("LOCALAI_P2P_ENABLE_LIMITS", "false")
	defer os.Unsetenv("LOCALAI_P2P_ENABLE_LIMITS")

	nc := NewNodeConfigFromEnv("limits-test")
	if !nc.DisableLimits {
		t.Error("DisableLimits should be true when enable_limits=false")
	}
}

func TestGenerateTokenNotEmpty(t *testing.T) {
	token := GenerateToken(30, 9000)
	if token == "" {
		t.Fatal("GenerateToken returned empty string")
	}
	if !strings.HasPrefix(token, "ey") {
		t.Log("Token does not start with 'ey' (base64 JSON), got:", token[:10]+"...")
	}
}

func TestGenerateConnectionDataDefaults(t *testing.T) {
	data := generateNewConnectionData(0, 0)
	if data == nil {
		t.Fatal("generateNewConnectionData returned nil")
	}
	if data.RoomName == "" {
		t.Error("RoomName is empty")
	}
	if data.MaxMessageSize != 20<<20 {
		t.Errorf("MaxMessageSize = %d, want %d", data.MaxMessageSize, 20<<20)
	}
}

func TestGenerateConnectionDataCustom(t *testing.T) {
	data := generateNewConnectionData(60, 18000)
	if data == nil {
		t.Fatal("generateNewConnectionData returned nil")
	}
	if data.OTP.DHT.Interval != 60 {
		t.Errorf("DHT interval = %d, want 60", data.OTP.DHT.Interval)
	}
	if data.OTP.Crypto.Interval != 18000 {
		t.Errorf("Crypto interval = %d, want 18000", data.OTP.Crypto.Interval)
	}
}

func TestNodeID(t *testing.T) {
	hostname, _ := os.Hostname()
	id := nodeID("worker")
	expected := hostname + "-worker"
	if id != expected {
		t.Errorf("nodeID = %q, want %q", id, expected)
	}
}

func TestStringsToMultiAddr(t *testing.T) {
	tests := []struct {
		name   string
		input  []string
		wantN  int
	}{
		{"nil slice", nil, 0},
		{"empty slice", []string{}, 0},
		{"valid single", []string{"/ip4/127.0.0.1/tcp/4001"}, 1},
		{"valid multiple", []string{"/ip4/127.0.0.1/tcp/4001", "/ip4/0.0.0.0/tcp/4002"}, 2},
		{"mix valid and invalid", []string{"/ip4/127.0.0.1/tcp/4001", "not-a-multiaddr"}, 1},
		{"all invalid", []string{"not-a-multiaddr", "also-not"}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stringsToMultiAddr(tt.input)
			if len(got) != tt.wantN {
				t.Errorf("got %d multiaddrs, want %d", len(got), tt.wantN)
			}
		})
	}
}

func TestAddAndGetNode(t *testing.T) {
	// Reset global state
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	nd := schema.NodeData{
		Name:          "test-node",
		ID:            "test-node-1",
		TunnelAddress: "127.0.0.1:8080",
		LastSeen:      time.Now(),
	}

	AddNode("test-service", nd)

	got, found := GetNode("test-service", "test-node-1")
	if !found {
		t.Fatal("GetNode returned not found")
	}
	if got.Name != "test-node" {
		t.Errorf("Name = %q, want %q", got.Name, "test-node")
	}
	if got.TunnelAddress != "127.0.0.1:8080" {
		t.Errorf("TunnelAddress = %q, want %q", got.TunnelAddress, "127.0.0.1:8080")
	}
}

func TestGetNodeNotFound(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	_, found := GetNode("nonexistent", "no-id")
	if found {
		t.Error("GetNode should return false for non-existent node")
	}
}

func TestGetNodeDefaultServiceID(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	nd := schema.NodeData{Name: "default-svc", ID: "default-id", LastSeen: time.Now()}
	AddNode("", nd)

	_, found := GetNode("", "default-id")
	if !found {
		t.Error("GetNode with empty serviceID should find node stored under defaultServicesID")
	}
}

func TestAddNodeDefaultServiceID(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	nd := schema.NodeData{Name: "node-a", ID: "id-a", LastSeen: time.Now()}
	AddNode("", nd)

	got, found := GetNode(defaultServicesID, "id-a")
	if !found {
		t.Fatal("Node stored with empty serviceID should be retrievable via defaultServicesID")
	}
	if got.Name != "node-a" {
		t.Errorf("Name = %q, want %q", got.Name, "node-a")
	}
}

func TestAddNodeMultiple(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	AddNode("svc", schema.NodeData{Name: "n1", ID: "id1", LastSeen: time.Now()})
	AddNode("svc", schema.NodeData{Name: "n2", ID: "id2", LastSeen: time.Now()})

	available := GetAvailableNodes("svc")
	if len(available) != 2 {
		t.Errorf("GetAvailableNodes returned %d nodes, want 2", len(available))
	}
}

func TestGetAvailableNodesEmptyServiceID(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	AddNode(defaultServicesID, schema.NodeData{Name: "n1", ID: "id1", LastSeen: time.Now()})

	available := GetAvailableNodes("")
	if len(available) != 1 {
		t.Errorf("GetAvailableNodes('') returned %d nodes, want 1", len(available))
	}
}

func TestGetAvailableNodesSortOrder(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	AddNode("svc", schema.NodeData{Name: "b-node", ID: "b-id", LastSeen: time.Now()})
	AddNode("svc", schema.NodeData{Name: "a-node", ID: "a-id", LastSeen: time.Now()})
	AddNode("svc", schema.NodeData{Name: "c-node", ID: "c-id", LastSeen: time.Now()})

	available := GetAvailableNodes("svc")
	if len(available) != 3 {
		t.Fatalf("got %d nodes, want 3", len(available))
	}
	if available[0].ID != "a-id" || available[1].ID != "b-id" || available[2].ID != "c-id" {
		t.Errorf("nodes not sorted by ID: got %v, %v, %v", available[0].ID, available[1].ID, available[2].ID)
	}
}

func TestReplaceNodes(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	// Add initial nodes
	AddNode("svc", schema.NodeData{Name: "old", ID: "old-id", LastSeen: time.Now()})

	// Replace with new set
	replacement := []schema.NodeData{
		{Name: "new1", ID: "new1-id", LastSeen: time.Now()},
		{Name: "new2", ID: "new2-id", LastSeen: time.Now()},
	}
	ReplaceNodes("svc", replacement)

	available := GetAvailableNodes("svc")
	if len(available) != 2 {
		t.Errorf("after replace: got %d nodes, want 2", len(available))
	}

	// Old node should be gone
	_, found := GetNode("svc", "old-id")
	if found {
		t.Error("old node should not exist after ReplaceNodes")
	}
}

func TestReplaceNodesDefaultServiceID(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	replacement := []schema.NodeData{
		{Name: "n1", ID: "id1", LastSeen: time.Now()},
	}
	ReplaceNodes("", replacement)

	available := GetAvailableNodes(defaultServicesID)
	if len(available) != 1 {
		t.Errorf("after ReplaceNodes with empty serviceID: got %d nodes, want 1", len(available))
	}
}

func TestReplaceNodesEmptySlice(t *testing.T) {
	mu.Lock()
	nodes = map[string]map[string]schema.NodeData{}
	mu.Unlock()

	AddNode("svc", schema.NodeData{Name: "n1", ID: "id1", LastSeen: time.Now()})
	ReplaceNodes("svc", []schema.NodeData{})

	available := GetAvailableNodes("svc")
	if len(available) != 0 {
		t.Errorf("after ReplaceNodes with empty slice: got %d nodes, want 0", len(available))
	}
}
