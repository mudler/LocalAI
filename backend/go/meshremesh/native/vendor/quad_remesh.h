#pragma once

// Quad remeshing of the dense TRELLIS dual-grid soup into mid-poly,
// quad-dominant topology, backed by the vendored MIT AutoRemesher core
// (third_party/autoremesher). The wrapper stays dependency-free: builds without
// the backend compile the same API and report the feature as unavailable.
//
// This is NOT a replacement for the CGAL Alpha Wrap print path. AutoRemesher
// skips islands whose parameterization fails and its quad extraction can leave
// boundaries, so the result is not guaranteed to be closed. Alpha Wrap remains
// the watertight-for-printing backend; this is the topology backend.
//
// See docs/plan/autoremesher-quad-remesh.md.

#include "trellis2.h" // TRELLIS2_API

#include <cstdint>
#include <string>
#include <vector>

namespace t2quad
{

TRELLIS2_API bool available();

struct QuadRemeshOptions {
	// Upstream's "target quads", mapped onto twice as many triangles the way
	// the AutoRemesher UI does. Treat it as a density hint, not a face count:
	// the value feeds a voxel size and adaptivity moves the result a long way
	// from it (20000 requested produced ~2300 faces in the Phase 1 measurements).
	int	  target_quads		= 20000;
	float edge_scaling		= 0.0f;	 // 1.0 - 4.0; <= 0 leaves it unset, as upstream does
	float adaptivity		= 1.0f;	 // 0.0 - 1.0, curvature-adaptive density
	float anisotropy		= 1.0f;	 // 0.0 - 1.0, curvature-adaptive elongation
	float sharp_edge_deg	= 90.0f; // 30 - 180
	float smooth_normal_deg = 0.0f;	 // 0 - 180
	bool  hard_surface		= false; // ModelType::HardSurface

	// Break vertices where the surface pinches into several fans, so the
	// halfedge mesh inside AutoRemesher can represent the input. Measured on
	// the synthetic fixtures this changes nothing once degenerate triangles are
	// gone (those turned out to be the actual trigger there), but it is cheap,
	// it fires on real junctions, and the failure it guards against is silent.
	// Disable only to compare.
	bool  split_non_manifold = true;

	// Optional decimation of the *input* before remeshing, as a triangle count.
	// <= 0 disables it. The dual grid is far denser than the parameterizer
	// needs, and its cost grows with input size.
	int	  input_triangle_budget = 0;

	// Fail instead of returning a half-remeshed model when AutoRemesher drops
	// islands. Compared as output surface area over input surface area; the
	// API itself reports nothing about dropped islands, so this is the only
	// signal available. 0 disables the check.
	float min_area_retained = 0.5f;
};

// Reported so callers can surface quality rather than guess at it.
struct QuadRemeshStats {
	int	  input_tris_after_prep = 0;
	int	  vertices_split		= 0; // non-manifold junctions broken open
	int	  faces_dropped			= 0; // degenerate + duplicate
	int	  quads					= 0;
	int	  triangles				= 0;
	int	  ngons					= 0;
	int	  boundary_edges		= 0; // > 0 means the result is not closed
	float area_retained			= 0.0f;
};

// progress is optional; it is called with 0..1 and a short status string, on
// the calling thread's behalf but from a worker, exactly as the vendored core
// reports it.
typedef void ( *ProgressFn )( void* user, float progress, const char* status );

// Quad-dominant output: `out_faces` is a flat index stream and `out_face_sizes`
// gives the vertex count per face (4 for quads, 3 and n for the rest), so no
// downstream code has to assume quads.
TRELLIS2_API bool remesh( const std::vector<float>& verts,
	const std::vector<int32_t>&						tris,
	const QuadRemeshOptions&						opt,
	std::vector<float>&								out_verts,
	std::vector<int32_t>&							out_faces,
	std::vector<int32_t>&							out_face_sizes,
	QuadRemeshStats&								stats,
	std::string&									err,
	ProgressFn										progress	  = nullptr,
	void*											progress_user = nullptr );

// Split a quad-dominant face stream into triangles: fan for triangles and
// n-gons, shorter-diagonal split for quads (which keeps the split off the
// long, thin direction where it would be most visible).
TRELLIS2_API void triangulate( const std::vector<float>& verts, const std::vector<int32_t>& faces, const std::vector<int32_t>& face_sizes, std::vector<int32_t>& out_tris );

} // namespace t2quad
