package prefixcache

import (
	"encoding/binary"

	"github.com/cespare/xxhash/v2"
)

// ExtractChain renders prompt into a cumulative chain of prefix hashes:
// h[0]=H(salt,block0), h[i]=H(h[i-1],block_i). Blocks are fixed
// cfg.WindowBytes-byte windows over the prompt bytes, chunked from absolute
// offset 0 with fixed boundaries [0,W), [W,2W), ... and the chain is capped to
// the FIRST cfg.MaxDepth blocks (the head).
//
// Head-first chunking is what makes this a true prefix-chain. The reusable
// KV/prefix cache is always at the HEAD of the prompt: the system prompt and
// early turns are stable, new content is appended at the end, and the KV cache
// is valid up to the first differing token scanning from the start. Because the
// boundaries are anchored at offset 0 (never length-dependent), a prompt P and
// any extension P+suffix share their entire leading overlap, so turn N and turn
// N+1 match for longest-prefix routing. Prefixes deeper than
// MaxDepth*WindowBytes bytes are treated as equal (two prompts agreeing on the
// first MaxDepth head blocks yield identical chains): an accepted routing-hint
// limitation, since the cap bounds the chain length for very long prompts.
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
	salt := xxhash.Sum64String(model)
	var chain []uint64
	prev := salt
	for i := range min(nBlocks, cfg.MaxDepth) {
		off := i * cfg.WindowBytes
		end := min(off+cfg.WindowBytes, len(data))
		d := xxhash.New()
		var pb [8]byte
		binary.LittleEndian.PutUint64(pb[:], prev)
		_, _ = d.Write(pb[:])
		_, _ = d.Write(data[off:end])
		prev = d.Sum64()
		chain = append(chain, prev)
	}
	return chain
}
