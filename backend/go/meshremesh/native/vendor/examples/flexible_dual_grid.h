// flexible_dual_grid.h — single-header CPU port of TRELLIS.2's flexible dual
// grid mesh extraction (o-voxel/o_voxel/convert/flexible_dual_grid.py, eval
// path). Turns the shape decoder's per-voxel 7-channel output into a triangle
// mesh, replacing the CUDA hashmap kernel with an std::unordered_map.
//
// Per active voxel v at integer coord c (in [0, grid_size)):
//   dual vertex   V_v = (c + offset_v) * voxel_size + aabb0   (unit cube here)
//   offset_v      = (1 + 2*margin) * sigmoid(feat[0:3]) - margin
//   intersected   feat[3:6] > 0, one flag per axis (x, y, z)
//   split_weight  softplus(feat[6])
// For each voxel with an intersected axis, the 4 voxels around that edge
// (offsets below) contribute their dual vertices as a quad; if all 4 exist the
// quad is split into 2 triangles along the diagonal chosen by the decoder's
// learned split_weight = softplus(feat[6]) (reference eval-path tie-break).
#pragma once

#include <algorithm>
#include <cmath>
#include <cstdint>
#include <unordered_map>
#include <unordered_set>
#include <utility>
#include <vector>

namespace fdg
{

struct Mesh {
	std::vector<float> verts; // 3 per vertex
	std::vector<int>   tris;  // 3 indices per triangle
	size_t			   n_verts() const { return verts.size() / 3; }
	size_t			   n_tris() const { return tris.size() / 3; }
};

namespace detail
{

	inline uint64_t key( int32_t x, int32_t y, int32_t z )
	{
		return ( ( uint64_t )( uint32_t )x << 40 ) | ( ( uint64_t )( uint32_t )y << 20 ) | ( uint64_t )( uint32_t )z;
	}

	// The 4 neighbor-voxel offsets around an edge, per axis (matches
	// edge_neighbor_voxel_offset in the reference).
	static const int EDGE_OFF[3][4][3] = {
		{ { 0, 0, 0 }, { 0, 0, 1 }, { 0, 1, 1 }, { 0, 1, 0 } }, // x-axis edge
		{ { 0, 0, 0 }, { 1, 0, 0 }, { 1, 0, 1 }, { 0, 0, 1 } }, // y-axis edge
		{ { 0, 0, 0 }, { 0, 1, 0 }, { 1, 1, 0 }, { 1, 0, 0 } }, // z-axis edge
	};

	inline void cross( const float* a, const float* b, float* o )
	{
		o[0] = a[1] * b[2] - a[2] * b[1];
		o[1] = a[2] * b[0] - a[0] * b[2];
		o[2] = a[0] * b[1] - a[1] * b[0];
	}

} // namespace detail

// feats: [n_voxels * 7], voxel-major (dec output). coords: [n_voxels * 3].
// grid_size = input_res * decoder upscale (e.g. 32 * 16 = 512). margin 0.5.
inline Mesh extract( const float* feats, const int32_t* coords, int n, int grid_size, float margin = 0.5f )
{
	using namespace detail;
	Mesh m;
	if( n <= 0 ) return m;

	const float						  vs	= 1.0f / ( float )grid_size; // voxel size (aabb span 1)
	const float						  aabb0 = -0.5f;

	// dual vertices + hashmap
	std::vector<float>				  V( ( size_t )n * 3 );
	std::unordered_map<uint64_t, int> idx;
	idx.reserve( ( size_t )n * 2 );
	for( int v = 0; v < n; ++v ) {
		const float* f = feats + ( size_t )v * 7;
		for( int a = 0; a < 3; ++a ) {
			const float s		   = 1.0f / ( 1.0f + std::exp( -f[a] ) ); // sigmoid
			const float off		   = ( 1.0f + 2.0f * margin ) * s - margin;
			V[( size_t )v * 3 + a] = ( ( float )coords[( size_t )v * 3 + a] + off ) * vs + aabb0;
		}
		idx[key( coords[( size_t )v * 3], coords[( size_t )v * 3 + 1], coords[( size_t )v * 3 + 2] )] = v;
	}
	m.verts = V;

	// quads from intersected edges
	for( int v = 0; v < n; ++v ) {
		const float*  f	 = feats + ( size_t )v * 7;
		const int32_t cx = coords[( size_t )v * 3];
		const int32_t cy = coords[( size_t )v * 3 + 1];
		const int32_t cz = coords[( size_t )v * 3 + 2];
		for( int axis = 0; axis < 3; ++axis ) {
			if( f[3 + axis] <= 0.0f ) continue; // not intersected on this axis
			int	 q[4];
			bool ok = true;
			for( int i = 0; i < 4; ++i ) {
				const int32_t nx = cx + EDGE_OFF[axis][i][0];
				const int32_t ny = cy + EDGE_OFF[axis][i][1];
				const int32_t nz = cz + EDGE_OFF[axis][i][2];
				auto		  it = idx.find( key( nx, ny, nz ) );
				if( it == idx.end() ) {
					ok = false;
					break;
				}
				q[i] = it->second;
			}
			if( !ok ) continue;

			// Choose the quad diagonal by the decoder's learned split_weight
			// (softplus of feat[6]), exactly as the reference eval path does
			// (FlexiDualGridVaeDecoder -> flexible_dual_grid_to_mesh, train=False):
			//   split 1: (0,1,2)+(0,2,3)   when sw0*sw2 >  sw1*sw3
			//   split 2: (0,1,3)+(3,1,2)   otherwise
			// (A geometric best-aligned-normals heuristic is the reference's
			// split_weight=None fallback; the shipped decoder always emits
			// feat[6], so we follow the learned choice.)
			auto sw = [&]( int i ) {
				const float x = feats[( size_t )q[i] * 7 + 6];
				return x > 20.0f ? x : std::log1p( std::exp( x ) ); // softplus
			};
			if( sw( 0 ) * sw( 2 ) > sw( 1 ) * sw( 3 ) ) {
				m.tris.push_back( q[0] );
				m.tris.push_back( q[1] );
				m.tris.push_back( q[2] );
				m.tris.push_back( q[0] );
				m.tris.push_back( q[2] );
				m.tris.push_back( q[3] );
			} else {
				m.tris.push_back( q[0] );
				m.tris.push_back( q[1] );
				m.tris.push_back( q[3] );
				m.tris.push_back( q[3] );
				m.tris.push_back( q[1] );
				m.tris.push_back( q[2] );
			}
		}
	}
	return m;
}

// Per-vertex shading normals for the dual grid's *unoriented*, heavily
// non-manifold mesh.
//
// The reference mesher emits every quad with a fixed vertex order regardless of
// which way the surface crosses the edge, so a large fraction of faces are wound
// opposite to their neighbours. Two failure modes follow: (a) a naive
// area-weighted normal cancels at those seams, and (b) any *sign* fix that leaves
// stray flips is not harmless — the normal is interpolated across the triangle
// *before* the fragment shader's abs(dot), so two adjacent vertices with opposite
// normals make the interpolated normal cross zero mid-face → normalize() explodes
// → speckled/blocky specular. The mesh is too non-manifold to 2-colour the
// winding cleanly (edges shared by >2 faces frustrate it), so we don't try to.
//
//   1. Recover a smooth, winding-INDEPENDENT normal *direction* per vertex as the
//      dominant eigenvector of the area-weighted structure tensor Σ area·n̂n̂ᵀ
//      (immune to winding sign since n̂n̂ᵀ == (−n̂)(−n̂)ᵀ).
//   2. Resolve the arbitrary per-vertex *sign* consistently with a parity
//      union-find over mesh edges, so edge-adjacent vertices share a hemisphere
//      and the interpolated normal stays clear of zero. Only genuinely frustrated
//      (odd-cycle / non-manifold) edges are left flipped.
// Shading here is orientation-independent (our viewer uses abs(dot)), so only
// local smoothness matters to it, not a globally correct outward sign. (Winding
// unification and sign diffusion were both tried as a way to *derive* these
// normals and are worse on this mesh: 2-colouring the >2-face non-manifold edges
// frustrates more, and Jacobi diffusion checkerboards.) The winding itself is
// repaired afterwards by orient_faces, which uses these normals as its reference
// and so needs no propagation at all — external consumers do read the winding.
inline std::vector<float> vertex_normals( const Mesh& m )
{
	const size_t		nv = m.n_verts();
	std::vector<double> A( ( size_t )nv * 6, 0.0 );		// sym structure tensor per vertex
	std::vector<float>	seed( ( size_t )nv * 3, 0.0f ); // signed area sum: a sign hint
	for( size_t t = 0; t < m.tris.size(); t += 3 ) {
		const int	 i0 = m.tris[t], i1 = m.tris[t + 1], i2 = m.tris[t + 2];
		const float* a = &m.verts[( size_t )i0 * 3];
		const float* b = &m.verts[( size_t )i1 * 3];
		const float* c = &m.verts[( size_t )i2 * 3];
		float		 e1[3], e2[3], fn[3];
		for( int k = 0; k < 3; ++k ) {
			e1[k] = b[k] - a[k];
			e2[k] = c[k] - a[k];
		}
		detail::cross( e1, e2, fn );
		const double area = std::sqrt( ( double )fn[0] * fn[0] + ( double )fn[1] * fn[1] + ( double )fn[2] * fn[2] );
		if( area <= 1e-20 ) continue;
		const double inv = 1.0 / area;
		const double xx = fn[0] * fn[0] * inv, yy = fn[1] * fn[1] * inv, zz = fn[2] * fn[2] * inv;
		const double xy = fn[0] * fn[1] * inv, xz = fn[0] * fn[2] * inv, yz = fn[1] * fn[2] * inv;
		for( int i : { i0, i1, i2 } ) {
			double* Av = &A[( size_t )i * 6];
			Av[0] += xx;
			Av[1] += yy;
			Av[2] += zz;
			Av[3] += xy;
			Av[4] += xz;
			Av[5] += yz;
			float* sv = &seed[( size_t )i * 3];
			sv[0] += fn[0];
			sv[1] += fn[1];
			sv[2] += fn[2];
		}
	}
	// (1) smooth, sign-ambiguous direction per vertex
	std::vector<float> dir( ( size_t )nv * 3, 0.0f );
	for( size_t v = 0; v < nv; ++v ) {
		const double* Av = &A[v * 6];
		double		  x = seed[v * 3], y = seed[v * 3 + 1], z = seed[v * 3 + 2];
		double		  l = std::sqrt( x * x + y * y + z * z );
		if( l < 1e-20 ) {
			x = Av[0];
			y = Av[3];
			z = Av[4];
			l = std::sqrt( x * x + y * y + z * z );
		}
		if( l < 1e-20 ) {
			dir[v * 3 + 2] = 1.0f;
			continue;
		}
		x /= l;
		y /= l;
		z /= l;
		for( int it = 0; it < 8; ++it ) {
			const double nx = Av[0] * x + Av[3] * y + Av[4] * z;
			const double ny = Av[3] * x + Av[1] * y + Av[5] * z;
			const double nz = Av[4] * x + Av[5] * y + Av[2] * z;
			const double nl = std::sqrt( nx * nx + ny * ny + nz * nz );
			if( nl < 1e-20 ) break;
			x = nx / nl;
			y = ny / nl;
			z = nz / nl;
		}
		dir[v * 3]	   = ( float )x;
		dir[v * 3 + 1] = ( float )y;
		dir[v * 3 + 2] = ( float )z;
	}
	// (2) Resolve the arbitrary per-vertex sign *consistently* via a parity
	//     union-find over mesh edges: two edge-adjacent vertices whose directions
	//     are anti-aligned must end up with opposite signs (and vice versa), so
	//     within each connected component neighbours share a hemisphere. Only
	//     genuinely frustrated (odd-cycle / non-manifold) edges are left flipped.
	std::vector<int>	 ufp( nv ), ufr( nv, 0 );
	std::vector<uint8_t> ufb( nv, 0 ); // parity of a vertex relative to its parent
	for( size_t i = 0; i < nv; ++i )
		ufp[i] = ( int )i;
	auto find = [&]( int v, int& parity ) {
		int p = 0;
		while( ufp[v] != v ) {
			p ^= ufb[v];
			v = ufp[v];
		}
		parity = p;
		return v;
	};
	auto join = [&]( int a, int b ) {
		const float* na	 = &dir[( size_t )a * 3];
		const float* nb	 = &dir[( size_t )b * 3];
		const int	 rel = ( na[0] * nb[0] + na[1] * nb[1] + na[2] * nb[2] ) < 0.0f ? 1 : 0;
		int			 pa, pb, ra = find( a, pa ), rb = find( b, pb );
		if( ra == rb ) return;
		if( ufr[ra] < ufr[rb] ) {
			std::swap( ra, rb );
			std::swap( pa, pb );
		}
		ufp[rb] = ra;
		ufb[rb] = ( uint8_t )( pa ^ pb ^ rel );
		if( ufr[ra] == ufr[rb] ) ufr[ra]++;
	};
	for( size_t t = 0; t < m.tris.size(); t += 3 ) {
		const int a = m.tris[t], b = m.tris[t + 1], c = m.tris[t + 2];
		join( a, b );
		join( b, c );
		join( c, a );
	}
	// (3) pick each component's global sign toward the signed-sum seed (so the
	//     result is deterministic and roughly outward), then emit oriented normals
	std::unordered_map<int, double> comp_sign;
	for( size_t v = 0; v < nv; ++v ) {
		int			 p, r = find( ( int )v, p );
		const double s	= p ? -1.0 : 1.0;
		const float* sv = &seed[v * 3];
		comp_sign[r] += s * ( dir[v * 3] * sv[0] + dir[v * 3 + 1] * sv[1] + dir[v * 3 + 2] * sv[2] );
	}
	std::vector<float> nrm( ( size_t )nv * 3, 0.0f );
	for( size_t v = 0; v < nv; ++v ) {
		int	   p, r = find( ( int )v, p );
		double s = p ? -1.0 : 1.0;
		if( comp_sign[r] < 0.0 ) s = -s;
		nrm[v * 3]	   = ( float )( s * dir[v * 3] );
		nrm[v * 3 + 1] = ( float )( s * dir[v * 3 + 1] );
		nrm[v * 3 + 2] = ( float )( s * dir[v * 3 + 2] );
	}
	return nrm;
}

// Remove triangles that belong to tiny disconnected islands (the floating
// specks that read as "blemishes"), keeping the vertex array and its indexing
// intact so a parallel per-vertex attribute array (e.g. baked PBR) stays
// aligned. Components are face groups connected through shared vertices; a
// component is dropped when its face count is below min_frac of the total.
inline void drop_small_components( Mesh& m, float min_frac = 0.0005f )
{
	const size_t nt = m.n_tris();
	if( nt == 0 ) return;
	const size_t	 nv = m.n_verts();
	std::vector<int> p( nv );
	for( size_t i = 0; i < nv; ++i )
		p[i] = ( int )i;
	auto find = [&]( int x ) {
		while( p[x] != x ) {
			p[x] = p[p[x]];
			x	 = p[x];
		}
		return x;
	};
	auto uni = [&]( int a, int b ) {
		a = find( a );
		b = find( b );
		if( a != b ) p[a] = b;
	};
	for( size_t t = 0; t < m.tris.size(); t += 3 ) {
		uni( m.tris[t], m.tris[t + 1] );
		uni( m.tris[t + 1], m.tris[t + 2] );
	}
	std::unordered_map<int, int> faces;
	for( size_t t = 0; t < m.tris.size(); t += 3 )
		faces[find( m.tris[t] )]++;
	const int		 min_faces = std::max( 1, ( int )( min_frac * ( double )nt ) );
	std::vector<int> keep;
	keep.reserve( m.tris.size() );
	for( size_t t = 0; t < m.tris.size(); t += 3 ) {
		if( faces[find( m.tris[t] )] >= min_faces ) {
			keep.push_back( m.tris[t] );
			keep.push_back( m.tris[t + 1] );
			keep.push_back( m.tris[t + 2] );
		}
	}
	m.tris.swap( keep );
}

// Fill the small holes extract() leaves where a dual-grid quad was skipped (a
// neighbour voxel was missing). A boundary edge is one used by exactly one
// triangle; boundary edges chain into loops around each hole. Each loop up to
// `max_loop` vertices is fan-triangulated from its first vertex. Only triangles
// are added — no new vertices — so a parallel per-vertex attribute array (baked
// PBR) stays aligned. Large loops (genuine openings) are left unfilled.
inline void fill_holes( Mesh& m, int max_loop = 64, int max_passes = 4 )
{
	if( m.n_tris() == 0 ) return;
	auto key = []( int a, int b ) {
		const uint32_t lo = a < b ? a : b, hi = a < b ? b : a;
		return ( ( uint64_t )lo << 32 ) | hi;
	};
	// Iterate: the greedy loop walk misses some loops at non-manifold junctions,
	// and each fill can expose newly closeable loops, so repeat until a pass
	// adds nothing (or the pass cap is hit).
	for( int pass = 0; pass < max_passes; ++pass ) {
		const size_t		  before = m.tris.size();
		// run-length count of undirected edges via sort (lighter than a hashmap)
		std::vector<uint64_t> ek;
		ek.reserve( m.tris.size() );
		for( size_t t = 0; t < m.tris.size(); t += 3 ) {
			ek.push_back( key( m.tris[t], m.tris[t + 1] ) );
			ek.push_back( key( m.tris[t + 1], m.tris[t + 2] ) );
			ek.push_back( key( m.tris[t + 2], m.tris[t] ) );
		}
		std::sort( ek.begin(), ek.end() );
		std::unordered_map<int, std::vector<int>> adj; // boundary-vertex adjacency
		for( size_t i = 0; i < ek.size(); ) {
			size_t j = i + 1;
			while( j < ek.size() && ek[j] == ek[i] )
				++j;
			if( j - i == 1 ) { // used by exactly one triangle -> boundary edge
				const int a = ( int )( ek[i] >> 32 ), b = ( int )( ek[i] & 0xffffffffu );
				adj[a].push_back( b );
				adj[b].push_back( a );
			}
			i = j;
		}
		if( adj.empty() ) break;

		std::unordered_set<uint64_t> used; // consumed boundary edges
		for( const auto& kv : adj ) {
			const int start = kv.first;
			for( const int nb0 : kv.second ) {
				if( used.count( key( start, nb0 ) ) ) continue;
				std::vector<int> loop { start };
				int				 prev = start, cur = nb0;
				used.insert( key( prev, cur ) );
				bool closed = false;
				while( ( int )loop.size() <= max_loop ) {
					loop.push_back( cur );
					if( cur == start ) {
						closed = true;
						break;
					}
					int	 next = -1;
					auto it	  = adj.find( cur );
					if( it != adj.end() )
						for( const int c : it->second )
							if( c != prev && !used.count( key( cur, c ) ) ) {
								next = c;
								break;
							}
					if( next < 0 ) break; // open chain / dead end
					used.insert( key( cur, next ) );
					prev = cur;
					cur	 = next;
				}
				if( !closed ) continue;
				loop.pop_back(); // drop the repeated start vertex
				const int k = ( int )loop.size();
				if( k < 3 || k > max_loop ) continue;
				for( int t = 1; t < k - 1; ++t ) { // fan-triangulate from loop[0]
					m.tris.push_back( loop[0] );
					m.tris.push_back( loop[t] );
					m.tris.push_back( loop[t + 1] );
				}
			}
		}
		if( m.tris.size() == before ) break; // converged
	}
}

// Settle the triangle winding globally, then take the normals' sign from it.
//
// extract() emits every quad in a fixed vertex order regardless of which way the
// surface crosses the edge, so the raw dual grid is *unoriented*: roughly one
// triangle in seven is wound against its neighbours. Our own viewer hides that
// (it never enables backface culling, turns the normal toward the camera and
// lights with abs(dot)), but every external consumer reads the winding instead:
// glTF front/back classification, Blender's face-orientation overlay, CGAL's
// alpha wrap and xatlas' chart packing. A glTF viewer flips the shading normal
// on a back-wound face, which speckles the surface with inverted lighting.
//
//   1. Propagate the orientation across every *manifold* edge (one used by
//      exactly two faces): those two must traverse the shared edge in opposite
//      directions. A parity union-find over faces satisfies all of the
//      constraints at once; the few that contradict a spanning assignment (odd
//      cycles) are dropped rather than propagated. Edges shared by more than two
//      faces carry no such constraint and are simply left out, so nothing
//      frustrates the assignment. This is the step vertex_normals gives up on,
//      but it gives up for a different reason: it needs a *vertex* sign field and
//      has to infer the relation from noisy normal directions, where the edge
//      constraint used here is exact. On a 1024 cascade mesh it puts 98.6% of the
//      triangles into one consistent patch, dropping 208 odd cycles out of 4.4M.
//   2. Give each patch its global sign, largest first. A patch that encloses
//      something is judged by the signed volume about its own centroid; the dust
//      left over is open sheet whose volume is numerically meaningless, so it
//      follows the majority of its already-decided neighbours across the
//      non-manifold seams, and only a wholly isolated island falls back to the
//      shading normals.
//   3. Rewrite the *sign* of the vertex normals from the settled winding, keeping
//      the smooth direction vertex_normals derived from the structure tensor.
//      Shading through abs(dot) is unaffected either way, so our viewer sees no
//      change; a glTF consumer sees a mesh that is right side out.
// Returns the number of triangles whose winding changed.
inline size_t orient_faces( Mesh& m, std::vector<float>& nrm )
{
	const size_t nv = m.n_verts();
	const size_t nt = m.n_tris();
	if( nt == 0 || nrm.size() != nv * 3 ) return 0;

	// ── (1) orientation constraints from the manifold edges ──────────────────
	// One entry per half-edge: the undirected edge as the sort key, the face and
	// the direction it traverses that edge in as the payload. Sorting keeps this
	// header dependency-free (as fill_holes does) at the cost of a transient
	// buffer proportional to the triangle count.
	std::vector<std::pair<uint64_t, uint32_t>> he;
	he.reserve( m.tris.size() );
	for( size_t t = 0; t < nt; ++t )
		for( int e = 0; e < 3; ++e ) {
			const uint32_t u = ( uint32_t )m.tris[t * 3 + e], v = ( uint32_t )m.tris[t * 3 + ( e + 1 ) % 3];
			const uint64_t k = u < v ? ( ( uint64_t )u << 32 ) | v : ( ( uint64_t )v << 32 ) | u;
			he.emplace_back( k, ( uint32_t )( ( t << 1 ) | ( u < v ? 1u : 0u ) ) );
		}
	std::sort( he.begin(), he.end() );

	std::vector<int>	 fp( nt ), fr( nt, 0 );
	std::vector<uint8_t> fb( nt, 0 ); // winding parity of a face relative to its parent
	for( size_t i = 0; i < nt; ++i )
		fp[i] = ( int )i;
	auto find = [&]( int f, int& parity ) {
		int p = 0;
		while( fp[f] != f ) {
			p ^= fb[f];
			f = fp[f];
		}
		parity = p;
		return f;
	};
	auto join = [&]( int a, int b, int rel ) {
		int pa, pb, ra = find( a, pa ), rb = find( b, pb );
		if( ra == rb ) return; // odd cycle: keep the spanning assignment, drop this
		if( fr[ra] < fr[rb] ) {
			std::swap( ra, rb );
			std::swap( pa, pb );
		}
		fp[rb] = ra;
		fb[rb] = ( uint8_t )( pa ^ pb ^ rel );
		if( fr[ra] == fr[rb] ) fr[ra]++;
	};
	// A run of equal keys is the faces meeting on one edge: exactly two give a
	// constraint, more than two are a non-manifold seam kept for step 2.
	std::vector<std::pair<uint32_t, uint32_t>> seams;
	for( size_t i = 0; i < he.size(); ) {
		size_t j = i + 1;
		while( j < he.size() && he[j].first == he[i].first )
			++j;
		if( j - i == 2 )
			join( ( int )( he[i].second >> 1 ), ( int )( he[i + 1].second >> 1 ), ( he[i].second & 1u ) == ( he[i + 1].second & 1u ) ? 1 : 0 );
		else if( j - i > 2 )
			for( size_t a = i; a < j; ++a )
				for( size_t b = a + 1; b < j; ++b )
					seams.emplace_back( he[a].second, he[b].second );
		i = j;
	}
	he.clear();
	he.shrink_to_fit();

	// ── (2) one global sign per patch ────────────────────────────────────────
	std::vector<uint8_t>		 par( nt ); // winding parity within its patch
	std::vector<int>			 pid( nt ); // dense patch id
	std::unordered_map<int, int> ids;
	for( size_t t = 0; t < nt; ++t ) {
		int		  p	  = 0;
		const int r	  = find( ( int )t, p );
		par[t]		  = ( uint8_t )p;
		const auto it = ids.find( r );
		if( it == ids.end() ) {
			const int n = ( int )ids.size();
			ids.emplace( r, n );
			pid[t] = n;
		} else
			pid[t] = it->second;
	}
	const size_t		np = ids.size();

	std::vector<double> cx( np, 0.0 ), cy( np, 0.0 ), cz( np, 0.0 ), cn( np, 0.0 );
	for( size_t t = 0; t < nt; ++t )
		for( int k = 0; k < 3; ++k ) {
			const int v = m.tris[t * 3 + k], p = pid[t];
			cx[p] += m.verts[( size_t )v * 3];
			cy[p] += m.verts[( size_t )v * 3 + 1];
			cz[p] += m.verts[( size_t )v * 3 + 2];
			cn[p] += 1.0;
		}
	for( size_t p = 0; p < np; ++p )
		if( cn[p] > 0.0 ) {
			cx[p] /= cn[p];
			cy[p] /= cn[p];
			cz[p] /= cn[p];
		}

	// Per patch: signed volume about that centroid, surface area (which tells an
	// enclosing patch from a sheet) and how well the winding matches the shading
	// normals (the last-resort tie-break).
	std::vector<double> vol( np, 0.0 ), area( np, 0.0 ), vote( np, 0.0 );
	std::vector<size_t> count( np, 0 );
	for( size_t t = 0; t < nt; ++t ) {
		int i0 = m.tris[t * 3], i1 = m.tris[t * 3 + 1], i2 = m.tris[t * 3 + 2];
		if( par[t] ) std::swap( i1, i2 );
		const int	 p = pid[t];
		const float* a = &m.verts[( size_t )i0 * 3];
		const float* b = &m.verts[( size_t )i1 * 3];
		const float* c = &m.verts[( size_t )i2 * 3];
		float		 e1[3], e2[3], fn[3];
		for( int k = 0; k < 3; ++k ) {
			e1[k] = b[k] - a[k];
			e2[k] = c[k] - a[k];
		}
		detail::cross( e1, e2, fn );
		area[p] += 0.5 * std::sqrt( ( double )fn[0] * fn[0] + ( double )fn[1] * fn[1] + ( double )fn[2] * fn[2] );
		const double ax = a[0] - cx[p], ay = a[1] - cy[p], az = a[2] - cz[p];
		const double bx = b[0] - cx[p], by = b[1] - cy[p], bz = b[2] - cz[p];
		const double dx = c[0] - cx[p], dy = c[1] - cy[p], dz = c[2] - cz[p];
		vol[p] += ax * ( by * dz - bz * dy ) + ay * ( bz * dx - bx * dz ) + az * ( bx * dy - by * dx );
		for( int k = 0; k < 3; ++k )
			vote[p] += ( double )fn[k] * ( nrm[( size_t )i0 * 3 + k] + nrm[( size_t )i1 * 3 + k] + nrm[( size_t )i2 * 3 + k] );
		count[p]++;
	}

	// Seam weight between two patches: positive when their faces already agree
	// across the non-manifold edges they share, assuming both keep their sign.
	std::vector<std::unordered_map<int, long long>> nb( np );
	for( const auto& s : seams ) {
		const uint32_t fa = s.first >> 1, fb2 = s.second >> 1;
		const int	   pa = pid[fa], pb = pid[fb2];
		if( pa == pb ) continue;
		const int		da = ( int )( s.first & 1u ) ^ par[fa];
		const int		db = ( int )( s.second & 1u ) ^ par[fb2];
		const long long w  = da != db ? 1 : -1;
		nb[pa][pb] += w;
		nb[pb][pa] += w;
	}

	std::vector<int> order( np );
	for( size_t p = 0; p < np; ++p )
		order[p] = ( int )p;
	std::sort( order.begin(), order.end(), [&]( int a, int b ) { return count[a] > count[b]; } );

	std::vector<uint8_t> pflip( np, 0 ), decided( np, 0 );
	for( const int p : order ) {
		// V/A^1.5 is dimensionless and says whether a patch encloses anything at
		// all: a sphere gives 0.094, an open sheet ~0. Only above that is the sign
		// of the volume meaningful.
		const double shape = area[p] > 0.0 ? ( vol[p] / 6.0 ) / std::pow( area[p], 1.5 ) : 0.0;
		long long	 w	   = 0;
		for( const auto& kv : nb[p] )
			if( decided[kv.first] ) w += pflip[kv.first] ? -kv.second : kv.second;
		if( std::fabs( shape ) > 1e-3 )
			pflip[p] = vol[p] < 0.0 ? 1 : 0;
		else if( w != 0 )
			pflip[p] = w < 0 ? 1 : 0;
		else
			pflip[p] = vote[p] < 0.0 ? 1 : 0;
		decided[p] = 1;
	}

	size_t flipped = 0;
	for( size_t t = 0; t < nt; ++t )
		if( par[t] ^ pflip[pid[t]] ) {
			std::swap( m.tris[t * 3 + 1], m.tris[t * 3 + 2] );
			++flipped;
		}

	// ── (3) take the normals' sign from the settled winding ──────────────────
	std::vector<float> geo( nv * 3, 0.0f );
	for( size_t t = 0; t < nt; ++t ) {
		const int	 i0 = m.tris[t * 3], i1 = m.tris[t * 3 + 1], i2 = m.tris[t * 3 + 2];
		const float* a = &m.verts[( size_t )i0 * 3];
		const float* b = &m.verts[( size_t )i1 * 3];
		const float* c = &m.verts[( size_t )i2 * 3];
		float		 e1[3], e2[3], fn[3];
		for( int k = 0; k < 3; ++k ) {
			e1[k] = b[k] - a[k];
			e2[k] = c[k] - a[k];
		}
		detail::cross( e1, e2, fn );
		for( const int i : { i0, i1, i2 } )
			for( int k = 0; k < 3; ++k )
				geo[( size_t )i * 3 + k] += fn[k];
	}
	for( size_t v = 0; v < nv; ++v ) {
		const double d = ( double )geo[v * 3] * nrm[v * 3] + ( double )geo[v * 3 + 1] * nrm[v * 3 + 1] + ( double )geo[v * 3 + 2] * nrm[v * 3 + 2];
		if( d < 0.0 )
			for( int k = 0; k < 3; ++k )
				nrm[v * 3 + k] = -nrm[v * 3 + k];
	}
	return flipped;
}

} // namespace fdg
