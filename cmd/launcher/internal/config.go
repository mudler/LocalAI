// SPDX-License-Identifier: MIT
package launcher

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Config retains the standalone Go fields for existing launcher callers. The
// JSON representation is exclusively the versioned, separated profile contract.
type Config struct {
	SchemaVersion   int
	Mode            string
	ModelsPath      string
	BackendsPath    string
	Address         string
	AutoStart       *bool
	StartOnBoot     bool // Deprecated: inert compatibility field.
	LogLevel        string
	EnvironmentVars map[string]string
	ShowWelcome     *bool
	Legacy          LegacyConfig
	Worker          WorkerConfig
}

type LegacyConfig struct {
	StartOnBoot *bool `json:"start_on_boot,omitempty"`
}
type SharedConfig struct {
	ModelsPath   string `json:"models_path"`
	BackendsPath string `json:"backends_path"`
	LogLevel     string `json:"log_level"`
	AutoStart    *bool  `json:"auto_start_server"`
	ShowWelcome  *bool  `json:"show_welcome"`
}
type StandaloneConfig struct {
	Address         string            `json:"address"`
	EnvironmentVars map[string]string `json:"environment_vars"`
}
type WorkerConfig struct {
	Connection      WorkerConnectionConfig `json:"connection"`
	NATS            WorkerNATSConfig       `json:"nats"`
	EnvironmentVars map[string]string      `json:"environment_vars"`
}
type WorkerConnectionConfig struct {
	FrontendURL       string `json:"frontend_url"`
	RegistrationToken string `json:"registration_token"`
	AdvertisedAddress string `json:"advertised_address"`
	NodeName          string `json:"node_name"`
	GRPCMaxPort       *int   `json:"grpc_max_port,omitempty"`
}
type WorkerNATSConfig struct {
	URL      string `json:"url"`
	TLSCA    string `json:"tls_ca"`
	TLSCert  string `json:"tls_cert"`
	TLSKey   string `json:"tls_key"`
	JWT      string `json:"jwt"`
	UserSeed string `json:"user_seed"`
}
type configDocument struct {
	SchemaVersion int              `json:"schema_version"`
	Mode          string           `json:"mode"`
	Legacy        LegacyConfig     `json:"legacy"`
	Shared        SharedConfig     `json:"shared"`
	Standalone    StandaloneConfig `json:"standalone"`
	Worker        WorkerConfig     `json:"worker"`
}

func (c Config) MarshalJSON() ([]byte, error) {
	if c.SchemaVersion != 0 && c.SchemaVersion != 2 {
		return nil, fmt.Errorf("unsupported launcher settings version; upgrade the launcher")
	}
	mode := c.Mode
	if mode == "" {
		mode = "standalone"
	}
	if mode != "standalone" && mode != "worker" {
		return nil, fmt.Errorf("unknown launcher mode; select a supported mode in settings")
	}
	legacy := c.Legacy
	if legacy.StartOnBoot == nil && c.StartOnBoot {
		value := true
		legacy.StartOnBoot = &value
	}
	return json.Marshal(configDocument{2, mode, legacy,
		SharedConfig{c.ModelsPath, c.BackendsPath, c.LogLevel, c.AutoStart, c.ShowWelcome},
		StandaloneConfig{c.Address, c.EnvironmentVars}, c.Worker})
}

func (c *Config) UnmarshalJSON(data []byte) error {
	var d configDocument
	if err := json.Unmarshal(data, &d); err != nil {
		return fmt.Errorf("invalid launcher settings JSON; repair launcher.json or restore a backup")
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("launcher settings must be a JSON object")
	}
	if d.SchemaVersion != 0 && d.SchemaVersion != 1 && d.SchemaVersion != 2 {
		return fmt.Errorf("unsupported launcher settings version; upgrade the launcher")
	}
	if d.Mode != "" && d.Mode != "standalone" && d.Mode != "worker" {
		return fmt.Errorf("unknown launcher mode; select a supported mode in settings")
	}
	if d.SchemaVersion < 2 {
		var legacy struct {
			SharedConfig
			StandaloneConfig
			StartOnBoot *bool `json:"start_on_boot"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return fmt.Errorf("invalid legacy launcher settings; repair launcher.json or restore a backup")
		}
		d.Shared, d.Standalone = legacy.SharedConfig, legacy.StandaloneConfig
		d.Legacy.StartOnBoot = legacy.StartOnBoot
	}
	if d.Mode == "" {
		d.Mode = "standalone"
	}
	next := Config{SchemaVersion: 2, Mode: d.Mode, Legacy: d.Legacy, Worker: d.Worker,
		ModelsPath: d.Shared.ModelsPath, BackendsPath: d.Shared.BackendsPath, LogLevel: d.Shared.LogLevel,
		AutoStart: d.Shared.AutoStart, ShowWelcome: d.Shared.ShowWelcome,
		Address: d.Standalone.Address, EnvironmentVars: d.Standalone.EnvironmentVars}
	if d.Legacy.StartOnBoot != nil {
		next.StartOnBoot = *d.Legacy.StartOnBoot
	}
	*c = next
	return nil
}

func (c *Config) clone() *Config {
	if c == nil {
		return nil
	}
	next := *c
	copyBool := func(p *bool) *bool {
		if p == nil {
			return nil
		}
		v := *p
		return &v
	}
	copyMap := func(m map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		n := make(map[string]string, len(m))
		for k, v := range m {
			n[k] = v
		}
		return n
	}
	next.AutoStart, next.ShowWelcome = copyBool(c.AutoStart), copyBool(c.ShowWelcome)
	next.Legacy.StartOnBoot = copyBool(c.Legacy.StartOnBoot)
	next.EnvironmentVars = copyMap(c.EnvironmentVars)
	next.Worker.EnvironmentVars = copyMap(c.Worker.EnvironmentVars)
	if c.Worker.Connection.GRPCMaxPort != nil {
		v := *c.Worker.Connection.GRPCMaxPort
		next.Worker.Connection.GRPCMaxPort = &v
	}
	return &next
}
