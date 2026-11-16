#include "quad_remesh.h"

#include <algorithm>
#include <array>
#include <cmath>
#include <cstdio>
#include <functional>
#include <cstddef>
#include <exception>
#include <map>
#include <unordered_map>
#include <utility>
#include <vector>

#ifdef TRELLIS2_USE_AUTOREMESHER
	#include <AutoRemesher/AutoRemesher>
	#include <AutoRemesher/Vector3>

	#include "meshoptimizer.h"
#endif

namespace t2quad
{

bool available()
{
#ifdef TRELLIS2_USE_AUTOREMESHER
	return true;
#else
	return false;
#endif
}

namespace
{

	double triangle_area( const float* a, const float* b, const float* c )
	{
		const double ab[3] = { ( double )b[0] - a[0], ( double )b[1] - a[1], ( double )b[2] - a[2] };
		const double ac[3] = { ( double )c[0] - a[0], ( double )c[1] - a[1], ( double )c[2] - a[2] };
		const double n[3]  = { ab[1] * ac[2] - ab[2] * ac[1], ab[2] * ac[0] - ab[0] * ac[2], ab[0] * ac[1] - ab[1] * ac[0] };
		return 0.5 * std::sqrt( n[0] * n[0] + n[1] * n[1] + n[2] * n[2] );
	}

	uint64_t edge_key( int32_t a, int32_t b )
	{
		const uint32_t lo = ( uint32_t )( a < b ? a : b );
		const uint32_t hi = ( uint32_t )( a < b ? b : a );
		return ( ( uint64_t )lo << 32 ) | hi;
	}

	// Drop zero-area triangles and exact duplicates. Duplicates matter beyond
	// wasted work: two copies of a face make all three of its edges look
	// non-manifold, which would send the splitter below on a pointless
	// rampage.
	void drop_degenerate_and_duplicate( const std::vector<float>& verts, const std::vector<int32_t>& tris, std::vector<int32_t>& out, int& dropped )
	{
		out.clear();
		out.reserve( tris.size() );
		dropped = 0;

		std::map<std::array<int32_t, 3>, char> seen;
		for( size_t t = 0; t + 2 < tris.size(); t += 3 ) {
			const int32_t ia = tris[t], ib = tris[t + 1], ic = tris[t + 2];
			if( ia == ib || ib == ic || ia == ic ) {
				++dropped;
				continue;
			}
			if( triangle_area( verts.data() + ( size_t )ia * 3, verts.data() + ( size_t )ib * 3, verts.data() + ( size_t )ic * 3 ) <= 0.0 ) {
				++dropped;
				continue;
			}
			// Orientation-insensitive identity: a face and its flipped twin are
			// still the same surface for connectivity purposes.
			std::array<int32_t, 3> key = { { ia, ib, ic } };
			std::sort( key.begin(), key.end() );
			if( !seen.insert( { key, 1 } ).second ) {
				++dropped;
				continue;
			}
			out.push_back( ia );
			out.push_back( ib );
			out.push_back( ic );
		}
	}

	// Break non-manifold junctions apart by duplicating vertices.
	//
	// Two faces are treated as connected at a vertex only when they share a
	// *manifold* edge there, i.e. one used by exactly two faces overall. Each
	// connected group of faces around a vertex then gets its own copy of it.
	// That splits both the bow-tie case (faces meeting only at a point) and
	// edges shared by three or more faces, which is exactly what the halfedge
	// mesh inside AutoRemesher cannot represent.
	//
	// The parts stay geometrically coincident; they merely stop being one
	// island. AutoRemesher handles disjoint islands well - it is the junction
	// it cannot handle.
	void split_non_manifold( const std::vector<float>& verts, const std::vector<int32_t>& tris, std::vector<float>& out_verts, std::vector<int32_t>& out_tris, int& split_count )
	{
		const size_t									   nv = verts.size() / 3;
		const size_t									   nf = tris.size() / 3;

		// Edge -> incident faces, and vertex -> incident faces.
		std::unordered_map<uint64_t, std::vector<int32_t>> edge_faces;
		edge_faces.reserve( nf * 3 );
		std::vector<std::vector<int32_t>> vertex_faces( nv );
		for( size_t f = 0; f < nf; ++f ) {
			const int32_t* tri = tris.data() + f * 3;
			for( int k = 0; k < 3; ++k ) {
				edge_faces[edge_key( tri[k], tri[( k + 1 ) % 3] )].push_back( ( int32_t )f );
				vertex_faces[( size_t )tri[k]].push_back( ( int32_t )f );
			}
		}

		out_verts	= verts;
		out_tris	= tris;
		split_count = 0;

		// Per face corner, which output vertex it ends up using.
		for( size_t v = 0; v < nv; ++v ) {
			const std::vector<int32_t>& fan = vertex_faces[v];
			if( fan.size() < 2 )
				continue;

			// Union-find over the fan, joined only through manifold edges that
			// actually contain v.
			std::unordered_map<int32_t, int32_t> index_of;
			index_of.reserve( fan.size() * 2 );
			for( size_t i = 0; i < fan.size(); ++i )
				index_of[fan[i]] = ( int32_t )i;

			std::vector<int32_t> parent( fan.size() );
			for( size_t i = 0; i < parent.size(); ++i )
				parent[i] = ( int32_t )i;
			std::function<int32_t( int32_t )> find = [&]( int32_t x ) {
				while( parent[x] != x ) {
					parent[x] = parent[parent[x]];
					x		  = parent[x];
				}
				return x;
			};

			for( const int32_t f : fan ) {
				const int32_t* tri = tris.data() + ( size_t )f * 3;
				for( int k = 0; k < 3; ++k ) {
					const int32_t a = tri[k], b = tri[( k + 1 ) % 3];
					if( a != ( int32_t )v && b != ( int32_t )v )
						continue;
					const std::vector<int32_t>& users = edge_faces[edge_key( a, b )];
					if( users.size() != 2 )
						continue; // non-manifold or boundary edge: no join
					const int32_t other = users[0] == f ? users[1] : users[0];
					auto		  it	= index_of.find( other );
					if( it == index_of.end() )
						continue;
					const int32_t ra = find( index_of[f] ), rb = find( it->second );
					if( ra != rb )
						parent[ra] = rb;
				}
			}

			// One component means the fan is fine; more means a junction.
			std::unordered_map<int32_t, int32_t> component_vertex;
			for( const int32_t f : fan ) {
				const int32_t root = find( index_of[f] );
				auto		  it   = component_vertex.find( root );
				int32_t		  use  = -1;
				if( it == component_vertex.end() ) {
					if( component_vertex.empty() ) {
						use = ( int32_t )v; // first component keeps the original
					} else {
						use = ( int32_t )( out_verts.size() / 3 );
						out_verts.push_back( verts[v * 3 + 0] );
						out_verts.push_back( verts[v * 3 + 1] );
						out_verts.push_back( verts[v * 3 + 2] );
						++split_count;
					}
					component_vertex[root] = use;
				} else {
					use = it->second;
				}
				int32_t* tri = out_tris.data() + ( size_t )f * 3;
				for( int k = 0; k < 3; ++k ) {
					if( tris[( size_t )f * 3 + k] == ( int32_t )v )
						tri[k] = use;
				}
			}
		}
	}

	int count_boundary_edges( const std::vector<int32_t>& faces, const std::vector<int32_t>& face_sizes )
	{
		std::unordered_map<uint64_t, int> counts;
		size_t							  offset = 0;
		for( const int32_t n : face_sizes ) {
			for( int32_t k = 0; k < n; ++k ) {
				const int32_t a = faces[offset + ( size_t )k];
				const int32_t b = faces[offset + ( size_t )( ( k + 1 ) % n )];
				counts[edge_key( a, b )] += 1;
			}
			offset += ( size_t )n;
		}
		int open = 0;
		for( const auto& it : counts ) {
			if( it.second == 1 )
				++open;
		}
		return open;
	}

} // namespace

void triangulate( const std::vector<float>& verts, const std::vector<int32_t>& faces, const std::vector<int32_t>& face_sizes, std::vector<int32_t>& out_tris )
{
	out_tris.clear();
	size_t offset = 0;
	for( const int32_t n : face_sizes ) {
		if( n < 3 ) {
			offset += ( size_t )( n > 0 ? n : 0 );
			continue;
		}
		const int32_t* f = faces.data() + offset;
		if( n == 4 ) {
			// Split along the shorter diagonal: on the stretched quads that
			// anisotropic remeshing produces, the long diagonal would put a
			// visible crease across the face.
			const float* p0	 = verts.data() + ( size_t )f[0] * 3;
			const float* p1	 = verts.data() + ( size_t )f[1] * 3;
			const float* p2	 = verts.data() + ( size_t )f[2] * 3;
			const float* p3	 = verts.data() + ( size_t )f[3] * 3;
			const double d02 = ( double )( p0[0] - p2[0] ) * ( p0[0] - p2[0] ) + ( double )( p0[1] - p2[1] ) * ( p0[1] - p2[1] ) + ( double )( p0[2] - p2[2] ) * ( p0[2] - p2[2] );
			const double d13 = ( double )( p1[0] - p3[0] ) * ( p1[0] - p3[0] ) + ( double )( p1[1] - p3[1] ) * ( p1[1] - p3[1] ) + ( double )( p1[2] - p3[2] ) * ( p1[2] - p3[2] );
			if( d02 <= d13 ) {
				out_tris.insert( out_tris.end(), { f[0], f[1], f[2], f[0], f[2], f[3] } );
			} else {
				out_tris.insert( out_tris.end(), { f[1], f[2], f[3], f[1], f[3], f[0] } );
			}
		} else {
			for( int32_t k = 2; k < n; ++k ) {
				out_tris.push_back( f[0] );
				out_tris.push_back( f[k - 1] );
				out_tris.push_back( f[k] );
			}
		}
		offset += ( size_t )n;
	}
}

bool remesh( const std::vector<float>& verts,
	const std::vector<int32_t>&		   tris,
	const QuadRemeshOptions&		   opt,
	std::vector<float>&				   out_verts,
	std::vector<int32_t>&			   out_faces,
	std::vector<int32_t>&			   out_face_sizes,
	QuadRemeshStats&				   stats,
	std::string&					   err,
	ProgressFn						   progress,
	void*							   progress_user )
{
	out_verts.clear();
	out_faces.clear();
	out_face_sizes.clear();
	stats = QuadRemeshStats();

#ifndef TRELLIS2_USE_AUTOREMESHER
	( void )verts;
	( void )tris;
	( void )opt;
	( void )progress;
	( void )progress_user;
	err = "quad remeshing is unavailable (rebuild with Eigen 5.x so the AutoRemesher backend is compiled in)";
	return false;
#else
	const size_t nv = verts.size() / 3;
	if( verts.size() < 9 || verts.size() % 3 != 0 || tris.size() < 3 || tris.size() % 3 != 0 ) {
		err = "empty quad remesh source";
		return false;
	}
	for( const float c : verts ) {
		if( !std::isfinite( c ) ) {
			err = "quad remesh source contains a non-finite vertex";
			return false;
		}
	}
	for( const int32_t i : tris ) {
		if( i < 0 || ( size_t )i >= nv ) {
			err = "quad remesh triangle index out of range";
			return false;
		}
	}
	if( opt.target_quads <= 0 ) {
		err = "target_quads must be positive";
		return false;
	}

	try {
		// --- input preparation -------------------------------------------
		std::vector<int32_t> clean;
		drop_degenerate_and_duplicate( verts, tris, clean, stats.faces_dropped );
		if( clean.empty() ) {
			err = "quad remesh source has no non-degenerate triangles";
			return false;
		}

		std::vector<float>	 prep_verts = verts;
		std::vector<int32_t> prep_tris	= clean;

		if( opt.input_triangle_budget > 0 && ( int )( prep_tris.size() / 3 ) > opt.input_triangle_budget ) {
			std::vector<unsigned int> src( prep_tris.begin(), prep_tris.end() );
			std::vector<unsigned int> dst( src.size() );
			const size_t			  target = ( size_t )opt.input_triangle_budget * 3;
			const size_t			  kept	 = meshopt_simplify( dst.data(), src.data(), src.size(), prep_verts.data(), prep_verts.size() / 3, 3 * sizeof( float ), target, 1e-2f, 0, nullptr );
			if( kept >= 3 ) {
				dst.resize( kept );
				prep_tris.assign( dst.begin(), dst.end() );
			}
		}

		if( opt.split_non_manifold ) {
			std::vector<float>	 split_verts;
			std::vector<int32_t> split_tris;
			split_non_manifold( prep_verts, prep_tris, split_verts, split_tris, stats.vertices_split );
			prep_verts.swap( split_verts );
			prep_tris.swap( split_tris );
		}
		stats.input_tris_after_prep = ( int )( prep_tris.size() / 3 );

		double input_area = 0.0;
		for( size_t t = 0; t + 2 < prep_tris.size(); t += 3 ) {
			input_area += triangle_area( prep_verts.data() + ( size_t )prep_tris[t] * 3, prep_verts.data() + ( size_t )prep_tris[t + 1] * 3, prep_verts.data() + ( size_t )prep_tris[t + 2] * 3 );
		}

		// --- remesh -------------------------------------------------------
		std::vector<AutoRemesher::Vector3> ar_verts;
		std::vector<std::vector<size_t>>   ar_tris;
		ar_verts.reserve( prep_verts.size() / 3 );
		for( size_t i = 0; i < prep_verts.size() / 3; ++i )
			ar_verts.push_back( AutoRemesher::Vector3( prep_verts[3 * i], prep_verts[3 * i + 1], prep_verts[3 * i + 2] ) );
		ar_tris.reserve( prep_tris.size() / 3 );
		for( size_t t = 0; t + 2 < prep_tris.size(); t += 3 )
			ar_tris.push_back( { ( size_t )prep_tris[t], ( size_t )prep_tris[t + 1], ( size_t )prep_tris[t + 2] } );

		AutoRemesher::AutoRemesher remesher( ar_verts, ar_tris );
		// Upstream maps target quads onto twice as many triangles.
		remesher.setTargetTriangleCount( ( size_t )opt.target_quads * 2 );
		if( opt.edge_scaling > 0.0f )
			remesher.setScaling( opt.edge_scaling );
		remesher.setModelType( opt.hard_surface ? AutoRemesher::ModelType::HardSurface : AutoRemesher::ModelType::Organic );
		remesher.setGradientAdaptivity( opt.adaptivity );
		remesher.setAnisotropy( opt.anisotropy );
		remesher.setSharpEdgeDegrees( opt.sharp_edge_deg );
		remesher.setSmoothNormalDegrees( opt.smooth_normal_deg );
		if( progress ) {
			remesher.setTag( progress_user );
			remesher.setProgressHandler( progress );
		}

		if( !remesher.remesh() ) {
			err = "quad remeshing failed";
			return false;
		}

		const std::vector<AutoRemesher::Vector3>& rv = remesher.remeshedVertices();
		const std::vector<std::vector<size_t>>&	  rq = remesher.remeshedQuads();
		if( rq.empty() || rv.empty() ) {
			err = "quad remeshing produced no faces";
			return false;
		}

		out_verts.reserve( rv.size() * 3 );
		for( const auto& p : rv ) {
			out_verts.push_back( ( float )p.x() );
			out_verts.push_back( ( float )p.y() );
			out_verts.push_back( ( float )p.z() );
		}
		out_face_sizes.reserve( rq.size() );
		for( const auto& face : rq ) {
			if( face.size() < 3 )
				continue;
			for( const size_t index : face ) {
				if( index >= rv.size() ) {
					err = "quad remeshing returned an out-of-range index";
					out_verts.clear();
					out_faces.clear();
					out_face_sizes.clear();
					return false;
				}
				out_faces.push_back( ( int32_t )index );
			}
			out_face_sizes.push_back( ( int32_t )face.size() );
			if( face.size() == 4 )
				++stats.quads;
			else if( face.size() == 3 )
				++stats.triangles;
			else
				++stats.ngons;
		}
		if( out_face_sizes.empty() ) {
			err = "quad remeshing produced no usable faces";
			return false;
		}

		// --- quality ------------------------------------------------------
		std::vector<int32_t> as_tris;
		triangulate( out_verts, out_faces, out_face_sizes, as_tris );
		double output_area = 0.0;
		for( size_t t = 0; t + 2 < as_tris.size(); t += 3 ) {
			output_area += triangle_area( out_verts.data() + ( size_t )as_tris[t] * 3, out_verts.data() + ( size_t )as_tris[t + 1] * 3, out_verts.data() + ( size_t )as_tris[t + 2] * 3 );
		}
		stats.area_retained	 = input_area > 0.0 ? ( float )( output_area / input_area ) : 0.0f;
		stats.boundary_edges = count_boundary_edges( out_faces, out_face_sizes );

		// AutoRemesher reports success even when it silently skipped islands
		// whose parameterization threw, so a large area loss is the only
		// signal the caller ever gets. Refuse rather than hand back a
		// half-remeshed model that looks like a result.
		if( opt.min_area_retained > 0.0f && stats.area_retained < opt.min_area_retained ) {
			char buf[192];
			std::snprintf( buf,
				sizeof( buf ),
				"quad remeshing lost too much of the surface (%.1f%% retained, minimum %.1f%%); islands were most likely dropped",
				100.0 * stats.area_retained,
				100.0 * opt.min_area_retained );
			err = buf;
			out_verts.clear();
			out_faces.clear();
			out_face_sizes.clear();
			return false;
		}
		return true;
	} catch( const std::exception& ex ) {
		err = std::string( "quad remeshing failed: " ) + ex.what();
		out_verts.clear();
		out_faces.clear();
		out_face_sizes.clear();
		return false;
	} catch( ... ) {
		err = "quad remeshing failed";
		out_verts.clear();
		out_faces.clear();
		out_face_sizes.clear();
		return false;
	}
#endif
}

} // namespace t2quad
