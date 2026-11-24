// Package p2ppair holds the pairing domain of the peer-to-peer layer: the
// invites an administrator mints, the devices that redeem them, the invite
// link format, the confirmation phrase and the stores that keep the rows.
//
// An invite is a one-time code with a short life. The administrator shares it
// as a link (see Link). The other side connects to the server by peer ID and
// redeems the code (Service.Redeem), which creates a Device row. A device is
// admitted to the peer-to-peer network until it is revoked. Both sides then
// show the same four-word confirmation phrase (Phrase) so that the people can
// check that no one sat in between.
//
// The package does not open network connections and does not serve HTTP. The
// caller supplies the parts that touch the outside world through small
// interfaces: PassRegistrar (relay passes), ClientKeyIssuer (API keys for
// invites of the remote kind), RelaySource (the relays the server knows) and
// Limiter (rate limits for wrong codes). It does not import the auth package,
// so the auth database can migrate its tables (see Models).
//
// Minting invites and accepting devices belong to the "share" P2P mode, which
// needs authentication. This package does not check the mode: the caller
// creates a Service only in that mode.
//
// # Stores
//
// Store has two implementations that pass the same tests. NewGormStore keeps
// the rows in the shared database (tables p2p_invites and p2p_devices).
// NewJSONStore keeps them in one file under the data path for deployments with
// no database. Codes are stored only as keyed hashes.
//
// # Confirmation phrase word list
//
// The word list in words.go was written for this project (1024 short English
// words, ten bits each) and is released under the same licence as the rest of
// the repository. It is not derived from any third-party list. The phrase
// carries 40 bits.
package p2ppair
