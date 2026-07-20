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
	// MemoryBytes is the measured footprint. It is omitted from the JSON
	// entirely when the size could not be determined, rather than serialized as
	// a zero that a client would have to know to read as unknown. An absent key
	// never means "needs nothing".
	MemoryBytes uint64 `json:"memory_bytes,omitempty"`
	// Fits reports whether auto-selection would consider this variant on this
	// host: its backend can run here and its known footprint is within budget.
	// An unknown footprint counts as fitting, exactly as selection treats it.
	Fits bool `json:"fits"`
	// IsBase marks the entry's own payload. It is always installable and is
	// what auto-selection falls back to, which is why it is listed alongside
	// the declared variants rather than hidden.
	IsBase bool `json:"is_base"`
	// Quantization is the weight format the referenced entry installs, e.g.
	// "Q2_G64", "PQ2_0", "F16". It is the fact that actually separates two
	// builds of one model: name, backend and probed size routinely agree
	// between variants that differ entirely in precision.
	//
	// Derived server-side from the referenced entry's model filename rather
	// than parsed by clients, so every client reads the same format out of the
	// same file the installer will point the backend at, and a naming
	// convention change is one edit here rather than one per client.
	//
	// Omitted when the entry names no recognisable format. That is a normal
	// outcome for a backend served from a directory of weights, and clients
	// must render its absence rather than an empty or invented value.
	Quantization string `json:"quantization,omitempty"`
	// Features are the serving features this build declares, best first, e.g.
	// ["dflash"]. They mean the same weights are served faster, which is a
	// reason to prefer a variant that neither its size nor its precision
	// conveys.
	//
	// This is the SAME tag-against-vocabulary match servingFeatureRank ranks
	// on, over the same host preference list, so what a client shows as a speed
	// advantage cannot disagree with what selection actually rewarded. A tag
	// outside that vocabulary is not a serving feature and is not reported
	// here: the entry's own tag list already carries it.
	Features []string `json:"features,omitempty"`
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
		memory, known := o.EffectiveMemory()
		view := VariantView{
			Model:   o.Variant.Model,
			Backend: o.Backend,
			// The base is exempt from every filter and always installs, so
			// reporting it as anything but fitting would misdescribe it.
			Fits:   o.IsBase || (env.backendRuns(o.Backend) && (!known || memory <= env.AvailableMemory)),
			IsBase: o.IsBase,
			// o.Tags is already the REFERENCED entry's tag list, which is the
			// only correct source: the feature belongs to the build that would
			// be installed, not to the family the parent describes.
			Features: env.servingFeaturesOf(o),
		}
		if known {
			view.MemoryBytes = memory
		}
		// The base option's payload is the entry itself; every other option
		// names a gallery entry variantOptions has already proved resolvable,
		// so this second lookup is an in-memory map hit and cannot fail.
		source := entry
		if !o.IsBase {
			source = FindGalleryElement(models, o.Variant.Model)
		}
		view.Quantization = quantizationOfEntry(source)
		views = append(views, view)
	}

	return &EntryVariants{Variants: views, AutoSelected: selection.Option.Variant.Model}, nil
}
