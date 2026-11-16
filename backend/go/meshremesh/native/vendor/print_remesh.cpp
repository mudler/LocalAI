#include "print_remesh.h"

#include <algorithm>
#include <atomic>
#include <cmath>
#include <cstddef>
#include <exception>
#include <limits>
#include <cstring>
#include <memory>
#include <mutex>
#include <thread>
#include <unordered_map>
#include <utility>

// Closest-surface PBR projection has two interchangeable backends. tinybvh is
// MIT and always available, so the texture path no longer depends on CGAL; the
// CGAL backend stays as the reference the tinybvh one is validated against.
// TRELLIS2_FORCE_TINYBVH selects tinybvh inside a CGAL build so both can be
// compared in one binary.
#if defined( TRELLIS2_USE_CGAL ) && !defined( TRELLIS2_FORCE_TINYBVH )
	#define T2_PROJECT_PBR_CGAL 1
#endif

#ifdef TRELLIS2_USE_CGAL

	#include <CGAL/Exact_predicates_inexact_constructions_kernel.h>
	#include <CGAL/AABB_tree.h>
	#if __has_include( <CGAL/AABB_traits_3.h>)
		#include <CGAL/AABB_traits_3.h>
		#include <CGAL/AABB_triangle_primitive_3.h>
		#define T2_CGAL_AABB_3_NAMES 1
	#else
		#include <CGAL/AABB_traits.h>
		#include <CGAL/AABB_triangle_primitive.h>
	#endif
	#include <CGAL/Surface_mesh.h>
	#include <CGAL/alpha_wrap_3.h>

	#include <array>

#endif

// Included after CGAL on purpose: Boost.MPL's preprocessed-header selection is
// fragile under clang's MSVC-compatibility mode, and pulling a large unrelated
// header in first was enough to break it ("preprocessed/plain / or.hpp").
#include <tiny_bvh.h>

namespace t2print
{

bool available()
{
#ifdef TRELLIS2_USE_CGAL
	return true;
#else
	return false;
#endif
}

bool projection_available()
{
	// Unconditional since the tinybvh backend landed. Kept as a function rather
	// than dropped so callers keep one place to ask, and so a future backend
	// that does need a build option has somewhere to report from.
	return true;
}

const char* projection_backend()
{
#ifdef T2_PROJECT_PBR_CGAL
	return "cgal";
#else
	return "tinybvh";
#endif
}

namespace
{

	void vertex_normals( const std::vector<float>& verts, const std::vector<int32_t>& tris, std::vector<float>& normals )
	{
		normals.assign( verts.size(), 0.0f );
		for( size_t t = 0; t + 2 < tris.size(); t += 3 ) {
			const int32_t ia = tris[t], ib = tris[t + 1], ic = tris[t + 2];
			const float*  a		= verts.data() + ( size_t )ia * 3;
			const float*  b		= verts.data() + ( size_t )ib * 3;
			const float*  c		= verts.data() + ( size_t )ic * 3;
			const float	  ab[3] = { b[0] - a[0], b[1] - a[1], b[2] - a[2] };
			const float	  ac[3] = { c[0] - a[0], c[1] - a[1], c[2] - a[2] };
			const float	  n[3]	= {
				   ab[1] * ac[2] - ab[2] * ac[1],
				   ab[2] * ac[0] - ab[0] * ac[2],
				   ab[0] * ac[1] - ab[1] * ac[0],
			};
			for( int32_t i : { ia, ib, ic } ) {
				normals[( size_t )i * 3 + 0] += n[0];
				normals[( size_t )i * 3 + 1] += n[1];
				normals[( size_t )i * 3 + 2] += n[2];
			}
		}
		for( size_t i = 0; i < verts.size() / 3; ++i ) {
			float*		n	= normals.data() + i * 3;
			const float len = std::sqrt( n[0] * n[0] + n[1] * n[1] + n[2] * n[2] );
			if( len > 1e-20f ) {
				n[0] /= len;
				n[1] /= len;
				n[2] /= len;
			}
		}
	}

} // namespace

bool alpha_wrap( const std::vector<float>& source_verts,
	const std::vector<int32_t>&			   source_tris,
	float								   alpha_ratio,
	float								   offset_ratio,
	std::vector<float>&					   out_verts,
	std::vector<float>&					   out_normals,
	std::vector<int32_t>&				   out_tris,
	std::string&						   err,
	int*								   out_capped )
{
	out_verts.clear();
	out_normals.clear();
	out_tris.clear();
	if( out_capped )
		*out_capped = 0;
#ifndef TRELLIS2_USE_CGAL
	( void )source_verts;
	( void )source_tris;
	( void )alpha_ratio;
	( void )offset_ratio;
	err = "print remeshing is unavailable (rebuild with CGAL >= 5.5)";
	return false;
#else
	if( source_verts.size() < 9 || source_tris.size() < 3 ) {
		err = "empty mesh";
		return false;
	}
	if( !std::isfinite( alpha_ratio ) || !std::isfinite( offset_ratio ) || alpha_ratio <= 0.0f || alpha_ratio > 0.5f || offset_ratio <= 0.0f || offset_ratio > 0.5f ) {
		err = "bad Alpha Wrap parameters";
		return false;
	}

	using Kernel = CGAL::Exact_predicates_inexact_constructions_kernel;
	using Point	 = Kernel::Point_3;
	using Mesh	 = CGAL::Surface_mesh<Point>;

	try {
		std::vector<Point> points;
		points.reserve( source_verts.size() / 3 );
		double lo[3] = { 1e300, 1e300, 1e300 };
		double hi[3] = { -1e300, -1e300, -1e300 };
		for( size_t i = 0; i < source_verts.size() / 3; ++i ) {
			double p[3] = { source_verts[3 * i], source_verts[3 * i + 1], source_verts[3 * i + 2] };
			if( !std::isfinite( p[0] ) || !std::isfinite( p[1] ) || !std::isfinite( p[2] ) ) {
				err = "mesh contains a non-finite vertex";
				return false;
			}
			points.emplace_back( p[0], p[1], p[2] );
			for( int k = 0; k < 3; ++k ) {
				lo[k] = std::min( lo[k], p[k] );
				hi[k] = std::max( hi[k], p[k] );
			}
		}

		std::vector<std::array<std::size_t, 3>> faces;
		faces.reserve( source_tris.size() / 3 );
		for( size_t t = 0; t < source_tris.size() / 3; ++t ) {
			const int32_t a = source_tris[3 * t], b = source_tris[3 * t + 1], c = source_tris[3 * t + 2];
			if( a < 0 || b < 0 || c < 0 || ( size_t )a >= points.size() || ( size_t )b >= points.size() || ( size_t )c >= points.size() ) {
				err = "triangle index out of range";
				return false;
			}
			if( a == b || b == c || a == c )
				continue;
			faces.push_back( { { ( size_t )a, ( size_t )b, ( size_t )c } } );
		}
		if( faces.empty() ) {
			err = "mesh has no valid triangles";
			return false;
		}

		// Cap the source's openings before wrapping.
		//
		// alpha_wrap_3 carves inward from outside with a ball of radius alpha, so
		// any opening wider than that ball lets it into the interior, where it
		// hugs the surface from the inside as well. What comes back is then a
		// closed *membrane* — two walls a hair apart — rather than a solid: twice
		// the surface area, an enclosed volume near zero, and a second set of
		// faces on the inside that the quad stage afterwards faithfully remeshes.
		// A 1024 cascade figure leaves 4392 boundary loops, 51 of them wider than
		// the ball, and one is enough to turn the whole wrap inside-out this way.
		//
		// The caps only have to *block* the ball; they are never part of the
		// result, since the wrap resamples everything. So rather than tracing
		// boundary loops in order — which the dual grid's non-manifold junctions
		// defeat, and which is why the mesher's own fill_holes leaves these
		// behind — take each connected group of boundary edges and fan it to that
		// group's centroid. That needs no ordering and closes a group whatever
		// shape it has.
		{
			auto edge_key = []( size_t a, size_t b ) {
				const uint64_t x = ( uint64_t )a, y = ( uint64_t )b;
				return a < b ? ( x << 32 ) | y : ( y << 32 ) | x;
			};
			std::unordered_map<uint64_t, int> edge_use;
			edge_use.reserve( faces.size() * 2 );
			for( const auto& f : faces )
				for( int k = 0; k < 3; ++k )
					edge_use[edge_key( f[k], f[( k + 1 ) % 3] )]++;

			std::vector<size_t> parent( points.size() );
			for( size_t i = 0; i < parent.size(); ++i )
				parent[i] = i;
			auto find = [&]( size_t x ) {
				while( parent[x] != x ) {
					parent[x] = parent[parent[x]];
					x		  = parent[x];
				}
				return x;
			};
			std::vector<std::pair<size_t, size_t>> boundary;
			for( const auto& e : edge_use ) {
				if( e.second != 1 )
					continue; // used by one triangle only: an open edge
				const size_t a = ( size_t )( e.first >> 32 ), b = ( size_t )( e.first & 0xffffffffull );
				boundary.emplace_back( a, b );
				const size_t ra = find( a ), rb = find( b );
				if( ra != rb )
					parent[ra] = rb;
			}
			std::unordered_map<size_t, std::array<double, 4>> centre; // x, y, z, count
			for( const auto& e : boundary )
				for( const size_t v : { e.first, e.second } ) {
					auto& c = centre[find( v )];
					c[0] += CGAL::to_double( points[v].x() );
					c[1] += CGAL::to_double( points[v].y() );
					c[2] += CGAL::to_double( points[v].z() );
					c[3] += 1.0;
				}
			std::unordered_map<size_t, size_t> apex;
			for( const auto& c : centre ) {
				apex[c.first] = points.size();
				points.emplace_back( c.second[0] / c.second[3], c.second[1] / c.second[3], c.second[2] / c.second[3] );
			}
			for( const auto& e : boundary )
				faces.push_back( { { e.first, e.second, apex[find( e.first )] } } );
			if( out_capped )
				*out_capped = ( int )centre.size();
		}

		const double dx = hi[0] - lo[0], dy = hi[1] - lo[1], dz = hi[2] - lo[2];
		const double diagonal = std::sqrt( dx * dx + dy * dy + dz * dz );
		if( !( diagonal > 0.0 ) || !std::isfinite( diagonal ) ) {
			err = "mesh has an empty bounding box";
			return false;
		}

		Mesh wrap;
		CGAL::alpha_wrap_3( points, faces, diagonal * ( double )alpha_ratio, diagonal * ( double )offset_ratio, wrap );
		if( wrap.is_empty() || wrap.number_of_faces() == 0 ) {
			err = "CGAL Alpha Wrap produced an empty mesh";
			return false;
		}

		// Surface_mesh descriptors are indices but are not required to be
		// densely packed, so retain an explicit descriptor-to-output remap.
		std::vector<int32_t> remap;
		out_verts.reserve( wrap.number_of_vertices() * 3 );
		for( Mesh::Vertex_index v : wrap.vertices() ) {
			if( ( size_t )v.idx() >= remap.size() )
				remap.resize( ( size_t )v.idx() + 1, -1 );
			remap[v.idx()] = ( int32_t )( out_verts.size() / 3 );
			const Point& p = wrap.point( v );
			out_verts.push_back( ( float )CGAL::to_double( p.x() ) );
			out_verts.push_back( ( float )CGAL::to_double( p.y() ) );
			out_verts.push_back( ( float )CGAL::to_double( p.z() ) );
		}

		out_tris.reserve( wrap.number_of_faces() * 3 );
		for( Mesh::Face_index f : wrap.faces() ) {
			Mesh::Halfedge_index h = wrap.halfedge( f );
			for( int k = 0; k < 3; ++k ) {
				Mesh::Vertex_index v = wrap.target( h );
				if( ( size_t )v.idx() >= remap.size() || remap[v.idx()] < 0 ) {
					err = "CGAL Alpha Wrap returned an invalid face";
					return false;
				}
				out_tris.push_back( remap[v.idx()] );
				h = wrap.next( h );
			}
			if( h != wrap.halfedge( f ) ) {
				err = "CGAL Alpha Wrap returned a non-triangle face";
				return false;
			}
		}
		vertex_normals( out_verts, out_tris, out_normals );
		return true;
	} catch( const std::exception& ex ) {
		err = std::string( "CGAL Alpha Wrap failed: " ) + ex.what();
		return false;
	} catch( ... ) {
		err = "CGAL Alpha Wrap failed";
		return false;
	}
#endif
}

namespace
{

	// A triangle whose three vertices are collinear has no interior to project
	// onto and no well-defined barycentric basis. CGAL refuses such primitives in
	// an AABB tree outright; tinybvh would accept them. Both backends therefore
	// share this one filter, so a comparison between them is a comparison of the
	// search, not of two different inputs.
	bool degenerate_triangle( const std::vector<float>& verts, int32_t ia, int32_t ib, int32_t ic )
	{
		if( ia == ib || ib == ic || ia == ic )
			return true;
		const float* a	   = verts.data() + ( size_t )ia * 3;
		const float* b	   = verts.data() + ( size_t )ib * 3;
		const float* c	   = verts.data() + ( size_t )ic * 3;
		const double ab[3] = { ( double )b[0] - a[0], ( double )b[1] - a[1], ( double )b[2] - a[2] };
		const double ac[3] = { ( double )c[0] - a[0], ( double )c[1] - a[1], ( double )c[2] - a[2] };
		const double n[3]  = { ab[1] * ac[2] - ab[2] * ac[1], ab[2] * ac[0] - ab[0] * ac[2], ab[0] * ac[1] - ab[1] * ac[0] };
		return !( n[0] * n[0] + n[1] * n[1] + n[2] * n[2] > 0.0 );
	}

	// Closest point on a triangle to p, by Voronoi region. Own implementation of
	// the standard construction; no third-party source involved.
	void closest_point_on_triangle( const float p[3], const float a[3], const float b[3], const float c[3], float out[3] )
	{
		const float ab[3] = { b[0] - a[0], b[1] - a[1], b[2] - a[2] };
		const float ac[3] = { c[0] - a[0], c[1] - a[1], c[2] - a[2] };
		const float ap[3] = { p[0] - a[0], p[1] - a[1], p[2] - a[2] };

		const float d1 = ab[0] * ap[0] + ab[1] * ap[1] + ab[2] * ap[2];
		const float d2 = ac[0] * ap[0] + ac[1] * ap[1] + ac[2] * ap[2];
		if( d1 <= 0.0f && d2 <= 0.0f ) { // vertex a
			out[0] = a[0];
			out[1] = a[1];
			out[2] = a[2];
			return;
		}

		const float bp[3] = { p[0] - b[0], p[1] - b[1], p[2] - b[2] };
		const float d3	  = ab[0] * bp[0] + ab[1] * bp[1] + ab[2] * bp[2];
		const float d4	  = ac[0] * bp[0] + ac[1] * bp[1] + ac[2] * bp[2];
		if( d3 >= 0.0f && d4 <= d3 ) { // vertex b
			out[0] = b[0];
			out[1] = b[1];
			out[2] = b[2];
			return;
		}

		const float vc = d1 * d4 - d3 * d2;
		if( vc <= 0.0f && d1 >= 0.0f && d3 <= 0.0f ) { // edge ab
			const float v = d1 / ( d1 - d3 );
			for( int i = 0; i < 3; ++i )
				out[i] = a[i] + v * ab[i];
			return;
		}

		const float cp[3] = { p[0] - c[0], p[1] - c[1], p[2] - c[2] };
		const float d5	  = ab[0] * cp[0] + ab[1] * cp[1] + ab[2] * cp[2];
		const float d6	  = ac[0] * cp[0] + ac[1] * cp[1] + ac[2] * cp[2];
		if( d6 >= 0.0f && d5 <= d6 ) { // vertex c
			out[0] = c[0];
			out[1] = c[1];
			out[2] = c[2];
			return;
		}

		const float vb = d5 * d2 - d1 * d6;
		if( vb <= 0.0f && d2 >= 0.0f && d6 <= 0.0f ) { // edge ac
			const float w = d2 / ( d2 - d6 );
			for( int i = 0; i < 3; ++i )
				out[i] = a[i] + w * ac[i];
			return;
		}

		const float va = d3 * d6 - d5 * d4;
		if( va <= 0.0f && ( d4 - d3 ) >= 0.0f && ( d5 - d6 ) >= 0.0f ) { // edge bc
			const float w = ( d4 - d3 ) / ( ( d4 - d3 ) + ( d5 - d6 ) );
			for( int i = 0; i < 3; ++i )
				out[i] = b[i] + w * ( c[i] - b[i] );
			return;
		}

		const float denom = 1.0f / ( va + vb + vc ); // interior
		const float v	  = vb * denom;
		const float w	  = vc * denom;
		for( int i = 0; i < 3; ++i )
			out[i] = a[i] + ab[i] * v + ac[i] * w;
	}

	float point_aabb_dist2( const float p[3], const tinybvh::bvhvec3& lo, const tinybvh::bvhvec3& hi )
	{
		const float dx = p[0] < lo.x ? lo.x - p[0] : ( p[0] > hi.x ? p[0] - hi.x : 0.0f );
		const float dy = p[1] < lo.y ? lo.y - p[1] : ( p[1] > hi.y ? p[1] - hi.y : 0.0f );
		const float dz = p[2] < lo.z ? lo.z - p[2] : ( p[2] > hi.z ? p[2] - hi.z : 0.0f );
		return dx * dx + dy * dy + dz * dz;
	}

	// tinybvh builds and traverses rays; it has no nearest-primitive query
	// (IntersectSphere is a boolean overlap test, not a nearest lookup). So we
	// use it as the builder and descend its nodes ourselves. The node layout this
	// relies on is documented in third_party/tinybvh/VERSION.md.
	class TinyBvhSurface
	{
	public:
		bool build( const std::vector<float>& verts, const std::vector<int32_t>& faces, std::string& err )
		{
			m_verts = &verts;
			m_faces = &faces;

			// Expand positions to bvhvec4 rather than handing tinybvh a
			// 12-byte-strided slice: bvhvec4slice::operator[] reads a full
			// bvhvec4, so a 12-byte stride would read 4 bytes past the last
			// vertex. Per-vertex expansion is also cheaper than the non-indexed
			// path, which would expand per triangle corner.
			const size_t nv = verts.size() / 3;
			m_positions.resize( nv );
			for( size_t i = 0; i < nv; ++i )
				m_positions[i] = tinybvh::bvhvec4( verts[3 * i], verts[3 * i + 1], verts[3 * i + 2], 0.0f );

			m_indices.resize( faces.size() );
			for( size_t i = 0; i < faces.size(); ++i )
				m_indices[i] = ( uint32_t )faces[i];

			const uint32_t prim_count = ( uint32_t )( faces.size() / 3 );
			m_bvh.Build( tinybvh::bvhvec4slice( m_positions.data(), ( uint32_t )nv ), m_indices.data(), prim_count );
			if( !m_bvh.bvhNode || !m_bvh.primIdx ) {
				err = "tinybvh failed to build a BVH over the projection source";
				return false;
			}
			return true;
		}

		// Returns the compacted triangle index, or SIZE_MAX if the traversal
		// stack overflowed. Never returns a silently wrong nearest hit.
		size_t closest( const float q[3], float out_point[3] ) const
		{
			const tinybvh::BVH::BVHNode* nodes = m_bvh.bvhNode;

			// Depth 128 is double the 64 that tinybvh's own ray traversal
			// assumes, so this cannot realistically trip; if it ever does we
			// report failure rather than dropping a subtree and returning a
			// plausible but wrong answer.
			uint32_t					 stack[128];
			float						 stack_dist[128];
			int							 sp = 0;

			float						 best_dist = std::numeric_limits<float>::max();
			size_t						 best_tri  = ( size_t )-1;
			float						 best_point[3] { 0.0f, 0.0f, 0.0f };

			uint32_t					 node	   = 0;
			float						 node_dist = 0.0f;
			for( ;; ) {
				if( node_dist < best_dist ) {
					const tinybvh::BVH::BVHNode& n = nodes[node];
					if( n.triCount > 0 ) {
						for( uint32_t i = 0; i < n.triCount; ++i ) {
							const uint32_t tri = m_bvh.primIdx[n.leftFirst + i];
							const int32_t* ids = m_faces->data() + ( size_t )tri * 3;
							const float*   a   = m_verts->data() + ( size_t )ids[0] * 3;
							const float*   b   = m_verts->data() + ( size_t )ids[1] * 3;
							const float*   c   = m_verts->data() + ( size_t )ids[2] * 3;
							float		   hit[3];
							closest_point_on_triangle( q, a, b, c, hit );
							const float dx = hit[0] - q[0], dy = hit[1] - q[1], dz = hit[2] - q[2];
							const float d2 = dx * dx + dy * dy + dz * dz;
							if( d2 < best_dist ) {
								best_dist	  = d2;
								best_tri	  = tri;
								best_point[0] = hit[0];
								best_point[1] = hit[1];
								best_point[2] = hit[2];
							}
						}
					} else {
						uint32_t near_child = n.leftFirst, far_child = n.leftFirst + 1;
						float	 near_dist = point_aabb_dist2( q, nodes[near_child].aabbMin, nodes[near_child].aabbMax );
						float	 far_dist  = point_aabb_dist2( q, nodes[far_child].aabbMin, nodes[far_child].aabbMax );
						if( near_dist > far_dist ) {
							std::swap( near_child, far_child );
							std::swap( near_dist, far_dist );
						}
						if( far_dist < best_dist ) {
							if( sp >= 128 )
								return ( size_t )-1;
							stack[sp]	   = far_child;
							stack_dist[sp] = far_dist;
							++sp;
						}
						if( near_dist < best_dist ) {
							node	  = near_child;
							node_dist = near_dist;
							continue;
						}
					}
				}
				if( sp == 0 )
					break;
				--sp;
				node	  = stack[sp];
				node_dist = stack_dist[sp];
			}

			out_point[0] = best_point[0];
			out_point[1] = best_point[1];
			out_point[2] = best_point[2];
			return best_tri;
		}

	private:
		tinybvh::BVH				  m_bvh;
		std::vector<tinybvh::bvhvec4> m_positions;
		std::vector<uint32_t>		  m_indices;
		const std::vector<float>*	  m_verts = nullptr;
		const std::vector<int32_t>*	  m_faces = nullptr;
	};

#ifdef TRELLIS2_USE_CGAL

	// The reference backend. Kept compiled alongside tinybvh whenever CGAL is
	// present so tests/test_print_remesh.cpp can run both in one binary and
	// compare them; production dispatch is still the compile-time default.
	class CgalSurface
	{
	public:
		using Kernel			= CGAL::Exact_predicates_inexact_constructions_kernel;
		using Point				= Kernel::Point_3;
		using Triangle			= Kernel::Triangle_3;
		using Triangle_iterator = std::vector<Triangle>::const_iterator;
	#ifdef T2_CGAL_AABB_3_NAMES
		using Primitive = CGAL::AABB_triangle_primitive_3<Kernel, Triangle_iterator>;
		using Traits	= CGAL::AABB_traits_3<Kernel, Primitive>;
	#else
		// These compatibility names are used by CGAL 5.5, the first Alpha Wrap
		// release. CGAL 6 selects the non-deprecated aliases above.
		using Primitive = CGAL::AABB_triangle_primitive<Kernel, Triangle_iterator>;
		using Traits	= CGAL::AABB_traits<Kernel, Primitive>;
	#endif
		using Tree = CGAL::AABB_tree<Traits>;

		bool build( const std::vector<float>& verts, const std::vector<int32_t>& faces, std::string& err )
		{
			( void )err;
			m_triangles.reserve( faces.size() / 3 );
			for( size_t t = 0; t < faces.size() / 3; ++t ) {
				const int32_t ia = faces[3 * t], ib = faces[3 * t + 1], ic = faces[3 * t + 2];
				m_triangles.emplace_back( Point( verts[3 * ( size_t )ia], verts[3 * ( size_t )ia + 1], verts[3 * ( size_t )ia + 2] ),
					Point( verts[3 * ( size_t )ib], verts[3 * ( size_t )ib + 1], verts[3 * ( size_t )ib + 2] ),
					Point( verts[3 * ( size_t )ic], verts[3 * ( size_t )ic + 1], verts[3 * ( size_t )ic + 2] ) );
			}
			// The tree stores iterators into m_triangles, so the vector must be
			// final before this point and must not be reallocated afterwards.
			m_tree.reset( new Tree( m_triangles.cbegin(), m_triangles.cend() ) );
			m_tree->build();
			m_tree->accelerate_distance_queries();
			return true;
		}

		size_t closest( const float q[3], float out_point[3] ) const
		{
			const auto	found = m_tree->closest_point_and_primitive( Point( q[0], q[1], q[2] ) );
			const Point p	  = found.first;
			out_point[0]	  = ( float )CGAL::to_double( p.x() );
			out_point[1]	  = ( float )CGAL::to_double( p.y() );
			out_point[2]	  = ( float )CGAL::to_double( p.z() );
			return ( size_t )std::distance( m_triangles.cbegin(), found.second );
		}

	private:
		std::vector<Triangle> m_triangles;
		std::unique_ptr<Tree> m_tree;
	};

#endif

	// Validate the inputs and compact away triangles neither backend may index.
	bool prepare_faces( const std::vector<float>& source_verts,
		const std::vector<int32_t>&				  source_tris,
		const std::vector<float>&				  source_pbr,
		const std::vector<float>&				  query_points,
		std::vector<int32_t>&					  faces,
		std::string&							  err )
	{
		const size_t source_nv = source_verts.size() / 3;
		if( source_verts.size() < 9 || source_verts.size() % 3 != 0 || source_tris.size() < 3 || source_tris.size() % 3 != 0 ) {
			err = "empty PBR projection source";
			return false;
		}
		if( source_pbr.size() != source_nv * 6 ) {
			err = "PBR projection source has no six-channel material";
			return false;
		}
		if( query_points.size() % 3 != 0 ) {
			err = "PBR projection query array is not xyz-aligned";
			return false;
		}

		faces.clear();
		faces.reserve( source_tris.size() );
		for( size_t t = 0; t < source_tris.size() / 3; ++t ) {
			const int32_t ia = source_tris[3 * t], ib = source_tris[3 * t + 1], ic = source_tris[3 * t + 2];
			if( ia < 0 || ib < 0 || ic < 0 || ( size_t )ia >= source_nv || ( size_t )ib >= source_nv || ( size_t )ic >= source_nv ) {
				err = "PBR projection triangle index out of range";
				return false;
			}
			if( degenerate_triangle( source_verts, ia, ib, ic ) )
				continue;
			faces.push_back( ia );
			faces.push_back( ib );
			faces.push_back( ic );
		}
		if( faces.empty() ) {
			err = "PBR projection source has no non-degenerate triangles";
			return false;
		}
		return true;
	}

	// Sampling loop shared by both backends: only `surface.closest` differs, so
	// a disagreement between backends is a disagreement about the search and
	// nothing else. Templated rather than std::function so the per-query call
	// stays direct.
	template<typename Surface>
	bool project_with( const Surface& surface,
		const std::vector<float>&	  source_verts,
		const std::vector<int32_t>&	  faces,
		const std::vector<float>&	  source_pbr,
		const std::vector<float>&	  query_points,
		std::vector<float>&			  out_pbr,
		std::string&				  err )
	{
		const size_t nq = query_points.size() / 3;
		out_pbr.resize( nq * 6 );

		std::atomic<bool> overflowed { false };

		auto			  sample_one = [&]( size_t qi ) {
			 const float  q[3] = { query_points[3 * qi], query_points[3 * qi + 1], query_points[3 * qi + 2] };
			 float		  hit[3];
			 const size_t ti = surface.closest( q, hit );
			 if( ti == ( size_t )-1 ) {
				 overflowed.store( true );
				 return;
			 }
			 const int32_t* ids = faces.data() + ti * 3;
			 const float*	a	= source_verts.data() + ( size_t )ids[0] * 3;
			 const float*	b	= source_verts.data() + ( size_t )ids[1] * 3;
			 const float*	c	= source_verts.data() + ( size_t )ids[2] * 3;

			 const double	ab[3] = { ( double )b[0] - a[0], ( double )b[1] - a[1], ( double )b[2] - a[2] };
			 const double	ac[3] = { ( double )c[0] - a[0], ( double )c[1] - a[1], ( double )c[2] - a[2] };
			 const double	aq[3] = { ( double )hit[0] - a[0], ( double )hit[1] - a[1], ( double )hit[2] - a[2] };
			 const double	d00	  = ab[0] * ab[0] + ab[1] * ab[1] + ab[2] * ab[2];
			 const double	d01	  = ab[0] * ac[0] + ab[1] * ac[1] + ab[2] * ac[2];
			 const double	d11	  = ac[0] * ac[0] + ac[1] * ac[1] + ac[2] * ac[2];
			 const double	d20	  = aq[0] * ab[0] + aq[1] * ab[1] + aq[2] * ab[2];
			 const double	d21	  = aq[0] * ac[0] + aq[1] * ac[1] + aq[2] * ac[2];
			 const double	denom = d00 * d11 - d01 * d01;
			 double			wb	  = ( d11 * d20 - d01 * d21 ) / denom;
			 double			wc	  = ( d00 * d21 - d01 * d20 ) / denom;
			 double			wa	  = 1.0 - wb - wc;
			 // The closest point is on the triangle. Clamp only numerical noise
			 // so interpolation remains stable on edges and vertices.
			 wa				  = std::max( 0.0, std::min( 1.0, wa ) );
			 wb				  = std::max( 0.0, std::min( 1.0, wb ) );
			 wc				  = std::max( 0.0, std::min( 1.0, wc ) );
			 const double sum = wa + wb + wc;
			 wa /= sum;
			 wb /= sum;
			 wc /= sum;
			 for( int ch = 0; ch < 6; ++ch ) {
				 out_pbr[6 * qi + ( size_t )ch] =
					 ( float )( wa * source_pbr[6 * ( size_t )ids[0] + ( size_t )ch] + wb * source_pbr[6 * ( size_t )ids[1] + ( size_t )ch] + wc * source_pbr[6 * ( size_t )ids[2] + ( size_t )ch] );
			 }
		};

		// The surface is fully built before any worker starts, so tinybvh's own
		// build threading never overlaps this pool.
		const unsigned		hw		= std::max( 1u, std::thread::hardware_concurrency() );
		const unsigned		workers = ( unsigned )std::min<size_t>( std::min( 16u, hw ), ( nq + 4095 ) / 4096 );
		std::atomic<size_t> next { 0 };
		std::atomic<bool>	failed { false };
		std::mutex			failure_mu;
		std::string			failure;
		auto				worker = [&]() {
			   try {
				   for( ;; ) {
					   const size_t begin = next.fetch_add( 4096 );
					   if( begin >= nq || failed.load() )
						   break;
					   const size_t end = std::min( nq, begin + 4096 );
					   for( size_t qi = begin; qi < end; ++qi )
						   sample_one( qi );
				   }
			   } catch( const std::exception& ex ) {
				   failed.store( true );
				   std::lock_guard<std::mutex> lock( failure_mu );
				   if( failure.empty() )
					   failure = ex.what();
			   } catch( ... ) {
				   failed.store( true );
			   }
		};

		if( workers <= 1 ) {
			for( size_t qi = 0; qi < nq; ++qi )
				sample_one( qi );
		} else {
			std::vector<std::thread> threads;
			threads.reserve( workers );
			for( unsigned i = 0; i < workers; ++i )
				threads.emplace_back( worker );
			for( auto& thread : threads )
				thread.join();
		}

		if( failed.load() ) {
			err = failure.empty() ? "PBR projection failed" : std::string( "PBR projection failed: " ) + failure;
			out_pbr.clear();
			return false;
		}
		if( overflowed.load() ) {
			err = "PBR projection exceeded the BVH traversal stack";
			out_pbr.clear();
			return false;
		}
		return true;
	}

} // namespace

bool project_pbr_backend( const char* backend,
	const std::vector<float>&		  source_verts,
	const std::vector<int32_t>&		  source_tris,
	const std::vector<float>&		  source_pbr,
	const std::vector<float>&		  query_points,
	std::vector<float>&				  out_pbr,
	std::string&					  err )
{
	out_pbr.clear();
	if( !backend )
		backend = projection_backend();

	const bool want_cgal = 0 == std::strcmp( backend, "cgal" );
	if( !want_cgal && 0 != std::strcmp( backend, "tinybvh" ) ) {
		err = std::string( "unknown PBR projection backend: " ) + backend;
		return false;
	}
#ifndef TRELLIS2_USE_CGAL
	if( want_cgal ) {
		err = "the CGAL PBR projection backend is unavailable (rebuild with CGAL >= 5.5)";
		return false;
	}
#endif

	std::vector<int32_t> faces;
	if( !prepare_faces( source_verts, source_tris, source_pbr, query_points, faces, err ) )
		return false;
	if( query_points.empty() )
		return true;

	try {
#ifdef TRELLIS2_USE_CGAL
		if( want_cgal ) {
			CgalSurface surface;
			if( !surface.build( source_verts, faces, err ) )
				return false;
			return project_with( surface, source_verts, faces, source_pbr, query_points, out_pbr, err );
		}
#endif
		TinyBvhSurface surface;
		if( !surface.build( source_verts, faces, err ) )
			return false;
		return project_with( surface, source_verts, faces, source_pbr, query_points, out_pbr, err );
	} catch( const std::exception& ex ) {
		err = std::string( "PBR projection failed: " ) + ex.what();
		out_pbr.clear();
		return false;
	} catch( ... ) {
		err = "PBR projection failed";
		out_pbr.clear();
		return false;
	}
}

bool project_pbr( const std::vector<float>& source_verts,
	const std::vector<int32_t>&				source_tris,
	const std::vector<float>&				source_pbr,
	const std::vector<float>&				query_points,
	std::vector<float>&						out_pbr,
	std::string&							err )
{
	return project_pbr_backend( projection_backend(), source_verts, source_tris, source_pbr, query_points, out_pbr, err );
}

} // namespace t2print
