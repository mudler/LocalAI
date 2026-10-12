// SPDX-License-Identifier: MIT
package launcher

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// launchDescription is a value snapshot: accessors never return its backing slices.
// It is not permission to start a worker: runtime/ownership gates are still absent.
type launchDescription struct {
	args, env, secrets []string
	dir, credentials   string
}

func (d launchDescription) Args() []string         { return append([]string(nil), d.args...) }
func (d launchDescription) Env() []string          { return append([]string(nil), d.env...) }
func (d launchDescription) Dir() string            { return d.dir }
func (d launchDescription) CredentialMode() string { return d.credentials }

// Prevent accidental diagnostic formatting of the child environment.
func (d launchDescription) String() string   { return "launcher description (redacted)" }
func (d launchDescription) GoString() string { return d.String() }

// Includes worker.Config aliases, global CLI path/role aliases and the reserved
// launcher observability namespace. Non-reserved accelerator options survive.
const reservedLaunchKeys = `LOCALAI_ADDR LOCALAI_SERVE_ADDR LOCALAI_HTTP_ADDR LOCALAI_ADVERTISE_ADDR LOCALAI_ADVERTISE_HTTP_ADDR LOCALAI_GRPC_MAX_PORT LOCALAI_REGISTER_TO LOCALAI_NODE_NAME LOCALAI_REGISTRATION_TOKEN LOCALAI_REGISTRATION_REQUIRE_AUTH LOCALAI_DISTRIBUTED_REQUIRE_AUTH LOCALAI_NATS_REQUIRE_AUTH LOCALAI_NATS_URL LOCALAI_NATS_JWT LOCALAI_NATS_USER_SEED LOCALAI_NATS_TLS_CA LOCALAI_NATS_TLS_CERT LOCALAI_NATS_TLS_KEY LOCALAI_MODELS_PATH MODELS_PATH LOCALAI_BACKENDS_PATH BACKENDS_PATH LOCALAI_BACKENDS_SYSTEM_PATH LOCALAI_ADDRESS ADDRESS LOCALAI_DATA_PATH DATA_PATH LOCALAI_CONFIG_DIR LOCALAI_GENERATED_CONTENT_PATH GENERATED_CONTENT_PATH LOCALAI_UPLOAD_PATH UPLOAD_PATH LOCALAI_P2P_TOKEN P2P_TOKEN LOCALAI_P2P_NETWORK_ID LOCALAI_P2P LOCALAI_WORKER LOCALAI_ROLE LOCALAI_HEARTBEAT_INTERVAL LOCALAI_LOG_LEVEL LOG_LEVEL LOCALAI_DEBUG DEBUG LOCALAI_CONFIG_PATH CONFIG_PATH LOCALAI_LOCALAI_CONFIG_DIR LOCALAI_P2P_DISABLE_DHT LOCALAI_TOKEN TOKEN BACKEND_SYSTEM_PATH LOCALAI_DOWNLOAD_STAGING_PATH DOWNLOAD_STAGING_PATH P2P P2P_NETWORK_ID P2P_DHT_INTERVAL P2P_OTP_INTERVAL LOCALAI_P2P_DHT_INTERVAL LOCALAI_P2P_OTP_INTERVAL`

func reservedLaunchKey(k string) bool {
	return strings.HasPrefix(k, "LOCALAI_LAUNCHER_") || strings.Contains(" "+reservedLaunchKeys+" ", " "+k+" ")
}
func controls(s string) bool { return strings.ContainsFunc(s, unicode.IsControl) }
func decimalPort(s string) (int, error) {
	if s == "" || strings.Trim(s, "0123456789") != "" {
		return 0, fmt.Errorf("invalid decimal port")
	}
	n, e := strconv.Atoi(s)
	if e != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("port outside 1..65535")
	}
	return n, nil
}
func validHost(s string) bool {
	if ip := net.ParseIP(s); ip != nil {
		return !ip.IsUnspecified()
	}
	if len(s) > 253 || s == "" {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}
func launchURL(raw string, nats bool) (string, error) {
	bad := fmt.Errorf("invalid connection URL; check scheme, host and port; credentials, query and fragment are forbidden")
	if strings.ContainsAny(raw, "?#") || strings.ContainsFunc(raw, unicode.IsSpace) || controls(raw) {
		return "", bad
	}
	u, e := url.Parse(raw)
	if e != nil || u.Opaque != "" || u.User != nil || !validHost(u.Hostname()) || strings.ContainsFunc(u.Path, unicode.IsSpace) || controls(u.Path) || strings.Contains(u.Host, "%") {
		return "", bad
	}
	if nats {
		if (u.Scheme != "tls" && u.Scheme != "nats") || (u.Path != "" && u.Path != "/") {
			return "", bad
		}
	} else if u.Scheme != "https" && u.Scheme != "http" {
		return "", bad
	}
	if strings.HasSuffix(u.Host, ":") {
		return "", bad
	}
	if u.Port() != "" {
		if _, e = decimalPort(u.Port()); e != nil {
			return "", bad
		}
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}
func buildLaunch(input *Config, dataPath string, inherited []string, plaintextAcknowledged bool) (launchDescription, error) {
	d := launchDescription{}
	fail := func(s string) (launchDescription, error) { return launchDescription{}, fmt.Errorf("%s", s) }
	c := input.clone()
	if c == nil {
		return fail("launcher settings missing")
	}
	if c.SchemaVersion != 0 && c.SchemaVersion != 2 {
		return fail("unsupported launcher settings version")
	}
	worker := c.Mode == "worker"
	if !worker && c.Mode != "" && c.Mode != "standalone" {
		return fail("unknown launcher mode")
	}
	profile := c.EnvironmentVars
	if worker {
		profile = c.Worker.EnvironmentVars
	}
	env := map[string]string{}
	for _, entry := range inherited {
		k, v, ok := strings.Cut(entry, "=")
		if ok && (!worker || !reservedLaunchKey(k)) {
			env[k] = v
		}
	}
	for k, v := range profile {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return fail("invalid advanced environment entry")
		}
		if worker && reservedLaunchKey(k) {
			return fail("reserved worker environment override; use connection settings")
		}
		env[k] = v
	}
	if !worker {
		d.args = standaloneArgs(c, dataPath)
	} else {
		w := c.Worker
		a := w.Connection
		n := w.NATS
		front, e := launchURL(a.FrontendURL, false)
		if e != nil {
			return d, e
		}
		broker, e := launchURL(n.URL, true)
		if e != nil {
			return d, e
		}
		if !plaintextAcknowledged && (strings.HasPrefix(front, "http:") || strings.HasPrefix(broker, "nats:")) {
			return fail("plaintext connections require protected-network acknowledgement")
		}
		host, p, e := net.SplitHostPort(a.AdvertisedAddress)
		if e != nil || !validHost(host) {
			return fail("invalid advertised host:port")
		}
		base, e := decimalPort(p)
		if e != nil || base < 2 {
			return fail("worker base port must be 2..65535")
		}
		if a.GRPCMaxPort != nil && (*a.GRPCMaxPort < base || *a.GRPCMaxPort > 65535) {
			return fail("maximum gRPC port must be base..65535")
		}
		if strings.TrimSpace(a.RegistrationToken) == "" || controls(a.RegistrationToken) || controls(a.NodeName) || controls(n.JWT) || controls(n.UserSeed) {
			return fail("registration token required; names and credentials must not contain controls")
		}
		if (n.JWT == "") != (n.UserSeed == "") {
			return fail("static NATS credentials require both JWT and user seed")
		}
		d.credentials = "registration-issued"
		if n.JWT != "" {
			claims, e := jwt.DecodeUserClaims(n.JWT)
			if e != nil {
				return fail("invalid static NATS user JWT")
			}
			kp, e := nkeys.FromSeed([]byte(n.UserSeed))
			if e != nil {
				return fail("invalid static NATS user seed")
			}
			defer kp.Wipe()
			pub, e := kp.PublicKey()
			if e != nil || !nkeys.IsValidPublicUserKey(pub) || claims.Subject != pub {
				return fail("static NATS JWT and user seed do not match")
			}
			vr := jwt.CreateValidationResults()
			claims.Validate(vr)
			if vr.IsBlocking(true) {
				return fail("static NATS user JWT is expired or invalid")
			}
			d.credentials = "static (no automatic renewal)"
			env["LOCALAI_NATS_JWT"] = n.JWT
			env["LOCALAI_NATS_USER_SEED"] = n.UserSeed
		}
		if (n.TLSCert == "") != (n.TLSKey == "") {
			return fail("NATS client certificate and key must be paired")
		}
		if (n.TLSCA != "" || n.TLSCert != "") && !strings.HasPrefix(broker, "tls:") {
			return fail("NATS TLS files require a tls:// endpoint")
		}
		for _, p := range []string{c.ModelsPath, c.BackendsPath, dataPath} {
			if strings.TrimSpace(p) == "" || controls(p) {
				return fail("worker storage paths must be nonempty and contain no controls")
			}
		}
		if c.LogLevel != "" && c.LogLevel != "error" && c.LogLevel != "warn" && c.LogLevel != "info" && c.LogLevel != "debug" && c.LogLevel != "trace" {
			return fail("invalid worker log level")
		}
		d.dir = dataPath
		d.args = []string{"worker", "--register-to", front, "--nats-url", broker, "--addr", a.AdvertisedAddress, "--models-path", c.ModelsPath, "--backends-path", c.BackendsPath, "--distributed-require-auth"}
		if c.LogLevel != "" {
			d.args = append(d.args, "--log-level", c.LogLevel)
		}
		if a.NodeName != "" {
			d.args = append(d.args, "--node-name", a.NodeName)
		}
		if a.GRPCMaxPort != nil {
			d.args = append(d.args, "--grpc-max-port", strconv.Itoa(*a.GRPCMaxPort))
		}
		for _, f := range []struct{ flag, path string }{{"--nats-tls-ca", n.TLSCA}, {"--nats-tls-cert", n.TLSCert}, {"--nats-tls-key", n.TLSKey}} {
			if f.path == "" {
				continue
			}
			if controls(f.path) {
				return fail("invalid NATS TLS file")
			}
			// Resolve against the launcher CWD before validation: the child uses
			// dataPath as its CWD and must open the same path we checked.
			absolutePath, e := filepath.Abs(f.path)
			if e != nil {
				return fail("invalid NATS TLS file")
			}
			f.path = absolutePath
			st, e := os.Stat(f.path)
			if e != nil || !st.Mode().IsRegular() {
				return fail("NATS TLS file must be readable and regular")
			}
			file, e := os.Open(f.path)
			if e != nil {
				return fail("NATS TLS file must be readable and regular")
			}
			st, e = file.Stat()
			file.Close()
			if e != nil || !st.Mode().IsRegular() {
				return fail("NATS TLS file must be readable and regular")
			}
			d.args = append(d.args, f.flag, f.path)
		}
		env["LOCALAI_REGISTRATION_TOKEN"] = a.RegistrationToken
	}
	// Snapshot values for redaction, including non-reserved secrets supplied by users.
	d.secrets = []string{c.Worker.Connection.RegistrationToken, c.Worker.NATS.JWT, c.Worker.NATS.UserSeed}
	keys := make([]string, 0, len(env))
	for k, v := range env {
		keys = append(keys, k)
		key := strings.ToUpper(k)
		if v != "" && (strings.Contains(key, "TOKEN") || strings.Contains(key, "SECRET") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "JWT") || strings.Contains(key, "SEED") || strings.Contains(key, "API_KEY")) {
			d.secrets = append(d.secrets, v)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		d.env = append(d.env, k+"="+env[k])
	}
	return d, nil
}
func standaloneArgs(c *Config, dataPath string) []string {
	return []string{"run", "--models-path", c.ModelsPath, "--backends-path", c.BackendsPath, "--address", c.Address, "--log-level", c.LogLevel, "--data-path", filepath.Join(dataPath, "data"), "--localai-config-dir", filepath.Join(dataPath, "configuration"), "--generated-content-path", filepath.Join(dataPath, "generated"), "--upload-path", filepath.Join(dataPath, "uploads")}
}
