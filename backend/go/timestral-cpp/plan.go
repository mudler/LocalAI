package main

import (
	"fmt"
	"math"
	"sort"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// quantileTolerance is how far a requested level may be from a model level.
// The model returns the levels it was trained on and nothing in between.
const quantileTolerance = 1e-6

func invalidArgument(format string, args ...any) error {
	return status.Errorf(codes.InvalidArgument, "timestral-cpp: "+format, args...)
}

// shape is everything one ABI call needs equal across its series: the ABI takes
// one [B,V,T] block with no padding fields.
type shape struct {
	V, T            int32
	cPast, cKnown   int32
	ccPast, ccKnown int32
}

func shapeOf(s *pb.ForecastSeries) shape {
	return shape{
		V: s.GetNumVariates(), T: s.GetLength(),
		cPast: s.GetNumPastCovariates(), cKnown: s.GetNumKnownCovariates(),
		ccPast: s.GetNumPastCategorical(), ccKnown: s.GetNumKnownCategorical(),
	}
}

// group is a run of series that share a shape. idx holds their positions in the
// request, so results can go back to the order the caller sent.
type group struct {
	shape shape
	idx   []int
}

// validateSeries checks counts against array lengths for every series and the
// request limits. The HTTP layer checks the same things, but a gRPC caller may
// not, and the ABI reads caller buffers without a length field.
func validateSeries(series []*pb.ForecastSeries, h int32, opts loadOptions) error {
	if len(series) == 0 {
		return invalidArgument("no series")
	}
	if len(series) > opts.maxSeries {
		return invalidArgument("%d series exceed the limit of %d (option max_series)", len(series), opts.maxSeries)
	}
	total := 0
	for i, s := range series {
		sh := shapeOf(s)
		if sh.V < 1 || sh.T < 1 {
			return invalidArgument("series %d: need at least one variate and one step", i)
		}
		if sh.cPast < 0 || sh.cKnown < 0 || sh.ccPast < 0 || sh.ccKnown < 0 {
			return invalidArgument("series %d: negative covariate count", i)
		}
		th := int64(sh.T) + int64(h)
		for _, c := range []struct {
			name      string
			got, want int64
		}{
			{"target", int64(len(s.GetTarget())), int64(sh.V) * int64(sh.T)},
			{"past_covariates", int64(len(s.GetPastCovariates())), int64(sh.cPast) * int64(sh.T)},
			{"known_covariates", int64(len(s.GetKnownCovariates())), int64(sh.cKnown) * th},
			{"past_categorical", int64(len(s.GetPastCategorical())), int64(sh.ccPast) * int64(sh.T)},
			{"known_categorical", int64(len(s.GetKnownCategorical())), int64(sh.ccKnown) * th},
		} {
			if c.got != c.want {
				return invalidArgument("series %d: %s holds %d values, expected %d", i, c.name, c.got, c.want)
			}
			total += int(c.got)
		}
		if total > opts.maxPoints {
			return invalidArgument("request exceeds %d input values (option max_total_points)", opts.maxPoints)
		}
	}
	return nil
}

// planGroups splits the series by shape, in order of first appearance.
func planGroups(series []*pb.ForecastSeries) []group {
	var groups []group
	at := map[shape]int{}
	for i, s := range series {
		sh := shapeOf(s)
		g, ok := at[sh]
		if !ok {
			g = len(groups)
			at[sh] = g
			groups = append(groups, group{shape: sh})
		}
		groups[g].idx = append(groups[g].idx, i)
	}
	return groups
}

// batch holds the concatenated buffers of one group, ready for one ABI call.
// Every series keeps its own [rows][steps] layout, so plain concatenation gives
// the ABI's B-major layout.
type batch struct {
	context  []float32
	pastCov  []float32
	knownCov []float32
	catPast  []float64
	catKnown []float64
}

func buildBatch(series []*pb.ForecastSeries, g group) batch {
	var b batch
	for _, i := range g.idx {
		s := series[i]
		b.context = append(b.context, s.GetTarget()...)
		b.pastCov = append(b.pastCov, s.GetPastCovariates()...)
		b.knownCov = append(b.knownCov, s.GetKnownCovariates()...)
		b.catPast = append(b.catPast, s.GetPastCategorical()...)
		b.catKnown = append(b.catKnown, s.GetKnownCategorical()...)
	}
	return b
}

// selectQuantiles maps the requested levels (or the defaults when none are
// requested) onto indexes of the model levels. The result is ascending and has
// no duplicates. A level the model does not have is an error that lists the
// valid ones.
func selectQuantiles(requested, defaults, modelLevels []float64) (idx []int, levels []float64, err error) {
	want := requested
	if len(want) == 0 {
		want = defaults
	}
	seen := map[int]bool{}
	for _, q := range want {
		found := -1
		for i, l := range modelLevels {
			if math.Abs(l-q) <= quantileTolerance {
				found = i
				break
			}
		}
		if found < 0 {
			strs := make([]string, len(modelLevels))
			for i, l := range modelLevels {
				strs[i] = fmt.Sprintf("%g", l)
			}
			return nil, nil, invalidArgument("quantile %g is not a level of this model; valid levels: %s", q, strings.Join(strs, ", "))
		}
		seen[found] = true
	}
	for i := range seen {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		levels = append(levels, modelLevels[i])
	}
	return idx, levels, nil
}

// reshape turns one group's ABI output [B,V,H,Q] into one result per series
// with values laid out [V][Q'][H], where Q' are the selected quantiles.
func reshape(out []float32, series []*pb.ForecastSeries, g group, h, modelQ int, sel []int) map[int]*pb.ForecastSeriesResult {
	results := make(map[int]*pb.ForecastSeriesResult, len(g.idx))
	v := int(g.shape.V)
	for b, i := range g.idx {
		vals := make([]float32, 0, v*len(sel)*h)
		for vi := 0; vi < v; vi++ {
			for _, q := range sel {
				for step := 0; step < h; step++ {
					vals = append(vals, out[((b*v+vi)*h+step)*modelQ+q])
				}
			}
		}
		results[i] = &pb.ForecastSeriesResult{Id: series[i].GetId(), NumVariates: g.shape.V, Values: vals}
	}
	return results
}
