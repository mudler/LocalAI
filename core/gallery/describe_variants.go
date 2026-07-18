package gallery

// VariantView is one selectable build of an entry, as a client sees it.
//
// It is the read-only mirror of what selection decides at install time, so a
// picker can show the user the same facts the installer acts on rather than a
// second, independently-derived opinion that could disagree with it.
type VariantView struct {
	// Model is the name to send back as the install request's `variant`.
	Model string `json:"model"`
	// Backend is the engine the referenced entry resolves to. A client renders
	// it, and it is also the reason Fits may be false on a host whose memory
	// would be ample.
	Backend string `json:"backend"`
	// MemoryBytes is the measured footprint, or 0 when it could not be
	// determined. Zero is unknown, never "needs nothing", so a client must
	// render it as unknown rather than as a free option.
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	// Fits reports whether auto-selection would consider this variant on this
	// host: its backend can run here and its known footprint is within budget.
	// An unknown footprint counts as fitting, exactly as selection treats it.
	Fits bool `json:"fits"`
	// IsBase marks the entry's own payload. It is always installable and is
	// what auto-selection falls back to, which is why it is listed alongside
	// the declared variants rather than hidden.
	IsBase bool `json:"is_base"`
}

// EntryVariants is the variant surface of a single gallery entry.
type EntryVariants struct {
	Variants []VariantView `json:"variants"`
	// AutoSelected is the variant auto-selection would install on this host
	// right now. Clients show it as the default choice.
	AutoSelected string `json:"auto_selected"`
}

// DescribeVariants reports an entry's selectable builds and which one
// auto-selection would currently pick.
//
// It runs the SAME variantOptions + SelectVariant pass the installer runs, so
// the reported auto-selection cannot drift from what installing would actually
// do. The env carries the same probe seam too, so the size shown here and the
// size the installer compares against come from one cache and one round trip.
//
// An entry that declares no variants returns nil, nil WITHOUT touching the
// probe. That is load-bearing: the gallery listing walks entries by the
// thousand and the overwhelming majority declare nothing, so the no-variant
// case must cost nothing at all.
func DescribeVariants(models []*GalleryModel, entry *GalleryModel, env ResolveEnv) (*EntryVariants, error) {
	if entry == nil || !entry.HasVariants() {
		return nil, nil
	}

	options, err := variantOptions(models, entry, env)
	if err != nil {
		return nil, err
	}

	selection, err := SelectVariant(options, env, "")
	if err != nil {
		return nil, err
	}

	views := make([]VariantView, 0, len(options))
	for _, o := range options {
		memory, known, err := o.EffectiveMemory()
		if err != nil {
			return nil, err
		}
		view := VariantView{
			Model:   o.Variant.Model,
			Backend: o.Backend,
			// The base is exempt from every filter and always installs, so
			// reporting it as anything but fitting would misdescribe it.
			Fits:   o.IsBase || (env.backendRuns(o.Backend) && (!known || memory <= env.AvailableMemory)),
			IsBase: o.IsBase,
		}
		if known {
			view.MemoryBytes = memory
		}
		views = append(views, view)
	}

	return &EntryVariants{Variants: views, AutoSelected: selection.Option.Variant.Model}, nil
}
