package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/qdrant/go-client/qdrant"
)

type Config struct {
	Host           string
	Port           int
	APIKey         string
	UseTLS         bool
	TLSSkipVerify  bool
	TLSCACert      string
	Collection     string
	Distance       qdrant.Distance
	RequestTimeout time.Duration
}

func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func loadConfig(opts *pb.ModelOptions) (Config, error) {
	cfg := Config{
		Host:           "localhost",
		Port:           6334,
		Distance:       qdrant.Distance_Cosine,
		RequestTimeout: 5 * time.Second,
	}
	var apiKeyEnv string

	for _, o := range opts.GetOptions() {
		key, value, _ := strings.Cut(o, ":")
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		var err error
		switch key {
		case "addr":
			cfg.Host, cfg.Port, err = parseAddr(value)
		case "api_key":
			cfg.APIKey = value
		case "api_key_env":
			apiKeyEnv = value
		case "tls":
			cfg.UseTLS, err = strconv.ParseBool(value)
		case "tls_skip_verify":
			cfg.TLSSkipVerify, err = strconv.ParseBool(value)
		case "tls_ca_cert":
			cfg.TLSCACert = value
		case "collection":
			cfg.Collection = value
		case "distance_metric":
			cfg.Distance, err = parseDistance(value)
		case "request_timeout_ms":
			var ms uint64
			ms, err = strconv.ParseUint(value, 10, 31)
			if ms > 0 {
				cfg.RequestTimeout = time.Duration(ms) * time.Millisecond
			}
		}
		if err != nil {
			return Config{}, fmt.Errorf("qdrant-store: invalid option %q: %w", o, err)
		}
	}

	if cfg.TLSCACert != "" || cfg.TLSSkipVerify {
		cfg.UseTLS = true
	}

	if cfg.APIKey == "" && apiKeyEnv != "" {
		cfg.APIKey = os.Getenv(apiKeyEnv)
	}
	return cfg, nil
}

func parseAddr(addr string) (string, int, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if !strings.Contains(err.Error(), "missing port") {
			return "", 0, err
		}
		return strings.Trim(addr, "[]"), 6334, nil
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return "", 0, fmt.Errorf("bad port %q", port)
	}
	return host, p, nil
}

func parseDistance(name string) (qdrant.Distance, error) {
	for n, v := range qdrant.Distance_value {
		if v != int32(qdrant.Distance_UnknownDistance) && strings.EqualFold(n, name) {
			return qdrant.Distance(v), nil
		}
	}
	return 0, fmt.Errorf("want COSINE, EUCLID, DOT or MANHATTAN")
}
