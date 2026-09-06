// SPDX-License-Identifier: MIT

package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// PeerIdentityHeader carries a replica's own peer credential on a dial to
// GET /api/cluster/peer, alongside the deployment's shared cluster token in
// Authorization.
//
// A header of its own rather than a second value crammed into Authorization,
// and the two are checked separately: the shared token says the dialler belongs
// to this deployment, this one says WHICH replica it is. Folding them together
// would make one of the two answers unavailable, and the shared token check is
// the one that must not be weakened.
//
// The constant lives beside PeerPath, in the package both ends import, for the
// same reason PeerPath does: the dialler here and the handler in
// core/http/endpoints/cluster cannot end up naming different headers.
const PeerIdentityHeader = "X-LocalAI-Peer-Token"

// PeerCredential is one frontend replica's proof of which replica it is.
//
// The shape follows the per-node worker credential (nodes.BackendNode
// TunnelTokenHash) rather than inventing a second mechanism: a random secret,
// stored only as its SHA-256, compared in constant time, with no fallback to
// the shared token. It differs in one way, and in the stronger direction. A
// worker's credential is minted BY the frontend and returned to the worker
// once, so it crosses the network at birth; a replica writes its own row in the
// instances table, so it mints its own secret, publishes only the hash, and the
// plaintext never leaves the process that made it.
//
// It is one value carrying both halves because they must not be minted twice.
// Membership stores Hash and PeerPool presents Token, and two independent mints
// would leave a replica whose stored hash and presented secret disagree: every
// dial it makes would be refused, on a route whose refusal looks exactly like a
// peer that is merely older. Passing this struct to both makes that a
// non-outcome rather than a bug to be found.
//
// The zero value is a credential nobody may use: an empty Token is refused by
// every handler, and an empty Hash authorizes nobody. That direction is
// deliberate. An empty credential is NOT a credential that matches everything.
type PeerCredential struct {
	token string
	hash  string
}

// NewPeerCredential mints a fresh credential for this replica.
//
// crypto/rand.Text gives at least 128 bits of randomness with no error to
// handle and no length constant to get wrong, which is what the worker
// credential is minted with too.
func NewPeerCredential() PeerCredential {
	token := rand.Text()
	return PeerCredential{token: token, hash: HashPeerToken(token)}
}

// Token is the secret this replica presents when it dials a peer. It is only
// ever sent, never stored.
func (c PeerCredential) Token() string { return c.token }

// Hash is what this replica publishes in its instances row, and the only form
// of the credential any other replica ever sees.
func (c PeerCredential) Hash() string { return c.hash }

// HashPeerToken renders a presented peer token in the form the instances table
// stores. Exported so the handler that verifies a dial and the replica that
// registers its row cannot disagree about the encoding.
func HashPeerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// PeerTokenMatches reports whether a presented token is the one behind
// storedHash, in constant time.
//
// An empty storedHash matches NOTHING, including an empty token. A replica with
// no published credential is one registered by a release that predates them, and
// it authorizes nobody: reading "no credential" as "any credential" is exactly
// the failure this route is being fixed for.
//
// The empty guard is not what makes that true (ConstantTimeCompare already
// returns 0 on a length mismatch, and a stored hash is 64 hex bytes or nothing);
// it is here so a reader does not have to derive the rule from a length, and so
// the caller can log that case as the distinct operator problem it is.
func PeerTokenMatches(token, storedHash string) bool {
	if storedHash == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashPeerToken(token)), []byte(storedHash)) == 1
}
