// Package ociimage resolves, verifies and caches OCI images for the sandbox
// backends. It holds the code that does not depend on how a backend lays the
// image out: reference parsing, the check of a local OCI layout against its
// digests, the cache folder with its lock and its env.json marker, the
// flattened layer tar, and the check of entry names. A backend supplies the
// part that turns the tar into its own format (an ext4 image for chv, a
// folder for gvisor).
//
// The package imports the standard library and go-containerregistry only.
package ociimage
