package enginesettings_test

import (
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	es "github.com/mudler/LocalAI/pkg/enginesettings"
)

// toyEngine is a second engine with a table of its own. It proves the
// framework needs no code of llama.cpp: the parser, the builder, the schema
// and the registry work from this description alone.
func toyEngine() *es.Engine {
	return &es.Engine{
		ID:       "toy-engine",
		Name:     "Toy Engine",
		Backends: es.BackendMatch{Families: []string{"toy"}},
		Groups:   []es.Group{"limits", "runtime"},
		Flags: []es.Flag{
			{
				Key: "max_model_len", Target: es.TargetConfig, Path: "context_size",
				CLI: []string{"--max-model-len"}, Type: es.TypeInt, Min: es.F64(1),
				Group: "limits", Doc: "Longest sequence in tokens.",
			},
			{
				Key: "memory_fraction", Target: es.TargetOption, Path: "options", Option: "gpu_memory_utilization",
				CLI: []string{"--gpu-memory-utilization"}, Type: es.TypeFloat, Min: es.F64(0), Max: es.F64(1),
				Group: "limits", Doc: "Share of GPU memory the engine may use.",
			},
			{
				Key: "eager", Target: es.TargetOption, Path: "options", Option: "enforce_eager",
				CLI: []string{"--enforce-eager"}, Type: es.TypeBool, BareOption: true,
				Group: "runtime", Doc: "Run without graph capture.",
			},
			{
				Key: "dtype", Target: es.TargetOption, Path: "options", Option: "dtype",
				CLI: []string{"--dtype"}, Type: es.TypeEnum, Enum: []string{"auto", "half", "bfloat16"},
				Group: "runtime", Doc: "Weight data type.",
			},
		},
		Presets: []es.Preset{{ID: "small", Name: "Small", Settings: map[string]any{"max_model_len": 2048, "eager": true}}},
		Command: &es.CommandLine{
			Binary:  "toy-serve",
			Ignored: []es.IgnoredFlag{{CLI: []string{"--port"}, Reason: "LocalAI picks the port"}},
		},
	}
}

// toyMemory is a memory description with an advice over the toy dtype flag,
// and memoryProfile a profile with a value per kind.
func toyMemory() *es.Memory {
	return &es.Memory{
		Fallback: es.MemoryDiscrete, FitFraction: 0.9,
		Kinds: []es.MemoryKindInfo{
			{Kind: es.MemoryUnified, Label: "Unified", Doc: "Shared."},
			{Kind: es.MemoryDiscrete, Label: "Discrete", Doc: "Own memory."},
			{Kind: es.MemorySystem, Label: "No GPU", Doc: "CPU."},
		},
		Advice: []es.Advice{{ID: "a", Control: "dtype", Kind: es.MemoryUnified, Value: "auto", Avoid: []string{"half"}, Doc: "Use auto."}},
	}
}

func memoryProfile() es.Preset {
	return es.Preset{
		ID: "many", Profile: true, Set: es.ProfileSetConcurrency, Streams: 8,
		Settings: map[string]any{"max_model_len": 4096},
		Memory: map[es.MemoryKind]es.PresetMemory{
			es.MemoryDiscrete: {Settings: map[string]any{"dtype": "half"}, MinMemory: 1},
			es.MemoryUnified:  {Tight: map[string]any{"dtype": "half"}},
			es.MemorySystem:   {Unsupported: true, Note: "Too slow."},
		},
	}
}

// toyDrafting is a drafting capability over the toy table: dtype stands for
// the kind and max_model_len for a field.
func toyDrafting() *es.Drafting {
	return &es.Drafting{
		Group: "runtime", TypeKey: "dtype", ModelKey: "dtype",
		Streams: &es.DraftStreams{Option: "max_streams", Doc: "Pause above this many streams."},
		Modes: []es.DraftMode{
			{ID: "off", Label: "Off", Doc: "No drafting."},
			{ID: "fast", Label: "Fast", Doc: "Draft.", Types: []string{"half"}, Fields: []string{"max_model_len"}, Set: map[string]any{"max_model_len": 8}},
		},
	}
}

func fieldsOnly() *es.Engine {
	e := toyEngine()
	e.ID = "fields-only"
	e.Command = nil
	for i := range e.Flags {
		e.Flags[i].CLI = nil
	}
	return e
}

var _ = Describe("an engine described only by its table", func() {
	var toy *es.Engine
	BeforeEach(func() { toy = toyEngine() })

	It("validates", func() {
		Expect(toy.Validate()).To(Succeed())
		Expect(fieldsOnly().Validate()).To(Succeed())
	})

	It("lists the drafting capability and its modes in the schema", func() {
		Expect(toy.Has(es.CapabilityDrafting)).To(BeFalse())
		Expect(toy.Schema().Drafting).To(BeNil())
		toy.Drafting = toyDrafting()
		Expect(toy.Validate()).To(Succeed())
		Expect(toy.Has(es.CapabilityDrafting)).To(BeTrue())
		raw, err := json.Marshal(toy.Schema())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"drafting":{"group":"runtime","type_key":"dtype","model_key":"dtype"`))
		Expect(string(raw)).To(ContainSubstring(`"streams":{"option":"max_streams"`))
	})

	It("parses its command line into config keys and options", func() {
		r, err := toy.ParseCommand(`toy-serve --max-model-len 8192 --gpu-memory-utilization=0.9 --enforce-eager --dtype HALF --port 8000 --seed 3`)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Fields).To(Equal(es.Config{"context_size": 8192}))
		Expect(r.Options).To(Equal([]string{"gpu_memory_utilization:0.9", "enforce_eager", "dtype:half", "--seed:3"}))
		Expect(r.Unmapped).To(HaveLen(2))
		Expect(r.Unmapped[0].Reason).To(HavePrefix("ignored"))
		Expect(r.Placements).To(BeNil())
	})

	It("checks values against the table", func() {
		r, err := toy.ParseCommand(`toy-serve --gpu-memory-utilization 2 --dtype fp4 --max-model-len 0`)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Options).To(BeEmpty())
		Expect(r.Unmapped).To(HaveLen(3))
	})

	It("builds the command of a config and reads it back", func() {
		cfg := es.Config{"context_size": 4096, "options": []string{"enforce_eager", "dtype:bfloat16", "unknown:1"}}
		b, err := toy.BuildCommand(cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(b.Command).To(Equal("toy-serve --max-model-len 4096 --enforce-eager --dtype bfloat16"))
		Expect(b.Warnings).To(ConsistOf(ContainSubstring(`"unknown:1" has no toy-serve flag`)))
		r, err := toy.ParseCommand(b.Command)
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Fields).To(Equal(es.Config{"context_size": 4096}))
		Expect(r.Options).To(Equal([]string{"enforce_eager", "dtype:bfloat16"}))
	})

	It("reads a config decoded from JSON", func() {
		var cfg es.Config
		Expect(json.Unmarshal([]byte(`{"context_size":1024,"name":"x","options":["gpu_memory_utilization:0.5"]}`), &cfg)).To(Succeed())
		b, err := toy.BuildCommand(cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(b.Command).To(Equal("toy-serve --max-model-len 1024 --gpu-memory-utilization 0.5"))
	})

	It("has a schema with no llama.cpp part", func() {
		s := toy.Schema()
		Expect(s.Capabilities).To(Equal([]es.Capability{es.CapabilityCommandLine}))
		Expect(s.Placement).To(BeNil())
		Expect(s.Combined).To(BeEmpty())
		Expect(s.ReservedOptions).To(BeEmpty())
		b, err := json.Marshal(s)
		Expect(err).ToNot(HaveOccurred())
		Expect(string(b)).ToNot(ContainSubstring("llama"))
		Expect(string(b)).To(ContainSubstring(`"binary":"toy-serve"`))
	})

	It("refuses the operations of a capability it lacks", func() {
		f := fieldsOnly()
		Expect(f.Capabilities()).To(BeEmpty())
		Expect(f.Has(es.CapabilityCommandLine)).To(BeFalse())
		_, err := f.ParseCommand("x")
		Expect(errors.Is(err, es.ErrUnsupported)).To(BeTrue())
		_, err = f.BuildCommand(es.Config{})
		Expect(errors.Is(err, es.ErrUnsupported)).To(BeTrue())
		Expect(f.Schema().Binary).To(BeEmpty())
	})
})

var _ = Describe("Engine.Validate", func() {
	It("reports a broken table", func() {
		cases := map[string]func(*es.Engine){
			"empty id":            func(e *es.Engine) { e.ID = "" },
			"upper-case id":       func(e *es.Engine) { e.ID = "Toy" },
			"no name":             func(e *es.Engine) { e.Name = "" },
			"no backend":          func(e *es.Engine) { e.Backends = es.BackendMatch{} },
			"duplicate key":       func(e *es.Engine) { e.Flags[1].Key = e.Flags[0].Key },
			"no doc":              func(e *es.Engine) { e.Flags[0].Doc = "" },
			"unknown group":       func(e *es.Engine) { e.Flags[0].Group = "nope" },
			"option without name": func(e *es.Engine) { e.Flags[1].Option = "" },
			"bad target":          func(e *es.Engine) { e.Flags[0].Target = "x" },
			"enum without values": func(e *es.Engine) { e.Flags[3].Enum = nil },
			"duplicate spelling":  func(e *es.Engine) { e.Flags[1].CLI = e.Flags[0].CLI },
			"ignored spelling":    func(e *es.Engine) { e.Command.Ignored[0].CLI = []string{"--dtype"} },
			"no binary":           func(e *es.Engine) { e.Command.Binary = "" },
			"no spelling":         func(e *es.Engine) { e.Flags[0].CLI = nil },
			"preset unknown key":  func(e *es.Engine) { e.Presets[0].Settings["nope"] = 1 },
			"preset bad value":    func(e *es.Engine) { e.Presets[0].Settings["max_model_len"] = "x" },
			"preset placement":    func(e *es.Engine) { e.Presets[0].Placement = &es.Placement{Mode: es.ExpertsCPUAll} },
			"combined unknown key": func(e *es.Engine) {
				e.Combined = []es.Combined{{ID: "c", Group: "limits", Keys: []string{"nope"}}}
			},
			"special without parser": func(e *es.Engine) {
				e.Command.Special = []es.SpecialFlag{{CLI: []string{"--x"}}}
			},
			"placement incomplete":         func(e *es.Engine) { e.Placement = &es.TensorPlacement{Option: "-x", Group: "limits"} },
			"LocalAI only with a spelling": func(e *es.Engine) { e.Flags[2].LocalAIOnly = true },
			"LocalAI only config key": func(e *es.Engine) {
				e.Flags[0].LocalAIOnly, e.Flags[0].CLI = true, nil
			},
			"collapsed unknown group": func(e *es.Engine) { e.Collapsed = []es.Group{"nope"} },
			"estimate unknown field":  func(e *es.Engine) { e.Estimate = map[string]string{"nope": "max_model_len"} },
			"estimate unknown key":    func(e *es.Engine) { e.Estimate = map[string]string{"context_size": "nope"} },
			"drafting unknown group":  func(e *es.Engine) { e.Drafting = toyDrafting(); e.Drafting.Group = "nope" },
			"drafting unknown key":    func(e *es.Engine) { e.Drafting = toyDrafting(); e.Drafting.ModelKey = "nope" },
			"drafting first not off":  func(e *es.Engine) { e.Drafting = toyDrafting(); e.Drafting.Modes = e.Drafting.Modes[1:] },
			"drafting field unknown":  func(e *es.Engine) { e.Drafting = toyDrafting(); e.Drafting.Modes[1].Fields = []string{"nope"} },
			"drafting bad set": func(e *es.Engine) {
				e.Drafting = toyDrafting()
				e.Drafting.Modes[1].Set = map[string]any{"max_model_len": "x"}
			},
			"drafting auto no need": func(e *es.Engine) { e.Drafting = toyDrafting(); e.Drafting.Modes[1].Auto = true },
			"drafting duplicate id": func(e *es.Engine) { e.Drafting = toyDrafting(); e.Drafting.Modes[1].ID = "off" },
			"drafting streams in the table": func(e *es.Engine) {
				e.Drafting = toyDrafting()
				e.Drafting.Streams = &es.DraftStreams{Option: "dtype", Doc: "x"}
			},
			"memory without kinds":     func(e *es.Engine) { e.Memory = toyMemory(); e.Memory.Kinds = e.Memory.Kinds[:2] },
			"memory bad fallback":      func(e *es.Engine) { e.Memory = toyMemory(); e.Memory.Fallback = "nope" },
			"memory bad fit":           func(e *es.Engine) { e.Memory = toyMemory(); e.Memory.FitFraction = 1.5 },
			"advice unknown control":   func(e *es.Engine) { e.Memory = toyMemory(); e.Memory.Advice[0].Control = "nope" },
			"advice bad value":         func(e *es.Engine) { e.Memory = toyMemory(); e.Memory.Advice[0].Avoid = []string{"x"} },
			"profile memory, no rules": func(e *es.Engine) { e.Presets = []es.Preset{memoryProfile()} },
			"profile memory bad key": func(e *es.Engine) {
				e.Memory = toyMemory()
				p := memoryProfile()
				p.Memory[es.MemoryUnified] = es.PresetMemory{Tight: map[string]any{"nope": 1}}
				e.Presets = []es.Preset{p}
			},
			"profile unsupported without note": func(e *es.Engine) {
				e.Memory = toyMemory()
				p := memoryProfile()
				p.Memory[es.MemorySystem] = es.PresetMemory{Unsupported: true}
				e.Presets = []es.Preset{p}
			},
			"preset with memory rules": func(e *es.Engine) {
				e.Memory = toyMemory()
				p := memoryProfile()
				p.Profile = false
				e.Presets = []es.Preset{p}
			},
			"profile with a placement": func(e *es.Engine) {
				e.Presets = []es.Preset{{ID: "a", Profile: true, Settings: map[string]any{}, Placement: &es.Placement{Mode: es.ExpertsCPUAll}}}
			},
		}
		for name, breakIt := range cases {
			e := toyEngine()
			breakIt(e)
			Expect(e.Validate()).To(HaveOccurred(), name)
		}
	})

	It("accepts nil preset values, LocalAI only options and profiles over the same keys", func() {
		e := toyEngine()
		e.Flags = append(e.Flags, es.Flag{
			Key: "warmup", Target: es.TargetOption, Path: "options", Option: "warmup", LocalAIOnly: true,
			Type: es.TypeBool, Group: "runtime", Doc: "Read by LocalAI only.",
		})
		e.Presets = []es.Preset{
			{ID: "plain", Profile: true, Settings: map[string]any{"max_model_len": nil, "warmup": nil}},
			{ID: "fast", Profile: true, Settings: map[string]any{"max_model_len": 4096, "warmup": true}},
			{ID: "other", Settings: map[string]any{"eager": nil}},
		}
		Expect(e.Validate()).To(Succeed())

		res, err := e.BuildCommand(es.Config{"options": []any{"warmup:true"}})
		Expect(err).ToNot(HaveOccurred())
		Expect(res.Command).To(Equal("toy-serve"))
		Expect(res.Warnings).To(ConsistOf(ContainSubstring("read by LocalAI")))

		raw, err := json.Marshal(e.Schema())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"localai_only":true`))
		Expect(string(raw)).To(ContainSubstring(`"profile":true`))
		Expect(string(raw)).To(ContainSubstring(`"max_model_len":null`))
	})
})

var _ = Describe("Memory kinds", func() {
	It("accepts profiles with values per memory kind and sends them in the schema", func() {
		e := toyEngine()
		e.Memory = toyMemory()
		e.Presets = append(e.Presets, memoryProfile())
		Expect(e.Validate()).To(Succeed())
		raw, err := json.Marshal(e.Schema())
		Expect(err).ToNot(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"set":"concurrency"`))
		Expect(string(raw)).To(ContainSubstring(`"fit_fraction":0.9`))
		Expect(string(raw)).To(ContainSubstring(`"tight":{"dtype":"half"}`))
	})

	It("merges the values of a kind, and the tight ones only when asked", func() {
		p := memoryProfile()
		Expect(p.SettingsFor(es.MemoryDiscrete, false)).To(Equal(map[string]any{"max_model_len": 4096, "dtype": "half"}))
		Expect(p.SettingsFor(es.MemoryUnified, false)).To(Equal(map[string]any{"max_model_len": 4096}))
		Expect(p.SettingsFor(es.MemoryUnified, true)).To(Equal(map[string]any{"max_model_len": 4096, "dtype": "half"}))
		Expect(p.SettingsFor("nope", true)).To(Equal(map[string]any{"max_model_len": 4096}))
		Expect(p.Settings).To(HaveLen(1), "the profile itself is not changed")
	})
})

var _ = Describe("Registry", func() {
	It("registers, finds by id and by backend, and refuses duplicates", func() {
		r := es.NewRegistry()
		Expect(r.Register(toyEngine())).To(Succeed())
		Expect(r.Register(fieldsOnly())).To(Succeed())
		Expect(r.Register(toyEngine())).To(MatchError(ContainSubstring("already registered")))
		bad := toyEngine()
		bad.ID = "bad"
		bad.Name = ""
		Expect(r.Register(bad)).ToNot(Succeed())

		Expect(r.All()).To(HaveLen(2))
		e, ok := r.Get("toy-engine")
		Expect(ok).To(BeTrue())
		Expect(e.Name).To(Equal("Toy Engine"))
		_, ok = r.Get("nope")
		Expect(ok).To(BeFalse())

		// The first registered engine that matches wins.
		e, ok = r.ForBackend("cuda12-toy-development")
		Expect(ok).To(BeTrue())
		Expect(e.ID).To(Equal("toy-engine"))
		_, ok = r.ForBackend("")
		Expect(ok).To(BeFalse())
		_, ok = r.ForBackend("llama-cpp")
		Expect(ok).To(BeFalse())
	})

	It("lists the backend names an engine matches in its info", func() {
		in := toyEngine().Info()
		Expect(in.ID).To(Equal("toy-engine"))
		Expect(in.Backends.Families).To(Equal([]string{"toy"}))
		Expect(in.Binary).To(Equal("toy-serve"))
	})
})

var _ = Describe("BackendMatch", func() {
	m := es.BackendMatch{Names: []string{"old-name"}, Families: []string{"fam"}, Excluded: []string{"other-fam"}, Empty: true}
	DescribeTable("matches",
		func(backend string, want bool) { Expect(m.Matches(backend)).To(Equal(want)) },
		Entry("exact name", "old-name", true),
		Entry("a name is not a family", "cuda12-old-name", false),
		Entry("family", "fam", true),
		Entry("hardware variant of the family", "vulkan-fam", true),
		Entry("release channel", "fam-quantization", true),
		Entry("dots as hyphens", "Old.Name", true),
		Entry("empty", "  ", true),
		Entry("excluded", "other-fam", false),
		Entry("variant of excluded", "cpu-other-fam", false),
		Entry("unrelated", "famous", false),
	)
})

var _ = Describe("layer ranges", func() {
	It("parse, merge and format", func() {
		rs, err := es.ParseLayerRanges("18-23, 5,0-6")
		Expect(err).ToNot(HaveOccurred())
		Expect(es.FormatLayerRanges(rs)).To(Equal("0-6,18-23"))
		for _, bad := range []string{"a", "5-3", "-1", "1000", "1-x"} {
			_, err := es.ParseLayerRanges(bad)
			Expect(err).To(HaveOccurred(), bad)
		}
	})
})
