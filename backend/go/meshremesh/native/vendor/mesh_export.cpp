#include "mesh_export.h"

#include "print_remesh.h"
#include "quad_remesh.h"

#include "flexible_dual_grid.h"
#include "xatlas.h"

#define STB_IMAGE_WRITE_IMPLEMENTATION
#include "stb_image_write.h"

#include <algorithm>
#include <cmath>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <functional>
#include <mutex>
#include <unordered_map>

namespace t2glb
{
namespace
{

	// Stage logging to stderr, opt-in via T2GLB_VERBOSE (keeps the library quiet by
	// default while giving the CLI / debugging a progress trace).
	bool verbose()
	{
		static bool v = std::getenv( "T2GLB_VERBOSE" ) != nullptr;
		return v;
	}
#define GLBLOG( ... )                                     \
	do {                                                  \
		if( verbose() ) {                                 \
			std::fprintf( stderr, "[glb] " __VA_ARGS__ ); \
			std::fprintf( stderr, "\n" );                 \
		}                                                 \
	} while( 0 )

	// ─────────────────────────────────────────────────────────────────────────
	// Copy the original topology while dropping only invalid/degenerate faces and
	// unreferenced vertices. Export and showcase deliberately retain full polygon
	// density; component cleanup below is the only operation allowed to delete
	// valid faces.
	// ─────────────────────────────────────────────────────────────────────────
	bool copy_mesh( const float* verts, int nv, const int32_t* tris, int nt, const float* pbr, std::vector<float>& dverts, std::vector<int32_t>& dtris, std::vector<float>& dpbr, std::string& err )
	{
		std::vector<int> remap( ( size_t )nv, -1 );
		dverts.clear();
		dverts.reserve( ( size_t )nv * 3 );
		dtris.clear();
		dtris.reserve( ( size_t )nt * 3 );
		dpbr.clear();
		if( pbr )
			dpbr.reserve( ( size_t )nv * 6 );
		for( int t = 0; t < nt; ++t ) {
			const int32_t a = tris[3 * t], b = tris[3 * t + 1], c = tris[3 * t + 2];
			if( a < 0 || b < 0 || c < 0 || a >= nv || b >= nv || c >= nv ) {
				err = "triangle index out of range";
				return false;
			}
			if( a == b || b == c || a == c )
				continue;
			for( int32_t v : { a, b, c } ) {
				if( remap[v] < 0 ) {
					remap[v] = ( int )( dverts.size() / 3 );
					dverts.push_back( verts[3 * v + 0] );
					dverts.push_back( verts[3 * v + 1] );
					dverts.push_back( verts[3 * v + 2] );
					if( pbr )
						dpbr.insert( dpbr.end(), pbr + ( size_t )v * 6, pbr + ( size_t )v * 6 + 6 );
				}
				dtris.push_back( remap[v] );
			}
		}
		return true;
	}

	// Remove triangles belonging to small connected components (islands). The
	// dual-grid surface can contain tiny disconnected bits that each force a
	// separate UV chart and may correspond to unwanted background geometry.
	// Mirrors the reference's remove_small_connected_components. Keeps components
	// with at least `min_frac` of the triangles; recompacts verts/tris in place.
	void filter_components( std::vector<float>& v, std::vector<int32_t>& f, std::vector<float>& pbr, float min_frac, ComponentFilter mode )
	{
		if( mode == ComponentFilter::KeepAll )
			return;
		const int nv = ( int )( v.size() / 3 ), nt = ( int )( f.size() / 3 );
		if( nv == 0 || nt == 0 )
			return;
		std::vector<int> parent( ( size_t )nv ), sz( ( size_t )nv, 1 );
		for( int i = 0; i < nv; ++i )
			parent[i] = i;
		std::function<int( int )> find = [&]( int x ) {
			while( parent[x] != x ) {
				parent[x] = parent[parent[x]];
				x		  = parent[x];
			}
			return x;
		};
		auto uni = [&]( int a, int b ) {
			a = find( a );
			b = find( b );
			if( a == b )
				return;
			if( sz[a] < sz[b] )
				std::swap( a, b );
			parent[b] = a;
			sz[a] += sz[b];
		};
		for( int t = 0; t < nt; ++t ) {
			uni( f[3 * t + 0], f[3 * t + 1] );
			uni( f[3 * t + 1], f[3 * t + 2] );
		}

		std::unordered_map<int, int> tris_in; // component root -> triangle count
		for( int t = 0; t < nt; ++t )
			tris_in[find( f[3 * t + 0] )]++;
		const int keep_min = std::max( 1, ( int )( nt * min_frac ) );
		int		  largest = -1, largest_n = -1;
		for( const auto& it : tris_in ) {
			if( it.second > largest_n ) {
				largest	  = it.first;
				largest_n = it.second;
			}
		}

		std::vector<int>   vremap( ( size_t )nv, -1 );
		std::vector<float> nvts;
		nvts.reserve( v.size() );
		std::vector<float> npbr;
		if( !pbr.empty() )
			npbr.reserve( pbr.size() );
		std::vector<int32_t> nfs;
		nfs.reserve( f.size() );
		for( int t = 0; t < nt; ++t ) {
			const int root = find( f[3 * t + 0] );
			if( mode == ComponentFilter::KeepLargest ? root != largest : tris_in[root] < keep_min )
				continue;
			for( int k = 0; k < 3; ++k ) {
				int vi = f[3 * t + k];
				if( vremap[vi] < 0 ) {
					vremap[vi] = ( int )( nvts.size() / 3 );
					nvts.push_back( v[3 * vi + 0] );
					nvts.push_back( v[3 * vi + 1] );
					nvts.push_back( v[3 * vi + 2] );
					if( !pbr.empty() )
						npbr.insert( npbr.end(), pbr.begin() + ( size_t )vi * 6, pbr.begin() + ( size_t )vi * 6 + 6 );
				}
				nfs.push_back( vremap[vi] );
			}
		}
		if( !nfs.empty() ) {
			v.swap( nvts );
			f.swap( nfs );
			if( !pbr.empty() )
				pbr.swap( npbr );
		}
	}

	// Taubin (λ|μ) smoothing optionally regularises zig-zag sliver triangles so
	// per-face normals become coherent. Without this, xatlas may split a chart at
	// every normal jump (tens of thousands of one-off charts).
	// Taubin's alternating positive/negative passes smooth without the shrinkage of
	// plain Laplacian. The texture is unaffected (it samples the dense source).
	void taubin_smooth( std::vector<float>& v, const std::vector<int32_t>& f, int iters )
	{
		const int nv = ( int )( v.size() / 3 ), nt = ( int )( f.size() / 3 );
		if( nv == 0 || nt == 0 )
			return;
		std::vector<int> deg( ( size_t )nv, 0 );
		// Accumulate neighbour sums per pass; build degree once.
		for( int t = 0; t < nt; ++t )
			for( int k = 0; k < 3; ++k )
				deg[f[3 * t + k]] += 2; // each vertex in 2 edges of its triangle
		const float		   lambda = 0.5f, mu = -0.53f;
		std::vector<float> acc( ( size_t )nv * 3 );
		auto			   pass = [&]( float w ) {
			  std::fill( acc.begin(), acc.end(), 0.0f );
			  for( int t = 0; t < nt; ++t ) {
				  int a = f[3 * t + 0], b = f[3 * t + 1], c = f[3 * t + 2];
				  for( int e = 0; e < 3; ++e ) {
					  int i = ( e == 0 ? a : e == 1 ? b : c ), j = ( e == 0 ? b : e == 1 ? c : a );
					  acc[3 * i + 0] += v[3 * j + 0];
					  acc[3 * i + 1] += v[3 * j + 1];
					  acc[3 * i + 2] += v[3 * j + 2];
					  acc[3 * j + 0] += v[3 * i + 0];
					  acc[3 * j + 1] += v[3 * i + 1];
					  acc[3 * j + 2] += v[3 * i + 2];
				  }
			  }
			  for( int i = 0; i < nv; ++i ) {
				  if( deg[i] == 0 )
					  continue;
				  float inv = 1.0f / deg[i];
				  for( int k = 0; k < 3; ++k )
					  v[3 * i + k] += w * ( acc[3 * i + k] * inv - v[3 * i + k] ); // move toward neighbour centroid
			  }
		};
		for( int it = 0; it < iters; ++it ) {
			pass( lambda );
			pass( mu );
		}
	}

	// Area-weighted per-vertex normals over a mesh.
	void vertex_normals( const std::vector<float>& v, const std::vector<int32_t>& f, std::vector<float>& n )
	{
		const size_t nv = v.size() / 3;
		n.assign( nv * 3, 0.0f );
		for( size_t t = 0; t < f.size() / 3; ++t ) {
			int	  a = f[3 * t + 0], b = f[3 * t + 1], c = f[3 * t + 2];
			float e1[3] = { v[3 * b + 0] - v[3 * a + 0], v[3 * b + 1] - v[3 * a + 1], v[3 * b + 2] - v[3 * a + 2] };
			float e2[3] = { v[3 * c + 0] - v[3 * a + 0], v[3 * c + 1] - v[3 * a + 1], v[3 * c + 2] - v[3 * a + 2] };
			float fn[3] = { e1[1] * e2[2] - e1[2] * e2[1], e1[2] * e2[0] - e1[0] * e2[2], e1[0] * e2[1] - e1[1] * e2[0] };
			for( int k : { a, b, c } ) {
				n[3 * k + 0] += fn[0];
				n[3 * k + 1] += fn[1];
				n[3 * k + 2] += fn[2];
			}
		}
		for( size_t i = 0; i < nv; ++i ) {
			float l = std::sqrt( n[3 * i + 0] * n[3 * i + 0] + n[3 * i + 1] * n[3 * i + 1] + n[3 * i + 2] * n[3 * i + 2] ) + 1e-20f;
			n[3 * i + 0] /= l;
			n[3 * i + 1] /= l;
			n[3 * i + 2] /= l;
		}
	}

	// ─────────────────────────────────────────────────────────────────────────
	// PNG encode to an in-memory byte vector.
	// ─────────────────────────────────────────────────────────────────────────
	void png_sink( void* ctx, void* data, int size )
	{
		auto* v = ( std::vector<uint8_t>* )ctx;
		auto* p = ( uint8_t* )data;
		v->insert( v->end(), p, p + size );
	}
	bool encode_png( int w, int h, int comp, const uint8_t* pix, std::vector<uint8_t>& out )
	{
		out.clear();
		return stbi_write_png_to_func( png_sink, &out, w, h, comp, pix, w * comp ) != 0;
	}

	// ─────────────────────────────────────────────────────────────────────────
	// glTF 2.0 binary (GLB) assembly. One buffer holding, in order: POSITION,
	// NORMAL, TEXCOORD_0, indices, baseColor PNG, metallicRoughness PNG.
	// ─────────────────────────────────────────────────────────────────────────
	void put_u32( std::vector<uint8_t>& b, uint32_t v )
	{
		b.push_back( v & 0xFF );
		b.push_back( ( v >> 8 ) & 0xFF );
		b.push_back( ( v >> 16 ) & 0xFF );
		b.push_back( ( v >> 24 ) & 0xFF );
	}
	void pad4( std::vector<uint8_t>& b, uint8_t fill )
	{
		while( b.size() % 4 )
			b.push_back( fill );
	}

	void write_glb( const std::vector<float>& pos,
		const std::vector<float>&			  nrm,
		const std::vector<float>&			  uv,
		const std::vector<uint32_t>&		  idx,
		const std::vector<uint8_t>&			  basecolor_png,
		const std::vector<uint8_t>&			  metalrough_png,
		bool								  transparent,
		std::vector<uint8_t>&				  out )
	{
		const uint32_t		 nv = ( uint32_t )( pos.size() / 3 );
		const uint32_t		 ni = ( uint32_t )idx.size();

		// Assemble the BIN buffer, tracking 4-aligned bufferView offsets.
		std::vector<uint8_t> bin;
		auto				 view = [&]( const void* data, size_t bytes, uint32_t& off, uint32_t& len ) {
			pad4( bin, 0 );
			off				 = ( uint32_t )bin.size();
			len				 = ( uint32_t )bytes;
			const uint8_t* p = ( const uint8_t* )data;
			bin.insert( bin.end(), p, p + bytes );
		};
		uint32_t off_pos, len_pos, off_nrm, len_nrm, off_uv, len_uv, off_idx, len_idx;
		uint32_t off_bc, len_bc, off_mr, len_mr;
		view( pos.data(), pos.size() * 4, off_pos, len_pos );
		view( nrm.data(), nrm.size() * 4, off_nrm, len_nrm );
		view( uv.data(), uv.size() * 4, off_uv, len_uv );
		view( idx.data(), idx.size() * 4, off_idx, len_idx );
		view( basecolor_png.data(), basecolor_png.size(), off_bc, len_bc );
		view( metalrough_png.data(), metalrough_png.size(), off_mr, len_mr );
		pad4( bin, 0 );

		float pmin[3] = { 1e30f, 1e30f, 1e30f }, pmax[3] = { -1e30f, -1e30f, -1e30f };
		for( uint32_t i = 0; i < nv; ++i )
			for( int k = 0; k < 3; ++k ) {
				pmin[k] = std::min( pmin[k], pos[3 * i + k] );
				pmax[k] = std::max( pmax[k], pos[3 * i + k] );
			}

		char		buf[2048];
		std::string j = "{\"asset\":{\"version\":\"2.0\",\"generator\":\"trellis2cpp\"},";
		j += "\"scene\":0,\"scenes\":[{\"nodes\":[0]}],\"nodes\":[{\"mesh\":0}],";
		j += "\"meshes\":[{\"primitives\":[{\"attributes\":{\"POSITION\":0,\"NORMAL\":1,\"TEXCOORD_0\":2},\"indices\":3,\"material\":0}]}],";
		j += "\"materials\":[{\"pbrMetallicRoughness\":{\"baseColorTexture\":{\"index\":0},\"metallicRoughnessTexture\":{\"index\":1},\"metallicFactor\":1.0,\"roughnessFactor\":1.0},";
		if( transparent )
			j += "\"alphaMode\":\"BLEND\",";
		j += "\"doubleSided\":true}],";
		j += "\"textures\":[{\"source\":0,\"sampler\":0},{\"source\":1,\"sampler\":0}],";
		j += "\"images\":[{\"bufferView\":4,\"mimeType\":\"image/png\"},{\"bufferView\":5,\"mimeType\":\"image/png\"}],";
		// No mipmaps: opt-in xatlas charts use explicit dilation around their seams.
		j += "\"samplers\":[{\"magFilter\":9729,\"minFilter\":9729,\"wrapS\":33071,\"wrapT\":33071}],";
		snprintf( buf,
			sizeof buf,
			"\"accessors\":[{\"bufferView\":0,\"componentType\":5126,\"count\":%u,\"type\":\"VEC3\",\"min\":[%.8g,%.8g,%.8g],\"max\":[%.8g,%.8g,%.8g]},"
			"{\"bufferView\":1,\"componentType\":5126,\"count\":%u,\"type\":\"VEC3\"},"
			"{\"bufferView\":2,\"componentType\":5126,\"count\":%u,\"type\":\"VEC2\"},"
			"{\"bufferView\":3,\"componentType\":5125,\"count\":%u,\"type\":\"SCALAR\"}],",
			nv,
			pmin[0],
			pmin[1],
			pmin[2],
			pmax[0],
			pmax[1],
			pmax[2],
			nv,
			nv,
			ni );
		j += buf;
		snprintf( buf,
			sizeof buf,
			"\"bufferViews\":[{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34963},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u}],",
			off_pos,
			len_pos,
			off_nrm,
			len_nrm,
			off_uv,
			len_uv,
			off_idx,
			len_idx,
			off_bc,
			len_bc,
			off_mr,
			len_mr );
		j += buf;
		snprintf( buf, sizeof buf, "\"buffers\":[{\"byteLength\":%u}]}", ( uint32_t )bin.size() );
		j += buf;

		std::vector<uint8_t> json( j.begin(), j.end() );
		while( json.size() % 4 )
			json.push_back( 0x20 ); // pad JSON chunk with spaces

		const uint32_t total = 12 + 8 + ( uint32_t )json.size() + 8 + ( uint32_t )bin.size();
		out.clear();
		out.reserve( total );
		put_u32( out, 0x46546C67 );
		put_u32( out, 2 );
		put_u32( out, total ); // header
		put_u32( out, ( uint32_t )json.size() );
		put_u32( out, 0x4E4F534A ); // JSON chunk
		out.insert( out.end(), json.begin(), json.end() );
		put_u32( out, ( uint32_t )bin.size() );
		put_u32( out, 0x004E4942 ); // BIN chunk
		out.insert( out.end(), bin.begin(), bin.end() );
	}

	// Portable full-density export. glTF natively supports interpolated RGBA
	// vertex colours, which are a much better representation for TRELLIS' dense
	// per-vertex material field than assigning millions of triangles to a finite
	// 2D atlas. Metallic/roughness have no standard per-vertex glTF semantics, so
	// their averages drive the standard material while the original quantised
	// values are retained in the application attribute _METALLIC_ROUGHNESS.
	void write_vertex_glb( const PreparedMesh& mesh, std::vector<uint8_t>& out )
	{
		const uint32_t nv		= ( uint32_t )( mesh.verts.size() / 3 );
		const uint32_t ni		= ( uint32_t )mesh.tris.size();
		const bool	   textured = mesh.pbr.size() == ( size_t )nv * 6;
		auto		   to8		= []( float f ) {
			   int v = ( int )std::lround( f * 255.0f );
			   return ( uint8_t )std::min( 255, std::max( 0, v ) );
		};
		auto clamp01		= []( float f ) { return std::min( 1.0f, std::max( 0.0f, f ) ); };
		auto srgb_to_linear = [&]( float f ) {
			f = clamp01( f );
			return f <= 0.04045f ? f / 12.92f : std::pow( ( f + 0.055f ) / 1.055f, 2.4f );
		};
		auto to16 = []( float f ) {
			int v = ( int )std::lround( f * 65535.0f );
			return ( uint16_t )std::min( 65535, std::max( 0, v ) );
		};

		std::vector<float>	  pos( ( size_t )nv * 3 ), nrm( ( size_t )nv * 3 );
		// glTF COLOR_0 is linear rather than sRGB. Sixteen-bit UNORM keeps dark
		// generated colours precise after converting from the texture model's
		// sRGB-like output.
		std::vector<uint16_t> color( ( size_t )nv * 4 );
		std::vector<uint8_t>  metalrough( ( size_t )nv * 2 );
		std::vector<uint32_t> idx( ni );
		double				  metal_sum = 0.0, rough_sum = 0.0, weight_sum = 0.0;
		uint32_t			  translucent = 0;
		for( uint32_t i = 0; i < nv; ++i ) {
			// Trellis -> glTF Y-up, preserving handedness.
			pos[3 * i + 0] = mesh.verts[3 * i + 0];
			pos[3 * i + 1] = mesh.verts[3 * i + 2];
			pos[3 * i + 2] = -mesh.verts[3 * i + 1];
			nrm[3 * i + 0] = mesh.normals[3 * i + 0];
			nrm[3 * i + 1] = mesh.normals[3 * i + 2];
			nrm[3 * i + 2] = -mesh.normals[3 * i + 1];
			const float* p = textured ? mesh.pbr.data() + ( size_t )i * 6 : nullptr;
			const float	 r = p ? p[0] : 0.7f, g = p ? p[1] : 0.7f, b = p ? p[2] : 0.7f;
			const float	 m		  = p ? clamp01( p[3] ) : 0.0f;
			const float	 ro		  = p ? clamp01( p[4] ) : 0.6f;
			const float	 a		  = p ? clamp01( p[5] ) : 1.0f;
			color[4 * i + 0]	  = to16( srgb_to_linear( r ) );
			color[4 * i + 1]	  = to16( srgb_to_linear( g ) );
			color[4 * i + 2]	  = to16( srgb_to_linear( b ) );
			color[4 * i + 3]	  = to16( a );
			metalrough[2 * i + 0] = to8( m );
			metalrough[2 * i + 1] = to8( ro );
			// Near-transparent samples should not skew the visible material's
			// standard fallback factors.
			const double w = std::max( 0.001f, a );
			metal_sum += m * w;
			rough_sum += ro * w;
			weight_sum += w;
			if( a < 0.95f )
				++translucent;
		}
		for( uint32_t i = 0; i < ni; ++i )
			idx[i] = ( uint32_t )mesh.tris[i];
		const float			 metallic  = weight_sum ? ( float )( metal_sum / weight_sum ) : 0.0f;
		const float			 roughness = weight_sum ? ( float )( rough_sum / weight_sum ) : 0.6f;
		// BLEND disables normal opaque depth writes in many viewers. Do not switch
		// an entire multi-million-face primitive to blending because of a handful
		// of near-one decoder outliers; upstream likewise exports ordinary surfaces
		// as opaque. Keep blending for a material with meaningful transparency.
		const bool			 transparent = translucent > 0 && ( uint64_t )translucent * 1000 >= ( uint64_t )nv;

		std::vector<uint8_t> bin;
		auto				 view = [&]( const void* data, size_t bytes, uint32_t& off, uint32_t& len ) {
			pad4( bin, 0 );
			off				 = ( uint32_t )bin.size();
			len				 = ( uint32_t )bytes;
			const uint8_t* p = ( const uint8_t* )data;
			bin.insert( bin.end(), p, p + bytes );
		};
		uint32_t off_pos, len_pos, off_nrm, len_nrm, off_col, len_col;
		uint32_t off_mr, len_mr, off_idx, len_idx;
		view( pos.data(), pos.size() * 4, off_pos, len_pos );
		view( nrm.data(), nrm.size() * 4, off_nrm, len_nrm );
		view( color.data(), color.size() * sizeof( uint16_t ), off_col, len_col );
		view( metalrough.data(), metalrough.size(), off_mr, len_mr );
		view( idx.data(), idx.size() * 4, off_idx, len_idx );
		pad4( bin, 0 );

		float pmin[3] = { 1e30f, 1e30f, 1e30f }, pmax[3] = { -1e30f, -1e30f, -1e30f };
		for( uint32_t i = 0; i < nv; ++i )
			for( int k = 0; k < 3; ++k ) {
				pmin[k] = std::min( pmin[k], pos[3 * i + k] );
				pmax[k] = std::max( pmax[k], pos[3 * i + k] );
			}

		char		buf[3072];
		std::string j = "{\"asset\":{\"version\":\"2.0\",\"generator\":\"trellis2cpp\"},";
		j += "\"scene\":0,\"scenes\":[{\"nodes\":[0]}],\"nodes\":[{\"mesh\":0}],";
		j += "\"meshes\":[{\"primitives\":[{\"attributes\":{\"POSITION\":0,\"NORMAL\":1,\"COLOR_0\":2,\"_METALLIC_ROUGHNESS\":3},\"indices\":4,\"material\":0}]}],";
		std::snprintf( buf, sizeof buf, "\"materials\":[{\"pbrMetallicRoughness\":{\"baseColorFactor\":[1,1,1,1],\"metallicFactor\":%.8g,\"roughnessFactor\":%.8g},", metallic, roughness );
		j += buf;
		if( transparent )
			j += "\"alphaMode\":\"BLEND\",";
		j += "\"doubleSided\":true}],";
		std::snprintf( buf,
			sizeof buf,
			"\"accessors\":[{\"bufferView\":0,\"componentType\":5126,\"count\":%u,\"type\":\"VEC3\",\"min\":[%.8g,%.8g,%.8g],\"max\":[%.8g,%.8g,%.8g]},"
			"{\"bufferView\":1,\"componentType\":5126,\"count\":%u,\"type\":\"VEC3\"},"
			"{\"bufferView\":2,\"componentType\":5123,\"normalized\":true,\"count\":%u,\"type\":\"VEC4\"},"
			"{\"bufferView\":3,\"componentType\":5121,\"normalized\":true,\"count\":%u,\"type\":\"VEC2\"},"
			"{\"bufferView\":4,\"componentType\":5125,\"count\":%u,\"type\":\"SCALAR\"}],",
			nv,
			pmin[0],
			pmin[1],
			pmin[2],
			pmax[0],
			pmax[1],
			pmax[2],
			nv,
			nv,
			nv,
			ni );
		j += buf;
		std::snprintf( buf,
			sizeof buf,
			"\"bufferViews\":[{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34962},"
			"{\"buffer\":0,\"byteOffset\":%u,\"byteLength\":%u,\"target\":34963}],",
			off_pos,
			len_pos,
			off_nrm,
			len_nrm,
			off_col,
			len_col,
			off_mr,
			len_mr,
			off_idx,
			len_idx );
		j += buf;
		std::snprintf( buf, sizeof buf, "\"buffers\":[{\"byteLength\":%u}]}", ( uint32_t )bin.size() );
		j += buf;

		std::vector<uint8_t> json( j.begin(), j.end() );
		while( json.size() % 4 )
			json.push_back( 0x20 );
		const uint32_t total = 12 + 8 + ( uint32_t )json.size() + 8 + ( uint32_t )bin.size();
		out.clear();
		out.reserve( total );
		put_u32( out, 0x46546C67 );
		put_u32( out, 2 );
		put_u32( out, total );
		put_u32( out, ( uint32_t )json.size() );
		put_u32( out, 0x4E4F534A );
		out.insert( out.end(), json.begin(), json.end() );
		put_u32( out, ( uint32_t )bin.size() );
		put_u32( out, 0x004E4942 );
		out.insert( out.end(), bin.begin(), bin.end() );
	}

	// Chart-based unwrap via xatlas → per-output-vertex position/normal/uv (texel
	// space) + index buffer + atlas dims. Proper editable charts, but only fast on
	// clean, manifold meshes. Opt-in with T2GLB_XATLAS.
	bool xatlas_unwrap( const std::vector<float>& dverts,
		const std::vector<float>&				  dnrm,
		const std::vector<float>&				  dpbr,
		int										  dnv,
		const std::vector<int32_t>&				  dtris,
		int										  dnt,
		const MeshExportOptions&				  opt,
		int										  TS,
		std::vector<float>&						  opos,
		std::vector<float>&						  onrm,
		std::vector<float>&						  ouv,
		std::vector<float>&						  opbr,
		std::vector<uint32_t>&					  oidx,
		int&									  AW,
		int&									  AH,
		std::string&							  err )
	{
		xatlas::Atlas*	 atlas = xatlas::Create();
		xatlas::MeshDecl md {};
		md.vertexCount			= ( uint32_t )dnv;
		md.vertexPositionData	= dverts.data();
		md.vertexPositionStride = sizeof( float ) * 3;
		md.indexCount			= ( uint32_t )( dnt * 3 );
		md.indexData			= dtris.data();
		md.indexFormat			= xatlas::IndexFormat::UInt32;
		if( xatlas::AddMesh( atlas, md ) != xatlas::AddMeshError::Success ) {
			xatlas::Destroy( atlas );
			err = "xatlas AddMesh failed";
			return false;
		}
		GLBLOG( "xatlas computing charts ..." );
		xatlas::ChartOptions co {};
		co.normalDeviationWeight = 0.5f;
		co.roundnessWeight		 = 0.0f;
		co.straightnessWeight	 = 1.0f;
		co.normalSeamWeight		 = 1.0f;
		co.textureSeamWeight	 = 0.0f;
		co.maxCost				 = 8.0f;
		co.maxIterations		 = 1;
		xatlas::ComputeCharts( atlas, co );

		xatlas::PackOptions po {};
		po.padding	   = ( uint32_t )opt.padding;
		po.bilinear	   = true;
		po.createImage = false;
		uint32_t res   = ( uint32_t )TS;
		for( int attempt = 0; attempt < 4; ++attempt ) {
			po.resolution = res;
			xatlas::PackCharts( atlas, po );
			if( atlas->atlasCount <= 1 )
				break;
			res = ( uint32_t )( res * 1.5f );
		}
		GLBLOG( "atlas %ux%u, %u pages, %u charts", atlas->width, atlas->height, atlas->atlasCount, atlas->chartCount );
		if( atlas->meshCount != 1 || atlas->atlasCount != 1 || atlas->width == 0 || atlas->height == 0 ) {
			xatlas::Destroy( atlas );
			err = "xatlas packing failed (charts did not fit one atlas)";
			return false;
		}
		AW						= ( int )atlas->width;
		AH						= ( int )atlas->height;
		const xatlas::Mesh& om	= atlas->meshes[0];
		const uint32_t		onv = om.vertexCount;
		opos.resize( onv * 3 );
		onrm.resize( onv * 3 );
		ouv.resize( onv * 2 );
		if( !dpbr.empty() )
			opbr.resize( onv * 6 );
		for( uint32_t i = 0; i < onv; ++i ) {
			uint32_t xr = om.vertexArray[i].xref;
			if( xr >= ( uint32_t )dnv )
				xr = 0;
			opos[3 * i + 0] = dverts[3 * xr + 0];
			opos[3 * i + 1] = dverts[3 * xr + 1];
			opos[3 * i + 2] = dverts[3 * xr + 2];
			onrm[3 * i + 0] = dnrm[3 * xr + 0];
			onrm[3 * i + 1] = dnrm[3 * xr + 1];
			onrm[3 * i + 2] = dnrm[3 * xr + 2];
			ouv[2 * i + 0]	= om.vertexArray[i].uv[0];
			ouv[2 * i + 1]	= om.vertexArray[i].uv[1];
			if( !dpbr.empty() )
				std::memcpy( opbr.data() + ( size_t )i * 6, dpbr.data() + ( size_t )xr * 6, 6 * sizeof( float ) );
		}
		oidx.assign( om.indexArray, om.indexArray + om.indexCount );
		xatlas::Destroy( atlas );
		return true;
	}

	// UV unwrap and raster bake.  In the ordinary xatlas mode, PBR is interpolated
	// from the atlas mesh's own vertices.  For print wrapping, projection_source is
	// the dense pre-wrap mesh: every covered atlas texel is mapped to a 3D point on
	// the wrapped target, projected to the closest source triangle, and sampled
	// barycentrically.  That mirrors upstream's remesh -> UV raster -> BVH ->
	// attribute-field path without requiring CUDA.
	bool bake_atlas_locked( const PreparedMesh& mesh, const PreparedMesh* projection_source, const MeshExportOptions& opt, std::vector<uint8_t>& out, std::string& err )
	{
		const int  TS			 = opt.texture_size;
		const int  dnv			 = ( int )( mesh.verts.size() / 3 );
		const int  dnt			 = ( int )( mesh.tris.size() / 3 );
		const bool vertex_pbr	 = mesh.pbr.size() == ( size_t )dnv * 6;
		const bool projected_pbr = projection_source != nullptr;
		if( projected_pbr && projection_source->pbr.size() != projection_source->verts.size() / 3 * 6 ) {
			err = "PBR projection source has no six-channel material";
			return false;
		}

		std::vector<float>	  opos, onrm, ouv, opbr;
		std::vector<uint32_t> oidx;
		int					  AW = TS, AH = TS;
		if( !xatlas_unwrap( mesh.verts, mesh.normals, mesh.pbr, dnv, mesh.tris, dnt, opt, TS, opos, onrm, ouv, opbr, oidx, AW, AH, err ) )
			return false;
		const uint32_t onv	= ( uint32_t )( opos.size() / 3 );
		const uint32_t ntri = ( uint32_t )( oidx.size() / 3 );

		GLBLOG( "rasterizing %u tris%s ...", ntri, projected_pbr ? " for source-surface PBR projection" : "" );
		const int			 NP = AW * AH;
		std::vector<float>	 bc( ( size_t )NP * 3, 0.0f ), met( NP, 0.0f ), rou( NP, 0.0f ), alp( NP, 0.0f );
		std::vector<uint8_t> mask( NP, 0 );
		std::vector<float>	 query_points;
		std::vector<int32_t> query_pixels;
		if( projected_pbr ) {
			query_points.reserve( ( size_t )NP * 2 ); // atlases are normally 60-80% occupied
			query_pixels.reserve( ( size_t )NP * 2 / 3 );
		}
		auto set_default = [&]( int pix ) {
			bc[3 * pix + 0] = bc[3 * pix + 1] = bc[3 * pix + 2] = 0.7f;
			met[pix]											= 0.0f;
			rou[pix]											= 0.6f;
			alp[pix]											= 1.0f;
		};

		for( uint32_t t = 0; t < ntri; ++t ) {
			const uint32_t ia = oidx[3 * t + 0], ib = oidx[3 * t + 1], ic = oidx[3 * t + 2];
			const float	   ax = ouv[2 * ia + 0], ay = ouv[2 * ia + 1];
			const float	   bx = ouv[2 * ib + 0], by = ouv[2 * ib + 1];
			const float	   cx = ouv[2 * ic + 0], cy = ouv[2 * ic + 1];
			const float	   area = ( bx - ax ) * ( cy - ay ) - ( by - ay ) * ( cx - ax );
			if( std::fabs( area ) < 1e-9f )
				continue;
			const float inv_area = 1.0f / area;
			const int	x0		 = std::max( 0, ( int )std::floor( std::min( { ax, bx, cx } ) - 1 ) );
			const int	x1		 = std::min( AW - 1, ( int )std::ceil( std::max( { ax, bx, cx } ) + 1 ) );
			const int	y0		 = std::max( 0, ( int )std::floor( std::min( { ay, by, cy } ) - 1 ) );
			const int	y1		 = std::min( AH - 1, ( int )std::ceil( std::max( { ay, by, cy } ) + 1 ) );
			for( int py = y0; py <= y1; ++py ) {
				const float sy = py + 0.5f;
				for( int px = x0; px <= x1; ++px ) {
					const float sx = px + 0.5f;
					const float w0 = ( ( bx - sx ) * ( cy - sy ) - ( by - sy ) * ( cx - sx ) ) * inv_area;
					const float w1 = ( ( cx - sx ) * ( ay - sy ) - ( cy - sy ) * ( ax - sx ) ) * inv_area;
					const float w2 = 1.0f - w0 - w1;
					const float e  = -0.001f;
					if( w0 < e || w1 < e || w2 < e )
						continue;
					const int pix = py * AW + px;
					if( projected_pbr ) {
						// Shared triangle edges can cover the same pixel twice.
						// The positions agree, so retain the first and issue one
						// expensive closest-surface query per atlas texel.
						if( mask[pix] )
							continue;
						for( int k = 0; k < 3; ++k )
							query_points.push_back( w0 * opos[3 * ia + k] + w1 * opos[3 * ib + k] + w2 * opos[3 * ic + k] );
						query_pixels.push_back( pix );
						mask[pix] = 1;
						continue;
					}
					if( !vertex_pbr ) {
						set_default( pix );
						mask[pix] = 1;
						continue;
					}
					const float* a = opbr.data() + ( size_t )ia * 6;
					const float* b = opbr.data() + ( size_t )ib * 6;
					const float* c = opbr.data() + ( size_t )ic * 6;
					float		 val[6];
					for( int ch = 0; ch < 6; ++ch )
						val[ch] = w0 * a[ch] + w1 * b[ch] + w2 * c[ch];
					bc[3 * pix + 0] = val[0];
					bc[3 * pix + 1] = val[1];
					bc[3 * pix + 2] = val[2];
					met[pix]		= val[3];
					rou[pix]		= val[4];
					alp[pix]		= val[5];
					mask[pix]		= 1;
				}
			}
		}

		if( projected_pbr ) {
			GLBLOG( "projecting %zu covered texels to %zu source tris ...", query_pixels.size(), projection_source->tris.size() / 3 );
			std::vector<float> query_pbr;
			if( !t2print::project_pbr( projection_source->verts, projection_source->tris, projection_source->pbr, query_points, query_pbr, err ) )
				return false;
			if( query_pbr.size() != query_pixels.size() * 6 ) {
				err = "PBR projection returned an invalid sample count";
				return false;
			}
			for( size_t i = 0; i < query_pixels.size(); ++i ) {
				const int	 pix = query_pixels[i];
				const float* val = query_pbr.data() + i * 6;
				bc[3 * pix + 0]	 = val[0];
				bc[3 * pix + 1]	 = val[1];
				bc[3 * pix + 2]	 = val[2];
				met[pix]		 = val[3];
				rou[pix]		 = val[4];
				alp[pix]		 = val[5];
			}
			query_points.clear();
			query_points.shrink_to_fit();
			query_pbr.clear();
			query_pbr.shrink_to_fit();
		}

		// Edge-pad the gutter so bilinear sampling cannot bleed empty texels
		// across chart seams.
		{
			std::vector<uint8_t> m = mask;
			for( int pass = 0; pass < opt.dilate; ++pass ) {
				std::vector<uint8_t> nm = m;
				for( int py = 0; py < AH; ++py )
					for( int px = 0; px < AW; ++px ) {
						const int pix = py * AW + px;
						if( m[pix] )
							continue;
						float acc[6] = { 0, 0, 0, 0, 0, 0 };
						int	  cnt	 = 0;
						for( int dy = -1; dy <= 1; ++dy )
							for( int dx = -1; dx <= 1; ++dx ) {
								const int qx = px + dx, qy = py + dy;
								if( qx < 0 || qx >= AW || qy < 0 || qy >= AH )
									continue;
								const int q = qy * AW + qx;
								if( !m[q] )
									continue;
								acc[0] += bc[3 * q + 0];
								acc[1] += bc[3 * q + 1];
								acc[2] += bc[3 * q + 2];
								acc[3] += met[q];
								acc[4] += rou[q];
								acc[5] += alp[q];
								++cnt;
							}
						if( cnt ) {
							bc[3 * pix + 0] = acc[0] / cnt;
							bc[3 * pix + 1] = acc[1] / cnt;
							bc[3 * pix + 2] = acc[2] / cnt;
							met[pix]		= acc[3] / cnt;
							rou[pix]		= acc[4] / cnt;
							alp[pix]		= acc[5] / cnt;
							nm[pix]			= 1;
						}
					}
				m.swap( nm );
			}
		}

		// glTF packs metallic in B and roughness in G. Base color is stored as
		// generated (sRGB-like), matching upstream's base_color*255 texture path.
		auto to8 = []( float f ) {
			int v = ( int )std::lround( f * 255.0f );
			return ( uint8_t )std::min( 255, std::max( 0, v ) );
		};
		std::vector<uint8_t> bc8( ( size_t )NP * 4 ), mr8( ( size_t )NP * 3 );
		for( int i = 0; i < NP; ++i ) {
			bc8[4 * i + 0] = to8( bc[3 * i + 0] );
			bc8[4 * i + 1] = to8( bc[3 * i + 1] );
			bc8[4 * i + 2] = to8( bc[3 * i + 2] );
			bc8[4 * i + 3] = to8( alp[i] );
			mr8[3 * i + 0] = 0;
			mr8[3 * i + 1] = to8( rou[i] );
			mr8[3 * i + 2] = to8( met[i] );
		}
		int covered = 0, translucent = 0;
		for( int i = 0; i < NP; ++i )
			if( mask[i] ) {
				++covered;
				if( alp[i] < 0.95f )
					++translucent;
			}
		const bool transparent = translucent > 0 && ( int64_t )translucent * 1000 >= ( int64_t )covered;
		GLBLOG( "inpaint + PNG encode ..." );
		std::vector<uint8_t> bc_png, mr_png;
		if( !encode_png( AW, AH, 4, bc8.data(), bc_png ) || !encode_png( AW, AH, 3, mr8.data(), mr_png ) ) {
			err = "PNG encode failed";
			return false;
		}

		std::vector<float> gpos( ( size_t )onv * 3 ), gnrm( ( size_t )onv * 3 ), guv( ( size_t )onv * 2 );
		for( uint32_t i = 0; i < onv; ++i ) {
			gpos[3 * i + 0] = opos[3 * i + 0];
			gpos[3 * i + 1] = opos[3 * i + 2];
			gpos[3 * i + 2] = -opos[3 * i + 1];
			gnrm[3 * i + 0] = onrm[3 * i + 0];
			gnrm[3 * i + 1] = onrm[3 * i + 2];
			gnrm[3 * i + 2] = -onrm[3 * i + 1];
			guv[2 * i + 0]	= ouv[2 * i + 0] / ( float )AW;
			guv[2 * i + 1]	= ouv[2 * i + 1] / ( float )AH;
		}
		write_glb( gpos, gnrm, guv, oidx, bc_png, mr_png, transparent, out );
		GLBLOG( "GLB %zu bytes (%u verts, %u tris; %s PBR atlas)", out.size(), onv, ntri, projected_pbr ? "projected" : "vertex" );
		return true;
	}

	bool prepare_mesh_locked(
		const float* verts, int nv, const int32_t* tris, int nt, const float* pbr, const MeshExportOptions& opt, PreparedMesh& out, std::string& err, bool allow_atlas_smoothing = true )
	{
		if( !verts || !tris || nv <= 0 || nt <= 0 ) {
			err = "empty mesh";
			return false;
		}
		out = PreparedMesh {};

		GLBLOG( "input %d verts %d tris; preserving original topology", nv, nt );
		if( !copy_mesh( verts, nv, tris, nt, pbr, out.verts, out.tris, out.pbr, err ) )
			return false;

		filter_components( out.verts, out.tris, out.pbr, 0.0005f, opt.components );
		const int dnv = ( int )( out.verts.size() / 3 ), dnt = ( int )( out.tris.size() / 3 );
		if( dnv < 3 || dnt < 1 ) {
			err = "component cleanup produced an empty mesh";
			return false;
		}
		GLBLOG( "after component filter -> %d verts %d tris", dnv, dnt );

		// xatlas optionally regularises the geometry; do it here so the preview is
		// the geometry that will actually be written to the GLB.
		if( allow_atlas_smoothing && std::getenv( "T2GLB_XATLAS" ) && !std::getenv( "T2GLB_NOSMOOTH" ) )
			taubin_smooth( out.verts, out.tris, 3 );
		// The area-weighted normals below cancel wherever neighbouring triangles
		// disagree on their winding, and glTF consumers read the winding directly.
		// Generation settles the orientation now (fdg::orient_faces), but meshes
		// persisted before that still arrive here back-wound, so repair again —
		// it is a no-op on a mesh that is already consistently oriented.
		{
			fdg::Mesh		   m { out.verts, std::vector<int>( out.tris.begin(), out.tris.end() ) };
			std::vector<float> n	   = fdg::vertex_normals( m );
			const size_t	   rewound = fdg::orient_faces( m, n );
			out.tris.assign( m.tris.begin(), m.tris.end() );
			if( rewound )
				GLBLOG( "settled the winding: %zu of %d triangles re-wound", rewound, dnt );
		}
		vertex_normals( out.verts, out.tris, out.normals );
		return true;
	}

	std::mutex g_bake_mu; // serialize bakes (bounds peak RAM; keeps stb/xatlas tidy)

} // namespace

bool prepare_mesh( const float* verts, int nv, const int32_t* tris, int nt, const float* pbr, const MeshExportOptions& opt, PreparedMesh& out, std::string& err )
{
	std::lock_guard<std::mutex> lock( g_bake_mu );
	return prepare_mesh_locked( verts, nv, tris, nt, pbr, opt, out, err );
}

bool print_remesh_available()
{
	return t2print::available();
}

bool prepare_print_mesh(
	const float* verts, int nv, const int32_t* tris, int nt, const float* pbr, const MeshExportOptions& opt, float alpha_ratio, float offset_ratio, PreparedMesh& out, std::string& err )
{
	// Carry the source material through component filtering so it can be sampled
	// onto the wrap for a textured preview (source.pbr stays aligned with
	// source.verts/source.tris; empty when the caller passed no material).
	PreparedMesh source;
	if( !prepare_mesh( verts, nv, tris, nt, pbr, opt, source, err ) )
		return false;

	PreparedMesh wrapped;
	int			 capped = 0;
	if( !t2print::alpha_wrap( source.verts, source.tris, alpha_ratio, offset_ratio, wrapped.verts, wrapped.normals, wrapped.tris, err, &capped ) )
		return false;
	GLBLOG( "alpha wrap -> %zu verts %zu tris (%d source opening(s) capped first)", wrapped.verts.size() / 3, wrapped.tris.size() / 3, capped );
	// Alpha Wrap constructs entirely new offset vertices, so source per-vertex
	// attributes cannot be carried directly. When the source is textured, sample
	// its material at each wrap vertex by closest-surface projection — the same
	// transfer the GLB download bakes per texel, here per vertex for a cheap,
	// approximate preview. Projection is best-effort: on failure the wrap still
	// previews untextured rather than aborting the print preview entirely.
	if( !source.pbr.empty() && source.pbr.size() == source.verts.size() / 3 * 6 ) {
		std::string perr;
		if( !t2print::project_pbr( source.verts, source.tris, source.pbr, wrapped.verts, wrapped.pbr, perr ) ) {
			wrapped.pbr.clear();
		}
	} else {
		wrapped.pbr.clear();
	}
	out = std::move( wrapped );
	return true;
}

bool quad_remesh_available()
{
	return t2quad::available();
}

bool prepare_quad_mesh( const float* verts,
	int								 nv,
	const int32_t*					 tris,
	int								 nt,
	const float*					 pbr,
	const MeshExportOptions&		 opt,
	int								 target_quads,
	float							 adaptivity,
	PreparedMesh&					 out,
	QuadMeshStats*					 stats,
	std::string&					 err )
{
	if( !t2quad::available() ) {
		err = "quad remeshing is unavailable (this build has no AutoRemesher backend)";
		return false;
	}

	// Same shape as prepare_print_mesh: filter first, so the remesher never sees
	// the tiny stray islands the component filter exists to remove.
	PreparedMesh source;
	if( !prepare_mesh( verts, nv, tris, nt, pbr, opt, source, err ) )
		return false;

	t2quad::QuadRemeshOptions qopt;
	if( target_quads > 0 )
		qopt.target_quads = target_quads;
	if( adaptivity >= 0.0f && adaptivity <= 1.0f )
		qopt.adaptivity = adaptivity;

	std::vector<float>		quad_verts;
	std::vector<int32_t>	quad_faces, quad_face_sizes;
	t2quad::QuadRemeshStats qstats;
	if( !t2quad::remesh( source.verts, source.tris, qopt, quad_verts, quad_faces, quad_face_sizes, qstats, err ) )
		return false;

	PreparedMesh remeshed;
	remeshed.verts = std::move( quad_verts );
	// Everything downstream (GLB, .t2mesh, the C API) takes triangles, so the
	// quad stream is split here. The quads themselves stay reachable through
	// t2quad::remesh for a future quad-preserving export.
	t2quad::triangulate( remeshed.verts, quad_faces, quad_face_sizes, remeshed.tris );
	if( remeshed.tris.empty() ) {
		err = "quad remeshing produced no triangles";
		return false;
	}
	vertex_normals( remeshed.verts, remeshed.tris, remeshed.normals );

	// Quad remeshing builds new vertices, so the source material is sampled onto
	// them by closest-surface projection - the same cheap per-vertex preview the
	// Alpha Wrap path uses, with the sharper per-texel rebake left to the GLB
	// download. Best-effort: a failed projection previews untextured rather than
	// failing the whole remesh.
	if( !source.pbr.empty() && source.pbr.size() == source.verts.size() / 3 * 6 ) {
		std::string perr;
		if( !t2print::project_pbr( source.verts, source.tris, source.pbr, remeshed.verts, remeshed.pbr, perr ) )
			remeshed.pbr.clear();
	}

	if( stats ) {
		stats->quads		  = qstats.quads;
		stats->triangles	  = qstats.triangles;
		stats->ngons		  = qstats.ngons;
		stats->boundary_edges = qstats.boundary_edges;
		stats->area_retained  = qstats.area_retained;
	}
	out = std::move( remeshed );
	return true;
}

bool mesh_to_glb( const float* verts, int nv, const int32_t* tris, int nt, const float* pbr, const MeshExportOptions& opt, std::vector<uint8_t>& out, std::string& err )
{
	if( nv <= 0 || nt <= 0 ) {
		err = "empty mesh";
		return false;
	}
	const int TS = opt.texture_size;
	if( TS < 16 || TS > 8192 ) {
		err = "bad texture_size";
		return false;
	}

	std::lock_guard<std::mutex> lock( g_bake_mu );

	// 1) preserve topology + optional component filter --------------------
	PreparedMesh				prepared;
	if( !prepare_mesh_locked( verts, nv, tris, nt, pbr, opt, prepared, err ) )
		return false;
	const int  dnv = ( int )( prepared.verts.size() / 3 ), dnt = ( int )( prepared.tris.size() / 3 );
	const bool use_xatlas = std::getenv( "T2GLB_XATLAS" ) != nullptr;

	// A fixed-size per-triangle grid atlas cannot represent a full-density
	// TRELLIS mesh: at 3.1M triangles, even 2048² gives each face barely one
	// texel before gutters. The old fallback consequently emitted unpainted,
	// transparent cells and visible rectangular/triangular colour blocks.
	// Preserve the material field directly on the original vertices instead.
	if( !use_xatlas ) {
		write_vertex_glb( prepared, out );
		GLBLOG( "GLB %zu bytes (%d verts, %d tris; vertex PBR)", out.size(), dnv, dnt );
		return true;
	}

	return bake_atlas_locked( prepared, nullptr, opt, out, err );
}

bool mesh_to_projected_glb( const float* target_verts,
	int									 target_nv,
	const int32_t*						 target_tris,
	int									 target_nt,
	const float*						 source_verts,
	int									 source_nv,
	const int32_t*						 source_tris,
	int									 source_nt,
	const float*						 source_pbr,
	const MeshExportOptions&			 opt,
	std::vector<uint8_t>&				 out,
	std::string&						 err )
{
	if( !t2print::projection_available() ) {
		err = "PBR projection is unavailable";
		return false;
	}
	if( !target_verts || !target_tris || target_nv <= 0 || target_nt <= 0 || !source_verts || !source_tris || !source_pbr || source_nv <= 0 || source_nt <= 0 ) {
		err = "empty projected GLB mesh";
		return false;
	}
	if( opt.texture_size < 16 || opt.texture_size > 8192 ) {
		err = "bad texture_size";
		return false;
	}

	std::lock_guard<std::mutex> lock( g_bake_mu );
	PreparedMesh				target, source;
	MeshExportOptions			target_opt = opt;
	target_opt.components				   = ComponentFilter::KeepAll;
	// The target is the exact already-previewed Alpha Wrap.  Do not let the
	// legacy T2GLB_XATLAS smoothing switch move it during export.
	if( !prepare_mesh_locked( target_verts, target_nv, target_tris, target_nt, nullptr, target_opt, target, err, false ) )
		return false;
	if( !prepare_mesh_locked( source_verts, source_nv, source_tris, source_nt, source_pbr, opt, source, err, false ) )
		return false;
	return bake_atlas_locked( target, &source, opt, out, err );
}

} // namespace t2glb
