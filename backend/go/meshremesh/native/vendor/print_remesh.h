#pragma once

// Optional CGAL Alpha Wrap backend for turning TRELLIS' arbitrary triangle
// soup into a closed solid.  The public wrapper stays dependency-free: builds
// without CGAL compile the same API and report the feature as unavailable.

#include "trellis2.h" // TRELLIS2_API

#include <cstdint>
#include <string>
#include <vector>

namespace t2print
{

// Alpha Wrap availability: still CGAL-only, fixed at build time.
TRELLIS2_API bool		 available();

// Closest-surface PBR projection availability. Unconditionally true since the
// tinybvh backend landed — unlike alpha_wrap, project_pbr no longer needs CGAL.
TRELLIS2_API bool		 projection_available();

// "cgal" or "tinybvh". Reporting only; both are expected to agree within the
// tolerance documented in tests/test_print_remesh.cpp.
TRELLIS2_API const char* projection_backend();

// alpha_ratio and offset_ratio are fractions of the input bounding-box
// diagonal (for example 0.01 and 0.01/30).  Alpha controls the smallest holes
// and cavities the wrap can enter; offset controls how tightly it encloses the
// source.  Alpha Wrap creates an entirely new surface, so material transfer is
// a separate closest-surface projection step (project_pbr below).
//
// The source is capped before wrapping (see the note in alpha_wrap): an opening
// wider than alpha would let the carving ball inside and return a two-walled
// membrane instead of a solid.  out_capped, when given, receives the number of
// openings that were closed, so a caller can report how open its input was.
TRELLIS2_API bool		 alpha_wrap( const std::vector<float>& source_verts,
		   const std::vector<int32_t>&						   source_tris,
		   float											   alpha_ratio,
		   float											   offset_ratio,
		   std::vector<float>&								   out_verts,
		   std::vector<float>&								   out_normals,
		   std::vector<int32_t>&							   out_tris,
		   std::string&										   err,
		   int*												   out_capped = nullptr );

// Project query points onto the closest source triangles and barycentrically
// interpolate their six-channel per-vertex PBR values.  This is the portable
// CPU counterpart of upstream's cuBVH texture reprojection.  Query/output order
// is preserved; query_points contains xyz triples and out_pbr receives six
// floats per query.
// Same as project_pbr, but forces a named backend ("cgal" or "tinybvh")
// instead of the build-time default. Exists so the two can be compared in one
// binary; returns false with a reason when the requested backend is not
// compiled in. Pass nullptr for the default.
TRELLIS2_API bool		 project_pbr_backend( const char* backend,
		   const std::vector<float>&					  source_verts,
		   const std::vector<int32_t>&					  source_tris,
		   const std::vector<float>&					  source_pbr,
		   const std::vector<float>&					  query_points,
		   std::vector<float>&							  out_pbr,
		   std::string&									  err );

TRELLIS2_API bool		 project_pbr( const std::vector<float>& source_verts,
		   const std::vector<int32_t>&							source_tris,
		   const std::vector<float>&							source_pbr,
		   const std::vector<float>&							query_points,
		   std::vector<float>&									out_pbr,
		   std::string&											err );

} // namespace t2print
