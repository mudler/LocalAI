package config

import "time"

// The remote LoadModel deadline used to be the fixed DefaultModelLoadTimeout.
// That is a model-size cliff, not a timeout: the deadline starts only after the
// backend install and file staging have finished, so it covers the worker's
// checkpoint read and pipeline init alone — work whose duration is proportional
// to the bytes on disk. A 70 GB video checkpoint on a Jetson Thor worker
// therefore failed reproducibly (953.5s wall clock, ~11m of it install and
// staging, then DeadlineExceeded on a load that never had a chance).
//
// Raising the constant does not fix that: it moves the cliff to the next larger
// model, and it makes a genuinely wedged SMALL model hang for the whole inflated
// duration before anyone notices. So the budget is derived from the size instead.
const (
	// ModelLoadTimeoutPerGiB is the budget granted per GiB of checkpoint on top
	// of DefaultModelLoadTimeout.
	//
	// It is deliberately generous. This is a timeout: erring long costs only
	// failure LATENCY on a load that was going to fail anyway, while erring
	// short costs a guaranteed FALSE failure on a load that was healthy. 20s/GiB
	// corresponds to reading weights at ~54 MB/s, which is below what any
	// supported medium sustains (NVMe is orders of magnitude faster; the slow end
	// is an eMMC or SD-backed Jetson, or a checkpoint faulted in over a network
	// filesystem) and so leaves headroom for the dequantisation and pipeline init
	// that follow the read. Hardware and quantisation both move the real figure
	// by an order of magnitude, which is exactly why the constant sits at the
	// pessimistic end rather than at a measured average.
	//
	// Worked examples: 2 GiB -> 5m40s (a wedged small model still fails fast),
	// 70 GiB -> 28m20s (the production checkpoint that failed at 5m),
	// 600 GiB -> 3h25m (the size the cluster has to support).
	ModelLoadTimeoutPerGiB = 20 * time.Second

	// MaxModelLoadTimeout caps the derived budget so a nonsense size (a corrupted
	// stat, a future 10 TB artifact) cannot hand out an effectively infinite
	// deadline. At ModelLoadTimeoutPerGiB the cap binds only above ~1 TiB,
	// comfortably past the 600 GB requirement.
	MaxModelLoadTimeout = 6 * time.Hour
)

// bytesPerGiB is the divisor for the per-GiB rate above.
const bytesPerGiB int64 = 1 << 30

// ModelLoadTimeoutForSize derives the gRPC deadline for the remote LoadModel
// call from the checkpoint's on-disk size.
//
// A non-positive size means the frontend could not measure the payload — a
// backend given a bare HuggingFace repo id fetches its own weights on the
// worker, so there is nothing local to stat. Rather than guess, that case keeps
// the historical DefaultModelLoadTimeout, which is also the floor of the derived
// range: size only ever adds budget.
//
// An explicit LOCALAI_NATS_MODEL_LOAD_TIMEOUT always wins over this; see
// SmartRouterOptions.ModelLoadTimeout.
func ModelLoadTimeoutForSize(bytes int64) time.Duration {
	if bytes <= 0 {
		return DefaultModelLoadTimeout
	}

	// Split into whole GiB plus remainder before scaling: multiplying a 600 GB
	// byte count by a 20e9-nanosecond rate overflows int64 long before the cap
	// could clamp it.
	gib := bytes / bytesPerGiB
	remainder := bytes % bytesPerGiB

	extra := time.Duration(gib) * ModelLoadTimeoutPerGiB
	extra += time.Duration(int64(ModelLoadTimeoutPerGiB) * remainder / bytesPerGiB)

	// A large enough gib still overflows the multiply above into a negative
	// duration; treat any non-positive extra as "past the cap".
	if extra <= 0 {
		return MaxModelLoadTimeout
	}

	return min(DefaultModelLoadTimeout+extra, MaxModelLoadTimeout)
}
