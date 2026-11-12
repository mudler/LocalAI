// SPDX-License-Identifier: MIT
package localai

import (
	"context"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"golang.org/x/sync/errgroup"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/harnessctx"
	"github.com/mudler/LocalAI/core/gallery"
	"github.com/mudler/LocalAI/pkg/xsysinfo"
	"github.com/mudler/xlog"
)

// RecommendedHardware is the machine the recommendation is made for.
type RecommendedHardware struct {
	Memory config.HostMemory
	// Scope is "local" for the host the server runs on, "cluster" for the best
	// single node of a distributed install.
	Scope    string
	NodeName string
}

// Detected reports whether any memory figure is known.
func (h RecommendedHardware) Detected() bool {
	return h.Memory.PerDeviceVRAM > 0 || h.Memory.SystemRAM > 0
}

// LocalRecommendedHardware reads the host the server runs on, from the same
// sources as the resource monitor and the context sizing of the loader.
func LocalRecommendedHardware(context.Context) RecommendedHardware {
	var hw RecommendedHardware
	hw.Scope = "local"
	hw.Memory.PerDeviceVRAM, _ = xsysinfo.MinPerGPUVRAM()
	if info, err := xsysinfo.GetSystemRAMInfo(); err == nil && info != nil {
		hw.Memory.SystemRAM = info.Total
	}
	return hw
}

// RecommendedModelsHardware tells the recommendation what machine to size for.
type RecommendedModelsHardware func(ctx context.Context) RecommendedHardware

// RecommendedHardwareInfo describes the machine a recommendation was made for.
type RecommendedHardwareInfo struct {
	// Detected is false when no memory figure could be read; the cpu tier is
	// then shown without a fit verdict.
	Detected bool `json:"detected"`
	// Kind is "gpu", "cpu" or "unknown".
	Kind string `json:"kind"`
	// MemoryBytes is the per-device VRAM on a GPU host and the system memory
	// otherwise.
	MemoryBytes uint64 `json:"memory_bytes"`
	// Scope is "local" or "cluster"; NodeName names the cluster node sized for.
	Scope    string `json:"scope"`
	NodeName string `json:"node_name,omitempty"`
}

// RecommendedModel is one suggestion.
type RecommendedModel struct {
	// ID is the gallery name; pass it to the install route.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Role is "default" or "alternative", as the curated list has it.
	Role string `json:"role"`
	// Reason is the one-line reason the curated list gives.
	Reason  string `json:"reason"`
	License string `json:"license,omitempty"`
	// ContextSize is the window the install will use. Pass it as the
	// context_size parameter of the install route so the installed config
	// carries it. 0 when it could not be computed; then install without it.
	ContextSize int `json:"context_size"`
	// MemoryBytes is the estimated memory at ContextSize.
	MemoryBytes uint64 `json:"memory_bytes"`
	// SizeBytes is the size of the weights to download.
	SizeBytes uint64 `json:"size_bytes"`
	// Fit is "fits", "tight", "does_not_fit" or "unknown".
	Fit string `json:"fit"`
	// FitReasons are stable keys a client turns into text.
	FitReasons []string `json:"fit_reasons"`
	Installed  bool     `json:"installed"`
}

// RecommendedModelsResponse is the answer of GET /api/models/recommended.
type RecommendedModelsResponse struct {
	// Tier is the hardware tier: cpu, gpu-small, gpu-mid or gpu-large.
	Tier     string                  `json:"tier"`
	Hardware RecommendedHardwareInfo `json:"hardware"`
	// Models are ranked best first. The first one is the recommendation.
	Models []RecommendedModel `json:"models"`
	// NothingFits is true when no curated model fits the machine. Models then
	// holds the smallest one, with a fit of does_not_fit.
	NothingFits bool `json:"nothing_fits"`
	// MinContext and RecommendedContext are the chat windows the fit is judged by.
	MinContext         int `json:"min_context"`
	RecommendedContext int `json:"recommended_context"`
}

const (
	// recommendedCacheTTL bounds how long a computed answer is reused. The
	// answer depends on the hardware and on the gallery, which changes slowly.
	recommendedCacheTTL = 10 * time.Minute
	// recommendedHeaderTimeout bounds the header reads of one computation. A
	// header is a few ranged requests; the weights are never downloaded.
	recommendedHeaderTimeout = 25 * time.Second
)

type recommendedKey struct {
	hardware config.HostMemory
	scope    string
	node     string
}

type recommendedCached struct {
	response RecommendedModelsResponse
	at       time.Time
}

// RecommendedModelsService answers the first-run recommendation.
type RecommendedModelsService struct {
	hardware RecommendedModelsHardware
	// loadList returns the curated list: the file served with the gallery, or
	// the built-in one.
	loadList func(context.Context) gallery.RecommendedList
	// memory reads what the planner needs to know about a gallery entry. It is
	// a field so a source other than the GGUF header can replace it, and so
	// tests do not need the network.
	memory gallery.MemoryModelReader
	// models lists the gallery; a field so tests do not share the process-wide
	// gallery cache.
	models func() (gallery.GalleryElements[*gallery.GalleryModel], error)

	mu       sync.Mutex
	profiles map[string]config.MemoryModel
	answers  map[recommendedKey]recommendedCached
	inflight map[recommendedKey]chan struct{}
}

// RecommendedModelsSources are the inputs of the service. Each one can be
// replaced: a test supplies them without a network, and a source of memory
// figures other than GGUF headers plugs in at Memory.
type RecommendedModelsSources struct {
	// Hardware is the machine to size for.
	Hardware RecommendedModelsHardware
	// List returns the curated list: the file served with the gallery, or the
	// built-in one. It is read when an answer is computed, so a change served
	// with the gallery takes effect without a restart.
	List func(context.Context) gallery.RecommendedList
	// Memory reads what the planner needs to know about a gallery entry.
	Memory gallery.MemoryModelReader
	// Models lists the gallery.
	Models func() (gallery.GalleryElements[*gallery.GalleryModel], error)
}

// NewRecommendedModelsService builds the service with the real sources: the
// host (or the cluster's best node), the list served with the gallery, GGUF
// headers read from the remote files, and the cached gallery.
func NewRecommendedModelsService(appConfig *config.ApplicationConfig, hardware RecommendedModelsHardware) *RecommendedModelsService {
	if hardware == nil {
		hardware = LocalRecommendedHardware
	}
	return NewRecommendedModelsServiceFrom(RecommendedModelsSources{
		Hardware: hardware,
		List: func(ctx context.Context) gallery.RecommendedList {
			list, _ := gallery.LoadRecommendedModels(ctx, appConfig.Galleries, appConfig.SystemState.Model.ModelsPath, appConfig.SystemState.RequireBackendIntegrity)
			return list
		},
		Memory: gallery.ReadGGUFMemoryModel,
		Models: func() (gallery.GalleryElements[*gallery.GalleryModel], error) {
			return gallery.AvailableGalleryModelsCached(appConfig.Galleries, appConfig.SystemState)
		},
	})
}

// NewRecommendedModelsServiceFrom builds the service from the given sources.
func NewRecommendedModelsServiceFrom(src RecommendedModelsSources) *RecommendedModelsService {
	return &RecommendedModelsService{
		hardware: src.Hardware,
		loadList: src.List,
		memory:   src.Memory,
		models:   src.Models,
		profiles: map[string]config.MemoryModel{},
		answers:  map[recommendedKey]recommendedCached{},
		inflight: map[recommendedKey]chan struct{}{},
	}
}

// RecommendedModelsEndpoint recommends models for the machine the models run
// on.
// @Summary Recommend models for this machine.
// @Description Returns the curated models for the hardware tier of the machine, best first, with the context window each would be installed with and whether it fits. Every signed-in user may read it; installing stays admin-only. The hardware is the host, or the best single node of a cluster.
// @Tags models
// @Success 200 {object} localai.RecommendedModelsResponse "Response"
// @Router /api/models/recommended [get]
func RecommendedModelsEndpoint(svc *RecommendedModelsService) echo.HandlerFunc {
	return func(c echo.Context) error {
		resp, err := svc.Recommend(c.Request().Context())
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]any{"error": err.Error()})
		}
		return c.JSON(http.StatusOK, resp)
	}
}

// Recommend computes the answer for the current hardware, or returns the one
// computed for the same hardware a short while ago.
func (s *RecommendedModelsService) Recommend(ctx context.Context) (RecommendedModelsResponse, error) {
	hw := s.hardware(ctx)
	key := recommendedKey{hardware: hw.Memory, scope: hw.Scope, node: hw.NodeName}

	for {
		s.mu.Lock()
		if c, ok := s.answers[key]; ok && time.Since(c.at) < recommendedCacheTTL {
			s.mu.Unlock()
			return c.response, nil
		}
		wait, running := s.inflight[key]
		if !running {
			s.inflight[key] = make(chan struct{})
			s.mu.Unlock()
			break
		}
		s.mu.Unlock()
		// Another request is reading the same headers: wait for it instead of
		// repeating the work.
		select {
		case <-wait:
		case <-ctx.Done():
			return RecommendedModelsResponse{}, ctx.Err()
		}
	}

	resp, complete, err := s.compute(ctx, hw)
	s.mu.Lock()
	if err == nil && complete {
		s.answers[key] = recommendedCached{response: resp, at: time.Now()}
	}
	close(s.inflight[key])
	delete(s.inflight, key)
	s.mu.Unlock()
	return resp, err
}

// candidate is a curated entry with what is known about it on this host.
type candidate struct {
	model gallery.RecommendedModel
	role  string
	tier  string
	entry *gallery.GalleryModel
	plan  config.ContextPlan
	size  uint64
	known bool // the memory model was read
}

func fitRank(f config.ContextFit) int {
	switch f {
	case config.FitFits:
		return 0
	case config.FitTight:
		return 1
	case config.FitUnknown:
		return 2
	default:
		return 3
	}
}

// compute builds the answer. complete is false when a header could not be
// read, so the answer is not kept and the next request tries again.
func (s *RecommendedModelsService) compute(ctx context.Context, hw RecommendedHardware) (RecommendedModelsResponse, bool, error) {
	tier := gallery.TierForMemory(hw.Memory.PerDeviceVRAM)
	resp := RecommendedModelsResponse{
		Tier: tier,
		Hardware: RecommendedHardwareInfo{
			Detected: hw.Detected(), Kind: "unknown", Scope: hw.Scope, NodeName: hw.NodeName,
		},
		Models:             []RecommendedModel{},
		MinContext:         harnessctx.MinContext,
		RecommendedContext: harnessctx.RecommendedContext,
	}
	switch {
	case hw.Memory.PerDeviceVRAM > 0:
		resp.Hardware.Kind, resp.Hardware.MemoryBytes = "gpu", hw.Memory.PerDeviceVRAM
	case hw.Memory.SystemRAM > 0:
		resp.Hardware.Kind, resp.Hardware.MemoryBytes = "cpu", hw.Memory.SystemRAM
	}

	all, err := s.models()
	if err != nil {
		return resp, false, err
	}

	list := s.loadList(ctx)

	// The tier of the host first, then the smaller tiers: they are the fallback
	// when nothing of the host's own tier fits.
	var own, lower []*candidate
	seen := map[string]bool{}
	add := func(tierName string, into *[]*candidate) {
		for n, m := range list.Tiers[tierName].Models() {
			if seen[m.ID] {
				continue
			}
			entry := gallery.FindGalleryElement(all, m.ID)
			if entry == nil {
				continue
			}
			seen[m.ID] = true
			role := "alternative"
			if n == 0 {
				role = "default"
			}
			*into = append(*into, &candidate{model: m, role: role, tier: tierName, entry: entry})
		}
	}
	add(tier, &own)
	for i := len(gallery.RecommendedTiers) - 1; i >= 0; i-- {
		if gallery.RecommendedTiers[i] != tier {
			continue
		}
		for j := i - 1; j >= 0; j-- {
			add(gallery.RecommendedTiers[j], &lower)
		}
	}

	complete := s.plan(ctx, hw, append(append([]*candidate{}, own...), lower...))

	rank := func(cs []*candidate) []*candidate {
		var out []*candidate
		for _, c := range cs {
			if c.plan.Fit != config.FitNone {
				out = append(out, c)
			}
		}
		// Stable, so the curated order decides between two with the same verdict.
		sort.SliceStable(out, func(a, b int) bool { return fitRank(out[a].plan.Fit) < fitRank(out[b].plan.Fit) })
		return out
	}
	chosen := rank(own)
	if !fitsAny(chosen) {
		if wider := rank(lower); fitsAny(wider) {
			chosen = append(chosen, wider...)
			sort.SliceStable(chosen, func(a, b int) bool { return fitRank(chosen[a].plan.Fit) < fitRank(chosen[b].plan.Fit) })
		}
	}
	if len(chosen) == 0 {
		// Nothing fits: say so, with the smallest model there is.
		var smallest *candidate
		for _, c := range append(append([]*candidate{}, own...), lower...) {
			if c.plan.MemoryBytes == 0 {
				continue
			}
			if smallest == nil || c.plan.MemoryBytes < smallest.plan.MemoryBytes {
				smallest = c
			}
		}
		if smallest != nil {
			chosen = []*candidate{smallest}
			resp.NothingFits = true
		}
	}

	for _, c := range chosen {
		resp.Models = append(resp.Models, RecommendedModel{
			ID: c.model.ID, Name: c.model.DisplayName, Role: c.role, Reason: c.model.Reason,
			License:     c.entry.License,
			ContextSize: planContext(c), MemoryBytes: c.plan.MemoryBytes, SizeBytes: c.size,
			Fit: string(c.plan.Fit), FitReasons: append([]string{}, c.plan.Reasons...),
			Installed: c.entry.Installed,
		})
	}
	return resp, complete, nil
}

func fitsAny(cs []*candidate) bool {
	for _, c := range cs {
		if c.plan.Fit == config.FitFits || c.plan.Fit == config.FitTight {
			return true
		}
	}
	return false
}

// planContext is the window to install with: 0 when the plan has no real
// figure, so the install leaves the window to the loader.
func planContext(c *candidate) int {
	if !c.known || c.plan.Fit == config.FitUnknown {
		return 0
	}
	return c.plan.ContextSize
}

// plan reads the headers of the candidates in parallel and fills in their
// plans. It reports whether every header could be read.
func (s *RecommendedModelsService) plan(ctx context.Context, hw RecommendedHardware, cs []*candidate) bool {
	// A client that goes away must not cancel work other requests wait for.
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recommendedHeaderTimeout)
	defer cancel()

	var failed bool
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(rctx)
	g.SetLimit(4)
	for _, c := range cs {
		g.Go(func() error {
			model, err := s.cachedMemoryModel(gctx, c.entry)
			if err != nil {
				xlog.Debug("recommended models: header not available", "model", c.model.ID, "error", err)
				mu.Lock()
				failed = true
				mu.Unlock()
				c.plan = config.ContextPlan{Fit: config.FitUnknown, Reasons: []string{config.ReasonEstimateUnavailable}}
				return nil
			}
			c.known = true
			c.size = model.WeightsBytes()
			c.plan = config.PlanContext(model, hw.Memory)
			return nil
		})
	}
	_ = g.Wait()
	if !hw.Detected() {
		// No memory figure: the plan has no verdict to give, and nothing is
		// dropped. The window is still the loader's.
		for _, c := range cs {
			if c.known {
				c.plan.Fit, c.plan.Reasons = config.FitUnknown, []string{config.ReasonHardwareUnknown}
			}
		}
	}
	return !failed
}

// cachedMemoryModel returns the memory model of an entry. A GGUF header is a
// property of the file, so it is kept for the life of the process.
func (s *RecommendedModelsService) cachedMemoryModel(ctx context.Context, entry *gallery.GalleryModel) (config.MemoryModel, error) {
	key := gallery.RecommendedWeightURI(entry)
	s.mu.Lock()
	m, ok := s.profiles[key]
	s.mu.Unlock()
	if ok {
		return m, nil
	}
	m, err := s.memory(ctx, entry)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.profiles[key] = m
	s.mu.Unlock()
	return m, nil
}
