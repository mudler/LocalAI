package main

// Connection + index configuration for the Valkey-backed vector store.
//
// Configuration is read from the environment in Load() rather than from
// arbitrary business logic so operators have a single, discoverable surface
// (documented in docs/content/features/stores.md and .env). Every default
// lives as a named constant below — no magic literals sprinkled through the
// store logic — so the defaults can be audited in one place and referenced by
// the unit tests.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mudler/xlog"
)

const (
	// _defaultAddr is the single-node Valkey address used when VALKEY_ADDR is
	// unset. Matches the port Phase 0 reserved for integration tests.
	_defaultAddr = "localhost:6379"

	// _defaultClientName is mandatory: every connection identifies itself with
	// this name so operators can spot LocalAI's traffic via CLIENT LIST. It is
	// always set on the client, even if the operator clears VALKEY_CLIENT_NAME.
	_defaultClientName = "localai-valkey-store"

	// _defaultIndexAlgo is FLAT (exact brute-force KNN) to preserve parity with
	// local-store's linear scan and keep the exact-cosine test expectations.
	_defaultIndexAlgo = indexAlgoFlat

	// _defaultDistanceMetric is COSINE so similarities match local-store
	// (sim = 1 - cosine_distance). L2/IP are opt-in.
	_defaultDistanceMetric = distanceCosine

	// HNSW graph defaults (only used when VALKEY_INDEX_ALGO=HNSW). Values follow
	// the Valkey Search documented defaults.
	_defaultHNSWM              = 16
	_defaultHNSWEFConstruction = 200
	_defaultHNSWEFRuntime      = 10

	// _defaultRequestTimeoutMS bounds every command. We deliberately do NOT rely
	// on the client's built-in write timeout: index back-fill or a slow KNN can
	// exceed a short default, so we thread this explicit deadline into every
	// command context.
	_defaultRequestTimeoutMS = 5000

	// Valkey Search index algorithms.
	indexAlgoFlat = "FLAT"
	indexAlgoHNSW = "HNSW"

	// Supported distance metrics.
	distanceCosine = "COSINE"
	distanceL2     = "L2"
	distanceIP     = "IP"
)

// hnswParams holds the HNSW-only tuning knobs. They are ignored unless
// IndexAlgo == indexAlgoHNSW.
type hnswParams struct {
	M              int
	EFConstruction int
	EFRuntime      int
}

// Config is the fully-resolved store configuration produced by loadConfig().
type Config struct {
	Addr           string
	Username       string
	Password       string
	UseTLS         bool
	ClientName     string
	IndexAlgo      string
	DistanceMetric string
	HNSW           hnswParams
	RequestTimeout time.Duration
}

// loadConfig reads the VALKEY_* environment and returns a validated Config.
// It fails fast on an unknown index algorithm or distance metric so a
// misconfiguration surfaces at Load() rather than silently degrading search.
func loadConfig() (Config, error) {
	cfg := Config{
		Addr:           envOr("VALKEY_ADDR", _defaultAddr),
		Username:       os.Getenv("VALKEY_USERNAME"),
		Password:       os.Getenv("VALKEY_PASSWORD"),
		UseTLS:         envBool("VALKEY_TLS", false),
		ClientName:     envOr("VALKEY_CLIENT_NAME", _defaultClientName),
		IndexAlgo:      strings.ToUpper(envOr("VALKEY_INDEX_ALGO", _defaultIndexAlgo)),
		DistanceMetric: strings.ToUpper(envOr("VALKEY_DISTANCE_METRIC", _defaultDistanceMetric)),
		HNSW: hnswParams{
			M:              envInt("VALKEY_HNSW_M", _defaultHNSWM),
			EFConstruction: envInt("VALKEY_HNSW_EF_CONSTRUCTION", _defaultHNSWEFConstruction),
			EFRuntime:      envInt("VALKEY_HNSW_EF_RUNTIME", _defaultHNSWEFRuntime),
		},
		RequestTimeout: time.Duration(envInt("VALKEY_REQUEST_TIMEOUT_MS", _defaultRequestTimeoutMS)) * time.Millisecond,
	}

	// ClientName is mandatory. Restore the default if the operator blanked it,
	// so the connection is always identifiable.
	if cfg.ClientName == "" {
		cfg.ClientName = _defaultClientName
	}

	switch cfg.IndexAlgo {
	case indexAlgoFlat, indexAlgoHNSW:
	default:
		return Config{}, fmt.Errorf("valkey-store: invalid VALKEY_INDEX_ALGO %q (want FLAT or HNSW)", cfg.IndexAlgo)
	}

	switch cfg.DistanceMetric {
	case distanceCosine, distanceL2, distanceIP:
	default:
		return Config{}, fmt.Errorf("valkey-store: invalid VALKEY_DISTANCE_METRIC %q (want COSINE, L2 or IP)", cfg.DistanceMetric)
	}

	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = time.Duration(_defaultRequestTimeoutMS) * time.Millisecond
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		// Surface operator typos instead of silently ignoring them; a coarse
		// on/off switch still falls back to its default rather than failing Load.
		xlog.Warn("valkey-store: ignoring unparseable env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return b
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		xlog.Warn("valkey-store: ignoring unparseable env var, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return n
}
