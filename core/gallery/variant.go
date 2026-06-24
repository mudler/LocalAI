package gallery

// Variant is one option in a gallery entry's variant list. It references an
// existing gallery entry by name, and that is all an author has to write.
//
// Authored order carries no meaning. Selection filters out what this host
// cannot run and then ranks what is left, so hardware knowledge lives in the
// selector rather than being pushed onto whoever edits the gallery.
//
// There is deliberately no authored memory figure here. When the live probe
// misreads a variant's footprint, the fix is the referenced entry's own `size:`
// field: the estimator already prefers a declared size over its own guesswork,
// so correcting it there fixes the figure for every consumer rather than only
// for this one selection pass.
type Variant struct {
	// Model is the name of a gallery entry that declares no variants of its own.
	Model string `json:"model" yaml:"model"`
}
