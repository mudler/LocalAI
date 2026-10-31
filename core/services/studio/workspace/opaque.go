package workspace

// The overlay attributes that mark a folder opaque.
const (
	xattrOpaqueTrusted = "trusted.overlay.opaque"
	xattrOpaqueUser    = "user.overlay.opaque"
)

// IsOpaque reports whether the extended attributes of a folder mark it opaque
// in an overlay: what is below it in the lower layer is hidden.
func IsOpaque(xattrs map[string][]byte) bool {
	return string(xattrs[xattrOpaqueTrusted]) == "y" || string(xattrs[xattrOpaqueUser]) == "y"
}
