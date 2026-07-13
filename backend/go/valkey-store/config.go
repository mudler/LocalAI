package main

// Connection + index configuration for the Valkey-backed vector store.
//
// Configuration is read from the model config `options:` list (a repeated
// `key:value` string carried over gRPC in ModelOptions.Options) rather than
// from process-wide environment variables. Driving it from the model config is
// the LocalAI convention and, crucially, lets multiple stores each have their
// own Valkey config (a face registry on one server, a router cache on another)
// within a single LocalAI process — something a single VALKEY_* env surface
// could never express. Every default lives as a named constant below — no
// magic literals sprinkled through the store logic — so the defaults can be
// audited in one place and referenced by the unit tests.
//
// Example model YAML:
//
//	name: my-vector-store
//	backend: valkey-store
//	options:
//	  - addr:valkey.internal:6379
//	  - index_algo:HNSW
//	  - distance_metric:COSINE

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"github.com/mudler/xlog"
)

const (
	// _defaultAddr is the single-node Valkey address used when the `addr`
	// option is unset. Matches the port Phase 0 reserved for integration tests.
	_defaultAddr = "localhost:6379"

	// _defaultClientName is mandatory: every connection identifies itself with
	// this name so operators can spot LocalAI's traffic via CLIENT LIST. It is
	// always set on the client, even if the operator clears the client_name option.
	_defaultClientName = "localai-valkey-store"

	// _defaultIndexAlgo is FLAT (exact brute-force KNN) to preserve parity with
	// local-store's linear scan and keep the exact-cosine test expectations.
	_defaultIndexAlgo = indexAlgoFlat

	// _defaultDistanceMetric is COSINE so similarities match local-store
	// (sim = 1 - cosine_distance). L2/IP are opt-in.
	_defaultDistanceMetric = distanceCosine

	// HNSW graph defaults (only used when index_algo=HNSW). Values follow
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

	// Option keys recognised in the model config `options:` list. They mirror
	// the previous VALKEY_* env var names without the prefix and lower-cased, so
	// operators migrating a config have an obvious 1:1 mapping.
	optAddr               = "addr"
	optUsername           = "username"
	optPassword           = "password"
	optTLS                = "tls"
	optTLSSkipVerify      = "tls_skip_verify"
	optTLSCACert          = "tls_ca_cert"
	optClientName         = "client_name"
	optDB                 = "db"
	optIndexAlgo          = "index_algo"
	optDistanceMetric     = "distance_metric"
	optHNSWM              = "hnsw_m"
	optHNSWEFConstruction = "hnsw_ef_construction"
	optHNSWEFRuntime      = "hnsw_ef_runtime"
	optRequestTimeoutMS   = "request_timeout_ms"
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
	TLSSkipVerify  bool
	TLSCACert      string
	ClientName     string
	DB             int
	IndexAlgo      string
	DistanceMetric string
	HNSW           hnswParams
	RequestTimeout time.Duration
}

// parseOptions turns the repeated `key:value` ModelOptions.Options list into a
// lookup map. The split is on the FIRST ':' via strings.Cut, so values that
// themselves contain a colon (e.g. `addr:host:6379`) are preserved intact. A
// malformed entry with no ':' is warned about and skipped rather than silently
// dropped, so an operator typo is visible in the logs.
func parseOptions(opts *pb.ModelOptions) map[string]string {
	m := make(map[string]string)
	if opts == nil {
		return m
	}
	for _, o := range opts.GetOptions() {
		k, v, ok := strings.Cut(o, ":")
		if !ok {
			xlog.Warn("valkey-store: ignoring malformed option (want key:value)", "option", o)
			continue
		}
		m[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}
	return m
}

// loadConfig resolves the store configuration from the model config options and
// returns a validated Config. It fails fast on an unknown index algorithm or
// distance metric (and on a malformed integer) so a misconfiguration surfaces
// at Load() rather than silently degrading search.
func loadConfig(opts *pb.ModelOptions) (Config, error) {
	o := parseOptions(opts)

	// intOr parses an integer option, failing fast on a malformed value the same
	// way an invalid index algo or distance metric does. A typo like
	// `hnsw_m:1x6` must surface at Load() rather than silently degrading to the
	// default and producing subtly wrong (and hard-to-diagnose) index
	// behaviour. The first parse error wins and is returned below.
	var parseErr error
	intOr := func(key string, fallback int) int {
		v, ok := o[key]
		if !ok || v == "" {
			return fallback
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			if parseErr == nil {
				parseErr = fmt.Errorf("valkey-store: invalid option %s %q: %w", key, v, err)
			}
			return fallback
		}
		return n
	}

	cfg := Config{
		Addr:           strOr(o, optAddr, _defaultAddr),
		Username:       o[optUsername],
		Password:       o[optPassword],
		UseTLS:         boolOr(o, optTLS, false),
		TLSSkipVerify:  boolOr(o, optTLSSkipVerify, false),
		TLSCACert:      o[optTLSCACert],
		ClientName:     strOr(o, optClientName, _defaultClientName),
		DB:             intOr(optDB, 0),
		IndexAlgo:      strings.ToUpper(strOr(o, optIndexAlgo, _defaultIndexAlgo)),
		DistanceMetric: strings.ToUpper(strOr(o, optDistanceMetric, _defaultDistanceMetric)),
		HNSW: hnswParams{
			M:              intOr(optHNSWM, _defaultHNSWM),
			EFConstruction: intOr(optHNSWEFConstruction, _defaultHNSWEFConstruction),
			EFRuntime:      intOr(optHNSWEFRuntime, _defaultHNSWEFRuntime),
		},
		RequestTimeout: time.Duration(intOr(optRequestTimeoutMS, _defaultRequestTimeoutMS)) * time.Millisecond,
	}
	if parseErr != nil {
		return Config{}, parseErr
	}

	// ClientName is mandatory. Restore the default if the operator blanked it,
	// so the connection is always identifiable.
	if cfg.ClientName == "" {
		cfg.ClientName = _defaultClientName
	}

	if cfg.DB < 0 {
		return Config{}, fmt.Errorf("valkey-store: invalid option %s %d (must be >= 0)", optDB, cfg.DB)
	}

	switch cfg.IndexAlgo {
	case indexAlgoFlat, indexAlgoHNSW:
	default:
		return Config{}, fmt.Errorf("valkey-store: invalid option %s %q (want FLAT or HNSW)", optIndexAlgo, cfg.IndexAlgo)
	}

	switch cfg.DistanceMetric {
	case distanceCosine, distanceL2, distanceIP:
	default:
		return Config{}, fmt.Errorf("valkey-store: invalid option %s %q (want COSINE, L2 or IP)", optDistanceMetric, cfg.DistanceMetric)
	}

	if cfg.RequestTimeout <= 0 {
		cfg.RequestTimeout = time.Duration(_defaultRequestTimeoutMS) * time.Millisecond
	}

	return cfg, nil
}

// strOr returns the option value for key, or fallback when it is unset/empty.
func strOr(o map[string]string, key, fallback string) string {
	if v, ok := o[key]; ok && v != "" {
		return v
	}
	return fallback
}

// boolOr parses a boolean option, falling back to the default on an unset or
// unparseable value. A typo is surfaced via a warning (like the previous env
// behaviour) rather than failing Load for a coarse on/off switch.
func boolOr(o map[string]string, key string, fallback bool) bool {
	v, ok := o[key]
	if !ok || v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		xlog.Warn("valkey-store: ignoring unparseable option, using default", "key", key, "value", v, "default", fallback)
		return fallback
	}
	return b
}
