package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// PeerIdentityHeader carries the peer credential of a replica when it dials
// another replica at GET /api/cluster/peer.
//
// The credential of a replica is the only secret that opens a peer link. The
// registration token of the deployment is for workers that join the cluster, and
// it opens no peer link: a replica that is empty of that token has the same
// peers as one that has it, and a worker that holds it cannot pose as a replica.
const PeerIdentityHeader = "X-LocalAI-Peer-Token"

// PeerCredential is the proof of one frontend replica of which replica it is.
//
// It follows the per-node credential of a worker: a random secret, stored only as
// its SHA-256, compared in constant time, with no fallback to a shared token. It
// differs in one way, in the stronger direction. A replica writes its own row in
// the instances table, so it mints its secret itself, publishes only the hash,
// and the secret never leaves the process that made it.
//
// It is one value with both halves because they must not be minted twice. The
// membership loop stores Hash and the peer pool presents Token. Two mints would
// leave a replica whose stored hash and presented secret disagree, and every dial
// it makes would be refused.
//
// The zero value is a credential that nobody can use. An empty Token is refused
// by every handler and an empty Hash authorises nobody.
type PeerCredential struct {
	token string
	hash  string
}

// NewPeerCredential mints a credential for this replica. crypto/rand.Text gives
// at least 128 bits of randomness.
func NewPeerCredential() PeerCredential {
	token := rand.Text()
	return PeerCredential{token: token, hash: HashPeerToken(token)}
}

// Token is the secret that this replica presents when it dials a peer. It is sent
// and never stored.
func (c PeerCredential) Token() string { return c.token }

// Hash is what this replica publishes in its instances row, and the only form of
// the credential that another replica sees.
func (c PeerCredential) Hash() string { return c.hash }

// HashPeerToken renders a presented token in the form that the instances table
// stores. The handler that checks a dial and the replica that registers its row
// call it, so they cannot disagree about the encoding.
func HashPeerToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// PeerTokenMatches reports whether a token is the one behind storedHash, in
// constant time. An empty hash matches nothing, and so does an empty token: a
// replica with no published credential authorises nobody, and "no credential"
// is never read as "any credential".
func PeerTokenMatches(token, storedHash string) bool {
	if storedHash == "" || token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashPeerToken(token)), []byte(storedHash)) == 1
}
