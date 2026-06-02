package messaging

import (
	"fmt"
	"os"

	"github.com/nats-io/nats.go"
)

// TLSFiles holds PEM paths for NATS TLS / mTLS. Cert and key must be set together.
// Use tls:// in LOCALAI_NATS_URL; CA and client cert paths are optional extras.
type TLSFiles struct {
	CA   string // LOCALAI_NATS_TLS_CA — private CA for server verification
	Cert string // LOCALAI_NATS_TLS_CERT — client certificate (mTLS)
	Key  string // LOCALAI_NATS_TLS_KEY — client private key
}

// Enabled reports whether any TLS file path is configured.
func (f TLSFiles) Enabled() bool {
	return f.CA != "" || f.Cert != "" || f.Key != ""
}

// Validate checks path pairing and that files exist.
func (f TLSFiles) Validate() error {
	if f.Cert != "" && f.Key == "" {
		return fmt.Errorf("LOCALAI_NATS_TLS_KEY is required when LOCALAI_NATS_TLS_CERT is set")
	}
	if f.Key != "" && f.Cert == "" {
		return fmt.Errorf("LOCALAI_NATS_TLS_CERT is required when LOCALAI_NATS_TLS_KEY is set")
	}
	for _, path := range []struct {
		name, path string
	}{
		{"LOCALAI_NATS_TLS_CA", f.CA},
		{"LOCALAI_NATS_TLS_CERT", f.Cert},
		{"LOCALAI_NATS_TLS_KEY", f.Key},
	} {
		if path.path == "" {
			continue
		}
		if _, err := os.Stat(path.path); err != nil {
			return fmt.Errorf("%s: %w", path.name, err)
		}
	}
	return nil
}

// natsOptions builds nats-go TLS options. Call Validate first.
func (f TLSFiles) natsOptions() ([]nats.Option, error) {
	if !f.Enabled() {
		return nil, nil
	}
	opts := []nats.Option{nats.Secure()}
	if f.CA != "" {
		opts = append(opts, nats.RootCAs(f.CA))
	}
	if f.Cert != "" {
		opts = append(opts, nats.ClientCert(f.Cert, f.Key))
	}
	return opts, nil
}

// WithTLS configures CA and/or client certificate paths for the NATS connection.
func WithTLS(files TLSFiles) Option {
	return func(c *connectConfig) {
		c.tls = files
	}
}