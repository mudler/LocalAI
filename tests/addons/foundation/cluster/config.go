// SPDX-License-Identifier: MIT

// Package cluster builds the foundation acceptance fixture: one add-on owner
// and two non-owner frontends, each a separate UI test server process with its
// own listener, sharing one account database and speaking the real mTLS
// carrier. The fixture add-on is the protocol fixture binary; nothing here
// imports a Tables package.
package cluster

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/services/addons/gateway"
)

// Owner and the two frontends are the fixed instance names.
const (
	Owner  = "owner"
	FrontA = "front-a"
	FrontB = "front-b"
)

// Instances lists every instance name in start order.
var Instances = []string{Owner, FrontA, FrontB}

// Secret is the API key HMAC secret every instance shares.
const Secret = "foundation-fixture-secret"

// Instance is one process of the fixture.
type Instance struct {
	Name        string `json:"name"`
	HTTPPort    int    `json:"httpPort"`
	CarrierAddr string `json:"carrierAddr"`
	// Dir holds the instance's models, data and logs. Root is the add-on root,
	// which only the owner opens.
	Dir  string `json:"dir"`
	Root string `json:"root"`
	Cert string `json:"cert"`
	Key  string `json:"key"`
	Sign string `json:"sign"`
}

// Config is the cluster.json every process reads.
type Config struct {
	Dir         string              `json:"dir"`
	AuthDB      string              `json:"authDb"`
	Control     string              `json:"control"`
	ControlAddr string              `json:"controlAddr"`
	CatalogURL  string              `json:"catalogUrl"`
	Credentials string              `json:"credentials"`
	CA          string              `json:"ca"`
	Pins        map[string]string   `json:"pins"`
	Verify      map[string]string   `json:"verify"`
	Nodes       map[string]Instance `json:"nodes"`
	// ArtifactExpiryMillis is the lifetime of a private file result in
	// milliseconds; zero is the product default. A test that needs expiry
	// sets it and saves the configuration before the cluster starts.
	ArtifactExpiryMillis int64 `json:"artifactExpiryMillis,omitempty"`
	// Alias names a derived configuration (see Derive); it only changes the
	// file the configuration is saved to.
	Alias string `json:"alias,omitempty"`
}

// Derive copies the configuration under another file name with the ports of
// the owner replaced, so a second owner can be started on the same storage.
func (c *Config) Derive(alias string) (*Config, error) {
	d := *c
	d.Alias = alias
	d.Nodes = map[string]Instance{}
	for k, v := range c.Nodes {
		d.Nodes[k] = v
	}
	owner := d.Nodes[Owner]
	var err error
	if owner.HTTPPort, err = freePort(); err != nil {
		return nil, err
	}
	// The carrier address stays: it is what the frontends dial, so the second
	// owner would need the same listener and must fail before it gets it.
	d.Nodes[Owner] = owner
	if d.ControlAddr, err = freeAddr(); err != nil {
		return nil, err
	}
	releasePorts()
	return &d, d.Save()
}

func freeAddr() (string, error) {
	p, err := freePort()
	return fmt.Sprintf("127.0.0.1:%d", p), err
}

// Generate writes a fresh cluster configuration under dir. Ports are chosen
// by the operating system and released again; the processes bind them later.
func Generate(dir, catalogURL string) (*Config, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "foundation CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		return nil, err
	}
	if ca, err = x509.ParseCertificate(der); err != nil {
		return nil, err
	}
	control := make([]byte, 16)
	_, _ = rand.Read(control)
	controlPort, err := freePort()
	if err != nil {
		return nil, err
	}
	c := &Config{
		ControlAddr: fmt.Sprintf("127.0.0.1:%d", controlPort),
		Dir:         dir, AuthDB: filepath.Join(dir, "auth.db"), Control: hex.EncodeToString(control), CatalogURL: catalogURL,
		Credentials: filepath.Join(dir, "credentials.json"),
		CA:          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		Pins:        map[string]string{}, Verify: map[string]string{}, Nodes: map[string]Instance{},
	}
	for i, name := range Instances {
		leafPub, leafKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		leaf := &x509.Certificate{SerialNumber: big.NewInt(int64(i + 2)), Subject: pkix.Name{CommonName: name}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
		leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, leafPub, key)
		if err != nil {
			return nil, err
		}
		keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
		if err != nil {
			return nil, err
		}
		signPub, signKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, err
		}
		pin := sha256.Sum256(leafDER)
		c.Pins[name] = hex.EncodeToString(pin[:])
		c.Verify[name] = hex.EncodeToString(signPub)
		httpPort, err := freePort()
		if err != nil {
			return nil, err
		}
		carrier := ""
		if name == Owner {
			p, err := freePort()
			if err != nil {
				return nil, err
			}
			carrier = fmt.Sprintf("127.0.0.1:%d", p)
		}
		node := Instance{
			Name: name, HTTPPort: httpPort, CarrierAddr: carrier, Dir: filepath.Join(dir, name), Root: filepath.Join(dir, name, "addons"),
			Cert: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})),
			Key:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})),
			Sign: hex.EncodeToString(signKey),
		}
		if err := os.MkdirAll(node.Dir, 0o700); err != nil {
			return nil, err
		}
		c.Nodes[name] = node
	}
	releasePorts()
	return c, c.Save()
}

// held keeps every chosen port bound until the configuration is complete, so
// that two choices can never return the same number.
var (
	heldMu sync.Mutex
	held   []net.Listener
)

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	heldMu.Lock()
	held = append(held, l)
	heldMu.Unlock()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// releasePorts frees the chosen ports for the processes that will bind them.
func releasePorts() {
	heldMu.Lock()
	defer heldMu.Unlock()
	for _, l := range held {
		_ = l.Close()
	}
	held = nil
}

// Path is where cluster.json lives.
func (c *Config) Path() string {
	if c.Alias != "" {
		return filepath.Join(c.Dir, "cluster-"+c.Alias+".json")
	}
	return filepath.Join(c.Dir, "cluster.json")
}

// Save writes cluster.json.
func (c *Config) Save() error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path(), b, 0o600)
}

// Load reads a cluster.json.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path) // #nosec G304 -- test harness reads files it created in its own work directory
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// OwnerAddress is the address every instance dials for the owner.
func (c *Config) OwnerAddress() string { return "https://" + c.Nodes[Owner].CarrierAddr }

// Carrier builds the carrier identity of one instance.
func (c *Config) Carrier(name string) (*gateway.CarrierConfig, error) {
	node, ok := c.Nodes[name]
	if !ok {
		return nil, fmt.Errorf("unknown instance %q", name)
	}
	pair, err := tls.X509KeyPair([]byte(node.Cert), []byte(node.Key))
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM([]byte(c.CA)) {
		return nil, fmt.Errorf("invalid CA")
	}
	signRaw, err := hex.DecodeString(node.Sign)
	if err != nil {
		return nil, err
	}
	pins := map[string][32]byte{}
	for id, h := range c.Pins {
		raw, err := hex.DecodeString(h)
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("invalid pin")
		}
		var pin [32]byte
		copy(pin[:], raw)
		pins[id] = pin
	}
	keys := map[string]ed25519.PublicKey{}
	for id, h := range c.Verify {
		raw, err := hex.DecodeString(h)
		if err != nil {
			return nil, err
		}
		keys[id] = ed25519.PublicKey(raw)
	}
	return &gateway.CarrierConfig{
		InstanceID: name, OwnerID: Owner, OwnerAddress: c.OwnerAddress(),
		Certificate: pair, Roots: roots, Pins: pins, SigningKey: ed25519.PrivateKey(signRaw), VerificationKeys: keys,
	}, nil
}
