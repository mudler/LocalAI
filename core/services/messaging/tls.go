package messaging

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
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
	// CAPEM is a private CA in memory, as a PEM bundle. A worker that the
	// frontend hands the CA to has no file for it. A path in CA is used as well
	// when both are set.
	CAPEM []byte
}

// Enabled reports whether any TLS file path is configured.
func (f TLSFiles) Enabled() bool {
	return f.CA != "" || f.Cert != "" || f.Key != "" || len(f.CAPEM) > 0
}

// Validate checks path pairing and that files exist.
func (f TLSFiles) Validate() error {
	if f.Cert != "" && f.Key == "" {
		return fmt.Errorf("LOCALAI_NATS_TLS_KEY is required when LOCALAI_NATS_TLS_CERT is set")
	}
	if f.Key != "" && f.Cert == "" {
		return fmt.Errorf("LOCALAI_NATS_TLS_CERT is required when LOCALAI_NATS_TLS_KEY is set")
	}
	if len(f.CAPEM) > 0 {
		if _, err := caPool(f.CAPEM); err != nil {
			return err
		}
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
	if len(f.CAPEM) > 0 {
		pool, err := caPool(f.CAPEM)
		if err != nil {
			return nil, err
		}
		opts = append(opts, func(o *nats.Options) error {
			previous := o.RootCAsCB
			o.RootCAsCB = func() (*x509.CertPool, error) {
				if previous == nil {
					return pool, nil
				}
				// A file was given as well: trust both.
				both, err := previous()
				if err != nil {
					return nil, err
				}
				both.AppendCertsFromPEM(f.CAPEM)
				return both, nil
			}
			if o.TLSConfig == nil {
				o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
			}
			o.Secure = true
			return nil
		})
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

// caPool parses a PEM bundle of CA certificates.
func caPool(pem []byte) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("the NATS CA given as PEM holds no certificate")
	}
	return pool, nil
}
