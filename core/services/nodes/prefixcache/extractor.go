package prefixcache

import "github.com/cespare/xxhash/v2"

// ExtractChain renders prompt into a cumulative chain of prefix hashes:
// h[0]=H(salt,block0), h[i]=H(h[i-1],block_i). Blocks are fixed
// cfg.WindowBytes-byte windows over the prompt bytes. Only the last
// cfg.MaxDepth blocks are returned (shallow to deep) so very long prompts do
// not produce thousands of hashes; this is enough to localize the divergence
// point. The model id salts every hash so different models never collide.
//
// xxhash is used (not hash/maphash) because the hash MUST be identical across
// frontend processes: peers exchange these hashes over NATS, and maphash uses a
// per-process random seed that would make peers disagree.
func ExtractChain(model, prompt string, cfg Config) []uint64 {
	if prompt == "" {
		return nil
	}
	data := []byte(prompt)
	nBlocks := (len(data) + cfg.WindowBytes - 1) / cfg.WindowBytes
	start := 0
	if nBlocks > cfg.MaxDepth {
		// Keep only the deepest MaxDepth blocks. We still chain from the start
		// of the kept window; matching only needs the divergence point.
		start = (nBlocks - cfg.MaxDepth) * cfg.WindowBytes
	}
	salt := xxhash.Sum64String(model)
	var chain []uint64
	prev := salt
	for off := start; off < len(data); off += cfg.WindowBytes {
		end := min(off+cfg.WindowBytes, len(data))
		d := xxhash.New()
		var pb [8]byte
		for i := 0; i < 8; i++ {
			pb[i] = byte(prev >> (8 * i))
		}
		_, _ = d.Write(pb[:])
		_, _ = d.Write(data[off:end])
		prev = d.Sum64()
		chain = append(chain, prev)
	}
	return chain
}
