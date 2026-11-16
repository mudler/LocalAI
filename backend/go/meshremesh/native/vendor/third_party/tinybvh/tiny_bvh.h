/*
The MIT License (MIT)

Copyright (c) 2024-2026, Jacco Bikker / Breda University of Applied Sciences.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
*/

// How to use:
//
// Use this in *one* .c or .cpp
//   #define TINYBVH_IMPLEMENTATION
//   #include "tiny_bvh.h"
// Instantiate a BVH and build it for a list of triangles:
//   BVH bvh;
//   bvh.Build( (bvhvec4*)myVerts, numTriangles );
//   Ray ray( bvhvec3( 0, 0, 0 ), bvhvec3( 0, 0, 1 ), 1e30f );
//   bvh.Intersect( ray );
// After this, intersection information is in ray.hit.

// tinybvh can use custom vector types by defining TINYBVH_USE_CUSTOM_VECTOR_TYPES once before inclusion.
// To define custom vector types create a tinybvh namespace with the appropriate using directives, e.g.:
//	 namespace tinybvh
//   {
//     using bvhint2 = math::int2;
//     using bvhint3 = math::int3;
//     using bvhuint2 = math::uint2;
//     using bvhuint3 = math::uint3;
//     using bvhuint4 = math::uint4;
//     using bvhvec2 = math::float2;
//     using bvhvec3 = math::float3;
//     using bvhvec4 = math::float4;
//     using bvhdbl3 = math::double3;
//     using bvhmat4 = math::mat4x4;
//   }
//
//	 #define TINYBVH_USE_CUSTOM_VECTOR_TYPES
//   #include <tiny_bvh.h>

// tinybvh can be further configured using #defines, to be specified before the #include:
// #define BVHBINS 8        - the number of bins to use in regular BVH construction. Default is 8.
// #define HQBVHBINS 32     - the number of bins to use in SBVH construction. Default is 8.
// #define INST_IDX_BITS 10 - the number of bits to use for the instance index. Default is 32,
//                            which stores the bits in a separate field in tinybvh::Intersection.
// #define C_INT 1          - the estimated cost of a primitive intersection test. Default is 1.
// #define C_TRAV 1         - the estimated cost of a traversal step. Default is 1.

// See tiny_bvh_minimal.cpp for basic usage. In short:
// instantiate a BVH: tinybvh::BVH bvh;
// build it: bvh.Build( (tinybvh::bvhvec4*)triangleData, TRIANGLE_COUNT );
// ..where triangleData is an array of four-component float vectors:
// - For a single triangle, provide 3 vertices,
// - For each vertex provide x, y and z.
// The fourth float in each vertex is a dummy value and exists purely for
// a more efficient layout of the data in memory.

// A manual for tinybvh can be found here:
// https://jacco.ompf2.com/2025/01/24/tinybvh-manual-basic-use
// https://jacco.ompf2.com/2025/01/25/tinybvh-manual-advanced

// More information about the BVH data structure:
// https://jacco.ompf2.com/2022/04/13/how-to-build-a-bvh-part-1-basics

// Further references: See README.md

// Get the latest version of tinybvh from GitHub:
// github.com/jbikker/tinybvh

// Author and contributors:
// Jacco Bikker: BVH code and examples
// Eddy L O Jansson: g++ / clang support
// Aras Pranckevičius: non-Intel architecture support
// Jefferson Amstutz: CMake support
// Christian Oliveros: WASM / EMSCRIPTEN support
// Thierry Cantenot: user-defined alloc & free
// David Peicho: slices & Rust bindings, API advice
// Aytek Aman: C++11 threading implementation
// .. and other contributors: see GitHub page.

#ifndef TINY_BVH_H_
#define TINY_BVH_H_

// Library version:
#define TINY_BVH_VERSION_MAJOR	1
#define TINY_BVH_VERSION_MINOR	7
#define TINY_BVH_VERSION_SUB	1

// Cached BVH file version - increases only when file layout changes.
#define TINY_BVH_CACHE_VERSION	171

// Run-time checks / debugging.
// #define PARANOID // checks out-of-bound access of slices
// #define SLICEDUMP // dumps the slice used for building to a file - debug feature.

// Binned BVH building: bin count.
#ifndef BVHBINS
#define BVHBINS 8
#endif
#ifndef HQBVHBINS
#define HQBVHBINS 8
#define MAXHQBINS 256
#endif
#define AVXBINS 8 // must stay at 8.

// TLAS setting
// Note: Except when INST_IDX_BITS is set to 32, the instance index is encoded in
// the top bits of the prim idx field.
// Max number of instances in TLAS: 2 ^ INST_IDX_BITS
// Max number of primitives per BLAS: 2 ^ (32 - INST_IDX_BITS)
#ifndef INST_IDX_BITS
#define INST_IDX_BITS 32 // Use 4..~12 to use prim field bits for instance id, or set to 32 to store index in separate field.
#endif

// SAH BVH building: Heuristic parameters
// CPU traversal: C_INT = 1, C_TRAV = 1 seems optimal.
// These are defaults, which initialize the public members c_int and c_trav in
// BVHBase (and thus each BVH instance).
#ifndef C_INT
#define C_INT	1
#endif
#ifndef C_TRAV
#define C_TRAV	1
#endif
#ifndef W_EPO
#define W_EPO	0.71f
#endif

// SBVH: "Unsplitting"
#define SBVH_UNSPLITTING
#define RDH_MAX_WEIGHT 0.8f

// Triangle intersection: "Watertight"
// #define WATERTIGHT_TRITEST

// 'Infinity' values
#define BVH_FAR	1e30f		// actual valid ieee range: 3.40282347E+38
#define BVH_DBL_FAR 1e300	// actual valid ieee range: 1.797693134862315E+308

// Threaded builds
#ifndef MT_NO_JOBSYSTEM
#define MT_USE_JOBSYSTEM	// use jobsystem for threading - better than std::thread.
#define MT_JOBSYSTEM_DEPTH	9	// will generate up to 2^N tasks
#endif
#ifndef MT_BUILD_THRESHOLD
#define MT_BUILD_THRESHOLD 50000 // single-threaded builds below this triangle count
#endif

// Features
#ifndef NO_DOUBLE_PRECISION_SUPPORT
#define DOUBLE_PRECISION_SUPPORT
#endif
// #define TINYBVH_USE_CUSTOM_VECTOR_TYPES
// #define TINYBVH_NO_SIMD
#ifndef NO_INDEXED_GEOMETRY
#define ENABLE_INDEXED_GEOMETRY
#endif
#ifndef NO_CUSTOM_GEOMETRY
#define ENABLE_CUSTOM_GEOMETRY
#endif
#ifndef NO_THREADED_BUILDS
#define ENABLE_THREADED_BUILDS
#endif

// Experimental / WIP features

// CWBVH triangle format - doesn't seem to help on GPU?
// #define CWBVH_COMPRESSED_TRIS
// BVH4 triangle format
// #define BVH4_GPU_COMPRESSED_TRIS

// ============================================================================
//
//        P R E L I M I N A R I E S
//
// ============================================================================

// needful includes
#ifdef _MSC_VER // Visual Studio / C11
#include <malloc.h> // for alloc/free
#include <stdio.h> // for fprintf
#include <math.h> // for sqrtf, fabs
#include <string.h> // for memset
#include <stdlib.h> // for exit(1)
#else // Emscripten / gcc / clang
#include <cstdlib>
#include <cstdio>
#include <cmath>
#include <cstring>
#endif
#include <cstdint>
#include <atomic> // for SBVH builds
#include <vector>
#include <thread> // for threaded builds

// Platform-independent compile-time warnings.
#define EMIT_COMPILER_WARNING_STRINGIFY0(x) #x
#define EMIT_COMPILER_WARNING_STRINGIFY1(x) EMIT_COMPILER_WARNING_STRINGIFY0(x)
#ifdef __GNUC__
#define EMIT_COMPILER_WARNING_COMPOSE(x) GCC warning x
#else
#define EMIT_COMPILER_MESSAGE_PREFACE(type) \
__FILE__ "(" EMIT_COMPILER_WARNING_STRINGIFY1(__LINE__) "): " type ": "
#define EMIT_COMPILER_WARNING_COMPOSE(x) message(EMIT_COMPILER_MESSAGE_PREFACE("warning C0000") x)
#endif
#define WARNING(x) _Pragma(EMIT_COMPILER_WARNING_STRINGIFY1(EMIT_COMPILER_WARNING_COMPOSE(x)))

// SSE/AVX/AVX2/NEON support.
// SSE4.2 availability: Since Nehalem (2008)
// AVX1 availability: Since Sandy Bridge (2011)
// AVX2 availability: Since Haswell (2013)
#ifndef TINYBVH_NO_SIMD
#if defined __x86_64__ || defined _M_X64 || defined __wasm_simd128__ || defined __wasm_relaxed_simd__
#if !defined __SSE4_2__  && !defined _MSC_VER
WARNING( "SSE4.2 not enabled in compilation." )
#else
#define BVH_USESSE
#ifndef __SSE4_2__
#define __SSE4_2__		// msvc doesn't set the SSE flag
#endif
#endif
#if !defined __AVX__
WARNING( "AVX not enabled in compilation." )
#define TINYBVH_NO_SIMD
#else
#define BVH_USEAVX		// required for BuildAVX, BVH_SoA and others
#define BVH_USESSE
#endif
#if !defined __AVX2__ || (!defined __FMA__ && !defined _MSC_VER)
WARNING( "AVX2 and FMA not enabled in compilation." )
#define TINYBVH_NO_SIMD
#else
#define BVH_USEAVX2		// required for BVH8_CPU
#define BVH_USEAVX
#define BVH_USESSE
#endif
#include "immintrin.h"	// for __m128 and __m256
#elif defined __aarch64__ || defined _M_ARM64
#if !defined __ARM_NEON && !defined __APPLE__
WARNING( "NEON not enabled in compilation." )
#define TINYBVH_NO_SIMD
#else
#define BVH_USENEON
#include "arm_neon.h"
#endif
#endif
#endif // TINYBVH_NO_SIMD

// aligned memory allocation
// note: formally, size needs to be a multiple of 'alignment', see:
// https://en.cppreference.com/w/c/memory/aligned_alloc.
// EMSCRIPTEN enforces this.
// Copy of the same construct in tinyocl, in a different namespace.
namespace tinybvh {
inline size_t make_multiple_of( size_t x, size_t alignment ) { return (x + (alignment - 1)) & ~(alignment - 1); }
#ifndef _ALIGNED_ALLOC
#ifdef _MSC_VER // Visual Studio / C11
#define ALIGNED( x ) __declspec( align( x ) )
#define _ALIGNED_ALLOC(alignment,size) _aligned_malloc( make_multiple_of( size, alignment ), alignment )
#define _ALIGNED_FREE(ptr) _aligned_free( ptr )
#else // EMSCRIPTEN / gcc / clang / Android
#define ALIGNED( x ) __attribute__( ( aligned( x ) ) )
#if !defined TINYBVH_NO_SIMD && (defined __x86_64__ || defined _M_X64 || defined __wasm_simd128__ || defined __wasm_relaxed_simd__)
#include <xmmintrin.h>
#define _ALIGNED_ALLOC(alignment,size) _mm_malloc( make_multiple_of( size, alignment ), alignment )
#define _ALIGNED_FREE(ptr) _mm_free( ptr )
#elif defined(__ANDROID__)
#include <malloc.h>
#include <android/api-level.h>
// Android API 28+ supports aligned_alloc, but older versions (like API 24) 
// require memalign for aligned memory.
#if defined(__ANDROID_API__) && (__ANDROID_API__ >= 28) // Modern Android (9.0+)
#define _ALIGNED_ALLOC(alignment,size) aligned_alloc( alignment, make_multiple_of( size, alignment ) )
#else // Legacy Android
#define _ALIGNED_ALLOC(alignment,size) memalign( alignment, make_multiple_of( size, alignment ) )
#endif
#define _ALIGNED_FREE(ptr) free( ptr )
#elif defined(__EMSCRIPTEN__) || defined(__APPLE__) || defined(__aarch64__)
// Emscripten and Apple strictly follow C11 aligned_alloc
#define _ALIGNED_ALLOC(alignment,size) aligned_alloc( alignment, make_multiple_of( size, alignment ) )
#define _ALIGNED_FREE(ptr) free( ptr )
#elif defined(__GNUC__)
#ifdef __linux__
#define _ALIGNED_ALLOC(alignment,size) aligned_alloc( alignment, make_multiple_of( size, alignment ) )
#else
#define _ALIGNED_ALLOC(alignment,size) _mm_malloc( make_multiple_of( size, alignment ), alignment );
#endif
#define _ALIGNED_FREE(ptr) free( ptr )
#else
// Fallback
#define _ALIGNED_ALLOC(alignment,size) malloc( size )
#define _ALIGNED_FREE(ptr) free( ptr )
#endif
#endif
#endif
inline void* malloc64( size_t size, void* = nullptr ) { return size == 0 ? 0 : _ALIGNED_ALLOC( 64, size ); }
inline void free64( void* ptr, void* = nullptr ) { _ALIGNED_FREE( ptr ); }
}; // namespace tiybvh

// Derived TLAS things; for convenience.
#define INST_IDX_SHFT (32 - INST_IDX_BITS)
#if INST_IDX_BITS == 32
#define PRIM_IDX_MASK 0xffffffff // instance index stored separately.
#else
#define PRIM_IDX_MASK ((1 << INST_IDX_SHFT) - 1) // instance index stored in top bits of hit.prim.
#endif

// Forced inlining.
#ifdef _MSC_VER
#define __FORCEINLINE __forceinline
#else
#define __FORCEINLINE __attribute__((always_inline)) inline
#endif

namespace tinybvh {

// We'll use this whenever a layout has no specialized shadow ray query.
#define FALLBACK_SHADOW_QUERY( s ) { Ray r = s; float d = s.hit.t; Intersect( r ); return r.hit.t < d; }

#ifdef _MSC_VER
// Suppress a warning caused by the union of x,y,.. and cell[..] in vectors.
// We need this union to address vector components either by name or by index.
// The warning is re-enabled right after the definition of the data types.
#pragma warning ( push )
#pragma warning ( disable: 4201 /* nameless struct / union */ )
#endif

#ifndef TINYBVH_USE_CUSTOM_VECTOR_TYPES

struct bvhvec3;
struct ALIGNED( 16 ) bvhvec4
{
	// vector naming is designed to not cause any name clashes.
	bvhvec4() = default;
	constexpr bvhvec4( const float a, const float b, const float c, const float d ) : x( a ), y( b ), z( c ), w( d ) {}
	constexpr bvhvec4( const float a ) : x( a ), y( a ), z( a ), w( a ) {}
	bvhvec4( const bvhvec3 & a );
	bvhvec4( const bvhvec3 & a, float b );
	constexpr float& operator [] ( const int32_t i ) { return cell[i]; }
	const float& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { float x, y, z, w; }; float cell[4]; };
};

struct ALIGNED( 8 ) bvhvec2
{
	bvhvec2() = default;
	constexpr bvhvec2( const float a, const float b ) : x( a ), y( b ) {}
	constexpr bvhvec2( const float a ) : x( a ), y( a ) {}
	constexpr bvhvec2( const bvhvec4 a ) : x( a.x ), y( a.y ) {}
	constexpr float& operator [] ( const int32_t i ) { return cell[i]; }
	const float& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { float x, y; }; float cell[2]; };
};

struct bvhvec3
{
	bvhvec3() = default;
	constexpr bvhvec3( const float a, const float b, const float c ) : x( a ), y( b ), z( c ) {}
	constexpr bvhvec3( const float a ) : x( a ), y( a ), z( a ) {}
	constexpr bvhvec3( const bvhvec4 a ) : x( a.x ), y( a.y ), z( a.z ) {}
	constexpr float& operator [] ( const int32_t i ) { return cell[i]; }
	const float& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { float x, y, z; }; float cell[3]; };
};

struct bvhint3
{
	bvhint3() = default;
	constexpr bvhint3( const int32_t a, const int32_t b, const int32_t c ) : x( a ), y( b ), z( c ) {}
	constexpr bvhint3( const int32_t a ) : x( a ), y( a ), z( a ) {}
	bvhint3( const bvhvec3& a ) { x = (int32_t)a.x, y = (int32_t)a.y, z = (int32_t)a.z; }
	constexpr int32_t& operator [] ( const int32_t i ) { return cell[i]; }
	const int32_t& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { int32_t x, y, z; }; int32_t cell[3]; };
};

struct bvhint2
{
	bvhint2() = default;
	constexpr bvhint2( const int32_t a, const int32_t b ) : x( a ), y( b ) {}
	constexpr bvhint2( const int32_t a ) : x( a ), y( a ) {}
	constexpr int32_t& operator [] ( const int32_t i ) { return cell[i]; }
	const int32_t& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { int32_t x, y; }; int32_t cell[2]; };
};

struct bvhuint2
{
	bvhuint2() = default;
	constexpr bvhuint2( const uint32_t a, const uint32_t b ) : x( a ), y( b ) {}
	constexpr bvhuint2( const uint32_t a ) : x( a ), y( a ) {}
	constexpr uint32_t& operator [] ( const int32_t i ) { return cell[i]; }
	const uint32_t& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { uint32_t x, y; }; uint32_t cell[2]; };
};

struct bvhuint3
{
	bvhuint3() = default;
	constexpr bvhuint3( const uint32_t a, const uint32_t b, const uint32_t c ) : x( a ), y( b ), z( c ) {}
	constexpr bvhuint3( const uint32_t a ) : x( a ), y( a ), z( a ) {}
	constexpr uint32_t& operator [] ( const int32_t i ) { return cell[i]; }
	const uint32_t& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { uint32_t x, y, z; }; uint32_t cell[3]; };
};

struct bvhuint4
{
	bvhuint4() = default;
	constexpr bvhuint4( const uint32_t a, const uint32_t b, const uint32_t c, const uint32_t d ) : x( a ), y( b ), z( c ), w( d ) {}
	constexpr bvhuint4( const uint32_t a ) : x( a ), y( a ), z( a ), w( a ) {}
	constexpr uint32_t& operator [] ( const int32_t i ) { return cell[i]; }
	const uint32_t& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { uint32_t x, y, z, w; }; uint32_t cell[4]; };
};

struct bvhmat4 // exists only so we can use tinybvh types conveniently in tinyscene.
{
	bvhmat4() = default;
	constexpr float& operator [] ( const int32_t i ) { return cell[i]; }
	constexpr const float& operator [] ( const int32_t i ) const { return cell[i]; }
	bvhmat4& operator += ( const bvhmat4& a ) { for (int i = 0; i < 16; i++) cell[i] += a.cell[i]; return *this; }
	float cell[16] = { 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1 };
};

#endif // TINYBVH_USE_CUSTOM_VECTOR_TYPES

struct ALIGNED( 32 ) bvhaabb
{
	bvhvec3 minBounds; uint32_t dummy1;
	bvhvec3 maxBounds; uint32_t dummy2;
};

struct bvhvec4slice
{
	bvhvec4slice() = default;
	bvhvec4slice( const bvhvec4* data, uint32_t count, uint32_t stride = sizeof( bvhvec4 ) );
	constexpr operator bool() const { return !!data; }
	const bvhvec4& operator [] ( size_t i ) const;
	const int8_t* data = nullptr;
	uint32_t count, stride;
};

// Math operations.
// Note: Since this header file is expected to be included in a source file
// of a separate project, the static keyword doesn't provide sufficient
// isolation; hence the tinybvh_ prefix.
inline bool tinybvh_isnan( float f )
{
	uint32_t i;
	std::memcpy( &i, &f, sizeof( i ) );
	return (i & 0x7F800000) == 0x7F800000 && (i & 0x007FFFFF) != 0;
}
inline float tinybvh_safercp( const float x ) { float r = 1 / x; if (tinybvh_isnan( r )) r = x < 0 ? (-BVH_FAR) : BVH_FAR; return r; }
inline bvhvec3 tinybvh_safercp( const bvhvec3 a ) { return bvhvec3( tinybvh_safercp( a.x ), tinybvh_safercp( a.y ), tinybvh_safercp( a.z ) ); }
inline bvhvec3 tinybvh_rcp( const bvhvec3 a ) { return tinybvh_safercp( a ); /* bvhvec3( 1.0f / a.x, 1.0f / a.y, 1.0f / a.z ); */ }
inline float tinybvh_sqrf( const float x ) { return x * x; }
inline float tinybvh_min( const float a, const float b ) { return a < b ? a : b; }
inline float tinybvh_max( const float a, const float b ) { return a > b ? a : b; }
inline double tinybvh_min( const double a, const double b ) { return a < b ? a : b; }
inline double tinybvh_max( const double a, const double b ) { return a > b ? a : b; }
inline int32_t tinybvh_min( const int32_t a, const int32_t b ) { return a < b ? a : b; }
inline int32_t tinybvh_max( const int32_t a, const int32_t b ) { return a > b ? a : b; }
inline uint32_t tinybvh_min( const uint32_t a, const uint32_t b ) { return a < b ? a : b; }
inline uint32_t tinybvh_max( const uint32_t a, const uint32_t b ) { return a > b ? a : b; }
inline bvhvec3 tinybvh_min( const bvhvec3& a, const bvhvec3& b ) { return bvhvec3( tinybvh_min( a.x, b.x ), tinybvh_min( a.y, b.y ), tinybvh_min( a.z, b.z ) ); }
inline bvhvec4 tinybvh_min( const bvhvec4& a, const bvhvec4& b ) { return bvhvec4( tinybvh_min( a.x, b.x ), tinybvh_min( a.y, b.y ), tinybvh_min( a.z, b.z ), tinybvh_min( a.w, b.w ) ); }
inline bvhvec3 tinybvh_max( const bvhvec3& a, const bvhvec3& b ) { return bvhvec3( tinybvh_max( a.x, b.x ), tinybvh_max( a.y, b.y ), tinybvh_max( a.z, b.z ) ); }
inline bvhvec4 tinybvh_max( const bvhvec4& a, const bvhvec4& b ) { return bvhvec4( tinybvh_max( a.x, b.x ), tinybvh_max( a.y, b.y ), tinybvh_max( a.z, b.z ), tinybvh_max( a.w, b.w ) ); }
inline float tinybvh_clamp( const float x, const float a, const float b ) { return x > a ? (x < b ? x : b) : a; /* NaN safe */ }
inline int32_t tinybvh_clamp( const int32_t x, const int32_t a, const int32_t b ) { return x > a ? (x < b ? x : b) : a; /* NaN safe */ }
template <class T> inline static void tinybvh_swap( T& a, T& b ) { T t = a; a = b; b = t; }
inline float tinybvh_halfarea( const bvhvec3& v ) { return v.x < -BVH_FAR ? 0 : (v.x * v.y + v.y * v.z + v.z * v.x); } // for SAH calculations
inline uint32_t tinybvh_maxdim( const bvhvec3& v ) { uint32_t r = fabs( v.x ) > fabs( v.y ) ? 0 : 1; return fabs( v.z ) > fabs( v[r] ) ? 2 : r; }

// Operator overloads.
// Only a minimal set is provided.
#ifndef TINYBVH_USE_CUSTOM_VECTOR_TYPES

inline bvhvec2 operator-( const bvhvec2& a ) { return bvhvec2( -a.x, -a.y ); }
inline bvhvec3 operator-( const bvhvec3& a ) { return bvhvec3( -a.x, -a.y, -a.z ); }
inline bvhvec4 operator-( const bvhvec4& a ) { return bvhvec4( -a.x, -a.y, -a.z, -a.w ); }
inline bvhvec2 operator+( const bvhvec2& a, const bvhvec2& b ) { return bvhvec2( a.x + b.x, a.y + b.y ); }
inline bvhvec3 operator+( const bvhvec3& a, const bvhvec3& b ) { return bvhvec3( a.x + b.x, a.y + b.y, a.z + b.z ); }
inline bvhvec4 operator+( const bvhvec4& a, const bvhvec4& b ) { return bvhvec4( a.x + b.x, a.y + b.y, a.z + b.z, a.w + b.w ); }
inline bvhvec4 operator+( const bvhvec4& a, const bvhvec3& b ) { return bvhvec4( a.x + b.x, a.y + b.y, a.z + b.z, a.w ); }
inline bvhvec2 operator-( const bvhvec2& a, const bvhvec2& b ) { return bvhvec2( a.x - b.x, a.y - b.y ); }
inline bvhvec3 operator-( const bvhvec3& a, const bvhvec3& b ) { return bvhvec3( a.x - b.x, a.y - b.y, a.z - b.z ); }
inline bvhvec4 operator-( const bvhvec4& a, const bvhvec4& b ) { return bvhvec4( a.x - b.x, a.y - b.y, a.z - b.z, a.w - b.w ); }
inline void operator+=( bvhvec2& a, const bvhvec2& b ) { a.x += b.x; a.y += b.y; }
inline void operator+=( bvhvec3& a, const bvhvec3& b ) { a.x += b.x; a.y += b.y; a.z += b.z; }
inline void operator+=( bvhvec4& a, const bvhvec4& b ) { a.x += b.x; a.y += b.y; a.z += b.z; a.w += b.w; }
inline bvhvec2 operator*( const bvhvec2& a, const bvhvec2& b ) { return bvhvec2( a.x * b.x, a.y * b.y ); }
inline bvhvec3 operator*( const bvhvec3& a, const bvhvec3& b ) { return bvhvec3( a.x * b.x, a.y * b.y, a.z * b.z ); }
inline bvhvec4 operator*( const bvhvec4& a, const bvhvec4& b ) { return bvhvec4( a.x * b.x, a.y * b.y, a.z * b.z, a.w * b.w ); }
inline bvhvec2 operator*( const bvhvec2& a, float b ) { return bvhvec2( a.x * b, a.y * b ); }
inline bvhvec3 operator*( const bvhvec3& a, float b ) { return bvhvec3( a.x * b, a.y * b, a.z * b ); }
inline bvhvec4 operator*( const bvhvec4& a, float b ) { return bvhvec4( a.x * b, a.y * b, a.z * b, a.w * b ); }
inline bvhvec2 operator*( float b, const bvhvec2& a ) { return bvhvec2( b * a.x, b * a.y ); }
inline bvhvec3 operator*( float b, const bvhvec3& a ) { return bvhvec3( b * a.x, b * a.y, b * a.z ); }
inline bvhvec4 operator*( float b, const bvhvec4& a ) { return bvhvec4( b * a.x, b * a.y, b * a.z, b * a.w ); }
inline bvhvec2 operator/( float b, const bvhvec2& a ) { return bvhvec2( b / a.x, b / a.y ); }
inline bvhvec3 operator/( float b, const bvhvec3& a ) { return bvhvec3( b / a.x, b / a.y, b / a.z ); }
inline bvhvec4 operator/( float b, const bvhvec4& a ) { return bvhvec4( b / a.x, b / a.y, b / a.z, b / a.w ); }
inline bvhvec3 operator/( bvhvec3 b, const bvhvec3& a ) { return bvhvec3( b.x / a.x, b.y / a.y, b.z / a.z ); }
inline void operator*=( bvhvec3& a, const float b ) { a.x *= b; a.y *= b; a.z *= b; }

#endif // TINYBVH_USE_CUSTOM_VECTOR_TYPES

// Vector math: cross and dot.
inline bvhvec3 tinybvh_cross( const bvhvec3& a, const bvhvec3& b )
{
	return bvhvec3( a.y * b.z - a.z * b.y, a.z * b.x - a.x * b.z, a.x * b.y - a.y * b.x );
}
inline float tinybvh_dot( const bvhvec2& a, const bvhvec2& b ) { return a.x * b.x + a.y * b.y; }
inline float tinybvh_dot( const bvhvec3& a, const bvhvec3& b ) { return a.x * b.x + a.y * b.y + a.z * b.z; }
inline float tinybvh_dot( const bvhvec4& a, const bvhvec4& b ) { return a.x * b.x + a.y * b.y + a.z * b.z + a.w * b.w; }

// Vector math: common operations.
inline float tinybvh_length( const bvhvec3& a ) { return sqrtf( a.x * a.x + a.y * a.y + a.z * a.z ); }
inline bvhvec3 tinybvh_normalize( const bvhvec3& a )
{
	float l = tinybvh_length( a ), rl = l == 0 ? 0 : (1.0f / l);
	return a * rl;
}
inline bvhvec3 tinybvh_transform_point( const bvhvec3& v, const bvhmat4& T )
{
	const float* Tcell = (const float*)&T;
	const bvhvec3 res(
		Tcell[0] * v.x + Tcell[1] * v.y + Tcell[2] * v.z + Tcell[3],
		Tcell[4] * v.x + Tcell[5] * v.y + Tcell[6] * v.z + Tcell[7],
		Tcell[8] * v.x + Tcell[9] * v.y + Tcell[10] * v.z + Tcell[11] );
	const float w = Tcell[12] * v.x + Tcell[13] * v.y + Tcell[14] * v.z + Tcell[15];
	if (w == 1) return res; else return res * (1.f / w);
}
inline bvhvec3 tinybvh_transform_vector( const bvhvec3& v, const bvhmat4& T )
{
	const float* Tcell = (const float*)&T;
	return bvhvec3( Tcell[0] * v.x + Tcell[1] * v.y + Tcell[2] * v.z, Tcell[4] * v.x +
		Tcell[5] * v.y + Tcell[6] * v.z, Tcell[8] * v.x + Tcell[9] * v.y + Tcell[10] * v.z );
}

#ifdef DOUBLE_PRECISION_SUPPORT
// Double-precision math

#ifndef TINYBVH_USE_CUSTOM_VECTOR_TYPES

struct bvhdbl3
{
	bvhdbl3() = default;
	constexpr bvhdbl3( const double a, const double b, const double c ) : x( a ), y( b ), z( c ) {}
	constexpr bvhdbl3( const double a ) : x( a ), y( a ), z( a ) {}
	bvhdbl3( const bvhvec3 a ) : x( (double)a.x ), y( (double)a.y ), z( (double)a.z ) {}
	constexpr double& operator [] ( const int32_t i ) { return cell[i]; }
	const double& operator [] ( const int32_t i ) const { return cell[i]; }
	union { struct { double x, y, z; }; double cell[3]; };
};

#endif // TINYBVH_USE_CUSTOM_VECTOR_TYPES

#ifdef _MSC_VER
#pragma warning ( pop )
#endif

inline bvhdbl3 tinybvh_min( const bvhdbl3& a, const bvhdbl3& b ) { return bvhdbl3( tinybvh_min( a.x, b.x ), tinybvh_min( a.y, b.y ), tinybvh_min( a.z, b.z ) ); }
inline bvhdbl3 tinybvh_max( const bvhdbl3& a, const bvhdbl3& b ) { return bvhdbl3( tinybvh_max( a.x, b.x ), tinybvh_max( a.y, b.y ), tinybvh_max( a.z, b.z ) ); }

#ifndef TINYBVH_USE_CUSTOM_VECTOR_TYPES

constexpr bvhdbl3 operator-( const bvhdbl3& a ) { return bvhdbl3( -a.x, -a.y, -a.z ); }
constexpr bvhdbl3 operator+( const bvhdbl3& a, const bvhdbl3& b ) { return bvhdbl3( a.x + b.x, a.y + b.y, a.z + b.z ); }
constexpr bvhdbl3 operator-( const bvhdbl3& a, const bvhdbl3& b ) { return bvhdbl3( a.x - b.x, a.y - b.y, a.z - b.z ); }
constexpr void operator+=( bvhdbl3& a, const bvhdbl3& b ) { a.x += b.x; a.y += b.y; a.z += b.z; }
constexpr bvhdbl3 operator*( const bvhdbl3& a, const bvhdbl3& b ) { return bvhdbl3( a.x * b.x, a.y * b.y, a.z * b.z ); }
constexpr bvhdbl3 operator*( const bvhdbl3& a, double b ) { return bvhdbl3( a.x * b, a.y * b, a.z * b ); }
constexpr bvhdbl3 operator*( double b, const bvhdbl3& a ) { return bvhdbl3( b * a.x, b * a.y, b * a.z ); }
constexpr bvhdbl3 operator/( double b, const bvhdbl3& a ) { return bvhdbl3( b / a.x, b / a.y, b / a.z ); }
constexpr bvhdbl3 operator/( bvhdbl3 b, const bvhdbl3& a ) { return bvhdbl3( b.x / a.x, b.y / a.y, b.z / a.z ); }
constexpr bvhdbl3 operator*=( bvhdbl3& a, const double b ) { return bvhdbl3( a.x * b, a.y * b, a.z * b ); }

#endif // TINYBVH_USE_CUSTOM_VECTOR_TYPES

inline double tinybvh_length( const bvhdbl3& a ) { return sqrt( a.x * a.x + a.y * a.y + a.z * a.z ); }
inline bvhdbl3 tinybvh_normalize( const bvhdbl3& a )
{
	double l = tinybvh_length( a ), rl = l == 0 ? 0 : (1.0 / l);
	return a * rl;
}
inline bvhdbl3 tinybvh_transform_point( const bvhdbl3& v, const double* T )
{
	const bvhdbl3 res(
		T[0] * v.x + T[1] * v.y + T[2] * v.z + T[3],
		T[4] * v.x + T[5] * v.y + T[6] * v.z + T[7],
		T[8] * v.x + T[9] * v.y + T[10] * v.z + T[11] );
	const double w = T[12] * v.x + T[13] * v.y + T[14] * v.z + T[15];
	if (w == 1) return res; else return res * (1. / w);
}
inline bvhdbl3 tinybvh_transform_vector( const bvhdbl3& v, const double* T )
{
	return bvhdbl3( T[0] * v.x + T[1] * v.y + T[2] * v.z, T[4] * v.x +
		T[5] * v.y + T[6] * v.z, T[8] * v.x + T[9] * v.y + T[10] * v.z );
}

inline bvhdbl3 tinybvh_cross( const bvhdbl3& a, const bvhdbl3& b )
{
	return bvhdbl3( a.y * b.z - a.z * b.y, a.z * b.x - a.x * b.z, a.x * b.y - a.y * b.x );
}
inline double tinybvh_dot( const bvhdbl3& a, const bvhdbl3& b ) { return a.x * b.x + a.y * b.y + a.z * b.z; }

inline double tinybvh_halfarea( const bvhdbl3& v ) { return v.x < -BVH_FAR ? 0 : (v.x * v.y + v.y * v.z + v.z * v.x); } // for SAH calculations

#endif // DOUBLE_PRECISION_SUPPORT

// SIMD typedef, helps keeping the interface generic
#if defined BVH_USESSE
typedef __m128 SIMDVEC4;
typedef __m128i SIMDIVEC4;
#define SIMD_SETVEC(a,b,c,d) _mm_set_ps( a, b, c, d )
#define SIMD_SETRVEC(a,b,c,d) _mm_set_ps( d, c, b, a )
#elif defined BVH_USENEON
typedef float32x4_t SIMDVEC4;
typedef int32x4_t SIMDIVEC4;
inline float32x4_t SIMD_SETVEC( float w, float z, float y, float x )
{
	ALIGNED( 64 ) float data[4] = { x, y, z, w };
	return vld1q_f32( data );
}
inline float32x4_t SIMD_SETRVEC( float x, float y, float z, float w )
{
	ALIGNED( 64 ) float data[4] = { x, y, z, w };
	return vld1q_f32( data );
}
inline uint32x4_t SIMD_SETRVECU( uint32_t x, uint32_t y, uint32_t z, uint32_t w )
{
	ALIGNED( 64 ) uint32_t data[4] = { x, y, z, w };
	return vld1q_u32( data );
}
inline int32x4_t SIMD_SETRVECS( int32_t x, int32_t y, int32_t z, int32_t w )
{
	ALIGNED( 64 ) int32_t data[4] = { x, y, z, w };
	return vld1q_s32( data );
}
#else
typedef bvhvec4 SIMDVEC4;
typedef struct { int x, y, z, w; } SIMDIVEC4;
#define SIMD_SETVEC(a,b,c,d) bvhvec4( d, c, b, a )
#define SIMD_SETRVEC(a,b,c,d) bvhvec4( a, b, c, d )
#endif
#ifdef BVH_USEAVX
typedef __m256 SIMDVEC8;
typedef __m256i SIMDIVEC8;
#elif defined BVH_USENEON
typedef float32x4x2_t SIMDVEC8;
typedef int32x4x2_t SIMDIVEC8;
#else
typedef struct { float v0, v1, v2, v3, v4, v5, v6, v7; } SIMDVEC8;
typedef struct { int v0, v1, v2, v3, v4, v5, v6, v7; } SIMDIVEC8;
#endif

// ============================================================================
//
//        T I N Y _ B V H   I N T E R F A C E
//
// ============================================================================

#if defined _MSC_VER || defined __GNUC__
#pragma pack(push, 4) // is there a good alternative for Clang / EMSCRIPTEN?
#endif
struct Intersection
{
	// An intersection result is designed to fit in no more than
	// four 32-bit values. This allows efficient storage of a result in
	// GPU code. The obvious missing result is an instance id; consider
	// squeezing this in the 'prim' field in some way.
	// Using this data and the original triangle data, all other info for
	// shading (such as normal, texture color etc.) can be reconstructed.
#if INST_IDX_BITS == 32
	uint32_t inst;	// instance index. Stored in top bits of prim if INST_IDX_BITS != 32.
#endif
	float t, u, v;	// distance along ray & barycentric coordinates of the intersection
	uint32_t prim;	// primitive index
	// 64 byte of custom data -
	// assuming struct Ray is aligned, this starts at a cache line boundary.
	void* auxData;
	union
	{
		unsigned char userChar[56];
		float userFloat[14];
		uint32_t userInt32[14];
		double userDouble[7];
		uint64_t userInt64[7];
	};
};
#if defined _MSC_VER || defined __GNUC__
#pragma pack(pop) // is there a good alternative for Clang / EMSCRIPTEN?
#endif

// 16 bits of mask to tell which BLASInstance to intersect or not.
// By default the mask will be initialized to intersect all instances.
#define RAY_MASK_INTERSECT_ALL 0xFFFF

struct ALIGNED( 64 ) Ray
{
	// Basic ray class. Note: For single blas traversal it is expected
	// that Ray::rD is properly initialized. For tlas/blas traversal this
	// field is typically updated for each blas.
	Ray() = default;
	Ray( bvhvec3 origin, bvhvec3 direction, float t = BVH_FAR, uint32_t rayMask = RAY_MASK_INTERSECT_ALL )
	{
		memset( this, 0, sizeof( Ray ) );
		O = origin, D = tinybvh_normalize( direction ), rD = tinybvh_rcp( D );
		hit.t = t;
		mask = rayMask & RAY_MASK_INTERSECT_ALL;
	}
	ALIGNED( 16 ) bvhvec3 O; uint32_t mask = RAY_MASK_INTERSECT_ALL;
	ALIGNED( 16 ) bvhvec3 D; uint32_t instIdx = 0;
	ALIGNED( 16 ) bvhvec3 rD;
#if INST_IDX_BITS != 32
	uint32_t dummy2; // align to 16 bytes if field 'hit' is 16 bytes; otherwise don't.
#endif
	Intersection hit;
};

inline float tinybvh_intersect_aabb( Ray& ray, const bvhvec3& aabbMin, const bvhvec3& aabbMax )
{
	// "slab test" ray/AABB intersection
	float tx1 = (aabbMin.x - ray.O.x) * ray.rD.x, tx2 = (aabbMax.x - ray.O.x) * ray.rD.x;
	float tmin = tinybvh_min( tx1, tx2 ), tmax = tinybvh_max( tx1, tx2 );
	float ty1 = (aabbMin.y - ray.O.y) * ray.rD.y, ty2 = (aabbMax.y - ray.O.y) * ray.rD.y;
	tmin = tinybvh_max( tmin, tinybvh_min( ty1, ty2 ) );
	tmax = tinybvh_min( tmax, tinybvh_max( ty1, ty2 ) );
	float tz1 = (aabbMin.z - ray.O.z) * ray.rD.z, tz2 = (aabbMax.z - ray.O.z) * ray.rD.z;
	tmin = tinybvh_max( tmin, tinybvh_min( tz1, tz2 ) );
	tmax = tinybvh_min( tmax, tinybvh_max( tz1, tz2 ) );
	if (tmax >= tmin && tmin < ray.hit.t && tmax >= 0) return tmin; else return BVH_FAR;
}

inline bool tinybvh_aabbs_overlap( const bvhvec3& bmin1, const bvhvec3& bmax1, const bvhvec3& bmin2, const bvhvec3& bmax2 )
{
	return bmin1.x <= bmax2.x && bmax1.x >= bmin2.x && bmin1.y <= bmax2.y &&
		bmax1.y >= bmin2.y && bmin1.z <= bmax2.z && bmax1.z >= bmin2.z;
}

#ifdef DOUBLE_PRECISION_SUPPORT

struct IntersectionEx
{
	// Double-precision hit record.
	double t, u, v;	// distance along ray & barycentric coordinates of the intersection
	uint64_t inst, prim; // instance and primitive index
};

struct RayEx
{
	// Double-precision ray definition.
	RayEx() = default;
	RayEx( bvhdbl3 origin, bvhdbl3 direction, double tmax = BVH_DBL_FAR, uint32_t rayMask = RAY_MASK_INTERSECT_ALL )
	{
		memset( this, 0, sizeof( RayEx ) );
		O = origin, D = direction;
		double rl = 1.0 / sqrt( D.x * D.x + D.y * D.y + D.z * D.z );
		D.x *= rl, D.y *= rl, D.z *= rl;
		rD.x = 1.0 / D.x, rD.y = 1.0 / D.y, rD.z = 1.0 / D.z;
		hit.u = hit.v = 0, hit.t = tmax;
		instIdx = 0;
		mask = rayMask & RAY_MASK_INTERSECT_ALL;
	}
	bvhdbl3 O, D, rD;
	IntersectionEx hit;
	uint64_t instIdx;
	uint64_t mask;
};

#endif

struct BVHContext
{
	void* (*malloc)(size_t size, void* userdata) = malloc64;
	void (*free)(void* ptr, void* userdata) = free64;
	void* userdata = nullptr;
};

class JobSystem;
class BVHBase
{
public:
	enum BVHType : uint32_t
	{
		// Every BVH class is derived from BVHBase, but we don't use virtual functions, for
		// performance reasons. For a TLAS over a mix of BVH layouts we do however need this
		// kind of behavior when transitioning from a TLAS leaf to a BLAS root node.
		UNDEFINED = 0,
		LAYOUT_BVH = 1,
		LAYOUT_BVH_VERBOSE,
		LAYOUT_BVH_DOUBLE,
		LAYOUT_BVH_SOA,
		LAYOUT_BVH_GPU,
		LAYOUT_MBVH,
		LAYOUT_BVH4_CPU,
		LAYOUT_BVH4_GPU,
		LAYOUT_CWBVH,
		LAYOUT_BVH8_AVX2,
		LAYOUT_VOXELSET
	};
	struct ALIGNED( 32 ) Fragment
	{
		// A fragment stores the bounds of an input primitive. The name 'Fragment' is from
		// "Parallel Spatial Splits in Bounding Volume Hierarchies", 2016, Fuetterling et al.,
		// and refers to the potential splitting of these boxes for SBVH construction.
		bvhvec3 bmin;				// AABB min x, y and z
		uint32_t primIdx;			// index of the original primitive
		bvhvec3 bmax;				// AABB max x, y and z
		uint32_t clipped = 0;		// Fragment is the result of clipping if > 0.
		void Extend( bvhvec3 p ) { bmin = tinybvh_min( p, bmin ); bmax = tinybvh_max( p, bmax ); }
	};
	// BVH flags, maintainted by tiny_bvh.
	bool rebuildable = true;		// rebuilds are safe only if a tree has not been converted.
	bool refittable = true;			// refits are safe only if the tree has no spatial splits.
	bool may_have_holes = false;	// threaded builds and MergeLeafs produce BVHs with unused nodes.
	bool bvh_over_aabbs = false;	// a BVH over AABBs is useful for e.g. TLAS traversal.
	bool bvh_over_indices = false;	// a BVH over indices cannot translate primitive index to vertex index.
	BVHContext context;				// context used to provide user-defined allocation functions.
	BVHType layout = UNDEFINED;		// BVH layout identifier.
	// Keep track of allocated buffer size to avoid repeated allocation during layout conversion.
	uint32_t allocatedNodes = 0;	// number of nodes allocated for the BVH.
	uint32_t usedNodes = 0;			// number of nodes used for the BVH.
	uint32_t triCount = 0;			// number of primitives in the BVH.
	uint32_t idxCount = 0;			// number of primitive indices; can exceed triCount for SBVH.
	float c_trav = C_TRAV;			// cost of a traversal step, used to steer SAH construction.
	float c_int = C_INT;			// cost of a primitive intersection, used to steer SAH construction.
	bool l_quads = false;			// some layouts have 4 prims in each leaf; adjust SAH cost for this.
	uint32_t hqbvhbins = HQBVHBINS;	// number of bins to use in SBVH construction.
	bool hqbvhoddeven = false;		// if true, odd levels will use one extra bin during construction.
	bvhvec3 aabbMin, aabbMax;		// bounds of the root node of the BVH.
	// Opacity maps support.
	uint32_t opmapN = 0;			// opacity micro map subdivision: 0 = no maps.
	uint32_t* opmap = 0;			// opacity micro maps; opmapN^2 bits per triangle.
	bool hasOpacityMicroMaps() const { return opmapN > 0; }
	void SetOpacityMicroMaps( uint32_t* mapData, uint32_t N ) { opmap = mapData, opmapN = N; }
	// Custom memory allocation
	void* AlignedAlloc( size_t size );
	void AlignedFree( void* ptr );
	// Common methods
	void CopyBasePropertiesFrom( const BVHBase& original );	// copy flags from one BVH to another
protected:
	~BVHBase() {}
	__FORCEINLINE void IntersectTri( Ray& ray, const uint32_t idx, const bvhvec4slice& verts, const uint32_t i0, const uint32_t i1, const uint32_t i2 ) const;
	__FORCEINLINE bool TriOccludes( const Ray& ray, const bvhvec4slice& verts, const uint32_t triIdx, const uint32_t i0, const uint32_t i1, const uint32_t i2 ) const;
	static void PrecomputeTriangle( const bvhvec4slice& vert, const uint32_t ti0, const uint32_t ti1, const uint32_t ti2, float* T );
	static float SA( const bvhvec3& aabbMin, const bvhvec3& aabbMax );
};

class BLASInstance;
class BVH_Verbose;
class BVH : public BVHBase
{
public:
	friend class BVH_GPU;
	friend class BVH_SoA;
	friend class BVH4_CPU;
	friend class BVH8_CPU;
	friend class BVH8_CWBVH;
	template <int M> friend class MBVH;
	struct SubdivTask { uint32_t node, sliceStart, sliceEnd, depth; };
	enum BuildFlags : uint32_t
	{
		NONE = 0,			// Default building behavior (binned, SAH-driven).
		FULLSPLIT = 1		// Split as far as possible, even when SAH doesn't agree.
	};
	struct BVHNode
	{
		// 'Traditional' 32-byte BVH node layout, as proposed by Ingo Wald.
		// When aligned to a cache line boundary, two of these fit together.
		bvhvec3 aabbMin; uint32_t leftFirst; // 16 bytes
		bvhvec3 aabbMax; uint32_t triCount;	// 16 bytes, total: 32 bytes
		bool isLeaf() const { return triCount > 0; /* empty BVH leaves do not exist */ }
		bool Intersect( const bvhvec3& bmin, const bvhvec3& bmax ) const;
		float SurfaceArea() const { return BVH::SA( aabbMin, aabbMax ); }
	};
	BVH( BVHContext ctx = {} ) { layout = LAYOUT_BVH; context = ctx; }
	BVH( const BVH_Verbose& original ) { layout = LAYOUT_BVH; ConvertFrom( original ); }
	BVH( const bvhvec4* vertices, const uint32_t primCount ) { layout = LAYOUT_BVH; Build( vertices, primCount ); }
	BVH( const bvhvec4slice& vertices ) { layout = LAYOUT_BVH; Build( vertices ); }
	BVH( BVH&& ) noexcept;
	BVH& operator=( const BVH& other ) = default;
	~BVH();
	void ConvertFrom( const BVH_Verbose& original, bool compact = true );
	void SplitLeafs( const uint32_t maxPrims );
	float SAHCost( const uint32_t nodeIdx = 0 ) const;
	float EPOCost( const uint32_t nodeIdx = 0 ) const;
	int32_t NodeCount() const;
	int32_t LeafCount() const;
	int32_t PrimCount( const uint32_t nodeIdx = 0 ) const;
	void Compact();
	void Save( const char* fileName );
	bool Load( const char* fileName, const bvhvec4* vertices, const uint32_t primCount );
	bool Load( const char* fileName, const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	bool Load( const char* fileName, const bvhvec4slice& vertices, const uint32_t* indices = 0, const uint32_t primCount = 0 );
	void BuildQuick( const bvhvec4* vertices, const uint32_t primCount );
	void BuildQuick( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void BuildAABB( const bvhvec4* aabbs, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( BLASInstance* instances, const uint32_t instCount, BVHBase** blasses, const uint32_t blasCount );
	void Build( void (*customGetAABB)(const unsigned, bvhvec3&, bvhvec3&), const uint32_t primCount );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildAVX( const bvhvec4* vertices, const uint32_t primCount );
	void BuildAVX( const bvhvec4slice& vertices );
	void BuildAVX( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildAVX( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
#ifdef BVH_USENEON
	void BuildNEON( const bvhvec4* vertices, const uint32_t primCount );
	void BuildNEON( const bvhvec4slice& vertices );
	void BuildNEON( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildNEON( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void PrepareNEONBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildNEON();
#endif
	void Refit( const uint32_t nodeIdx = 0 );
	void Optimize( const uint32_t iterations = 25, bool extreme = false, bool stochastic = false );
	uint32_t CombineLeafs( const uint32_t primCount, uint32_t& firstIdx, uint32_t nodeIdx = 0 );
	void CombineLeafs( const uint32_t nodeIdx = 0 );
	int32_t Intersect( Ray& ray ) const;
	bool IntersectSphere( const bvhvec3& pos, const float r ) const;
	bool IsOccluded( const Ray& ray ) const;
	void Intersect256Rays( Ray* first ) const;
	void Intersect256RaysSSE( Ray* packet ) const; // requires BVH_USEAVX
	// private:
	void PrepareBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( uint32_t nodeIdx = 0, uint32_t depth = 0 );
	void BuildFullSweep( uint32_t nodeIdx = 0, uint32_t depth = 0 );
	bool IsOccludedTLAS( const Ray& ray ) const;
	int32_t IntersectTLAS( Ray& ray ) const;
	void PrepareAVXBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildAVXSubtree( uint32_t nodeIdx = 0, uint32_t depth = 0 );
	void PrepareHQBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t prims );
	void BuildHQ();
	void BuildHQTask( uint32_t nodeIdx, uint32_t depth, uint32_t sliceStart, uint32_t sliceEnd, uint32_t* triIdxB );
	bool ClipFrag( const Fragment& orig, Fragment& newFrag, bvhvec3 bmin, bvhvec3 bmax, const uint32_t splitAxis ) const;
	bool SplitFrag( const Fragment& orig, Fragment& left, Fragment& right, const uint32_t splitAxis, const float splitPos ) const;
protected:
	template <bool posX, bool posY, bool posZ> int32_t Intersect( Ray& ray ) const;
	template <bool posX, bool posY, bool posZ> int32_t IntersectTLAS( Ray& ray ) const;
	template <bool posX, bool posY, bool posZ> bool IsOccluded( const Ray& ray ) const;
	template <bool posX, bool posY, bool posZ> bool IsOccludedTLAS( const Ray& ray ) const;
	void BuildDefault( const bvhvec4* vertices, const uint32_t primCount );
	void BuildDefault( const bvhvec4slice& vertices );
	void BuildDefault( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildDefault( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	// Helpers
	inline float SplitCostSAH( const float rAparent, const float Aleft, const int Nleft, const float Aright, const int Nright ) const;
	inline float NoSplitCostSAH( const int Nparent ) const;
	float EPOArea( const uint32_t subtreeRoot, const uint32_t nodeIdx = 0 ) const;
	float PrimArea( const uint32_t p ) const;
	float SplitPriority( const Fragment& f ) const;
	int SplitCount( const float prio, const float sumPrio, const int triCount, const float factor ) const;
	uint32_t Presplit();
	static float GetNodeSize( const float extent, const float globalSize );
public:
	// BVH type identification
	bool isTLAS() const { return instList != 0; }
	bool isBLAS() const { return instList == 0; }
	bool isIndexed() const { return vertIdx != 0; }
	bool hasCustomGeom() const { return customIntersect != 0; }
	// Basic BVH data
	bvhvec4slice verts = {};		// pointer to input primitive array: 3x16 bytes per tri.
	uint32_t* vertIdx = 0;			// vertex indices, only used in case the BVH is built over indexed prims.
	uint32_t* primIdx = 0;			// primitive index array.
	uint32_t* rrsHits = 0;			// for RDH: ray hit count per triangle.
	BLASInstance* instList = 0;		// instance array, for top-level acceleration structure.
	BVHBase** blasList = 0;			// blas array, for TLAS traversal.
	uint32_t blasCount = 0;			// number of blasses in blasList.
	BVHNode* bvhNode = 0;			// BVH node pool, Wald 32-byte format. Root is always in node 0.
	uint32_t newNodePtr = 0;		// used during build to keep track of next free node in pool.
	uint32_t nextFrag = 0;			// used during SBVH build to keep track of next free fragment.
	Fragment* fragment = 0;			// input primitive bounding boxes.
	bool usePresplitting = false;	// pre-split triangles before building the BVH.
	float presplitFactor = 0.3f;	// presplit budget relative to input data size.
	bool useFullSweep = false;		// for experiments only; full-sweep SAH builder.
	bool threadedBuild = true;		// will be disabled for small meshes.
	// Custom geometry intersection callback
	bool (*customIntersect)(Ray&, const unsigned) = 0;
	bool (*customIsOccluded)(const Ray&, const unsigned) = 0;
private:
	// Atomic counters for threaded builds
	std::atomic<uint32_t>* atomicNewNodePtr = 0;
	std::atomic<uint32_t>* atomicNextFrag = 0;
	// job system for parallel builds
	JobSystem* subtreeJobs = 0;
	JobSystem* binningJobs = 0;
	// data for full sweep builder
	uint8_t* flag = 0;
	uint32_t* sortedIdx[3] = { 0 };
	float* SARs = 0;
#ifdef BVH_USESSE
	static __m128 half4, two4, min1, mask3, binmul3;
	static __m128i maxbin4;
#endif
#ifdef BVH_USEAVX
	// static AVX data members
	static __m256 max8, mask6, signFlip8;
public:
	// helper for AVX binning
	void BuildAVXBinTask( const uint32_t first, const uint32_t last, __m256* binbox, __m256* orig,
		uint32_t* count, const __m128& nmin4, const __m128& rpd4 );
#endif
};

class VoxelSet : public BVHBase // just so it can be attached conveniently in a TLAS
{
public:
	struct DDAState
	{
		uint32_t X, Y, Z;		// 12 bytes
		float dummy1 = 0;		// 16 bytes
		bvhvec3 tmax;
		float dummy2 = 0;		// 16 values, 64 bytes in total
	};
	VoxelSet();
	void Set( const uint32_t x, const uint32_t y, const uint32_t z, const uint32_t v );
	void UpdateTopGrid();
	bvhvec3 GetNormal( const Ray& ray ) const;
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const;
private:
	bool Setup3DDDA( const Ray& ray, const bvhvec3& Dsign, DDAState& state, const bvhint3& step, bvhvec3& tdelta, float& t ) const;
	// lowest level: 1 32-bit value per voxel
public:
	static constexpr int objectDim = 256; // 64, 128 or 256.
private:
	static constexpr int objectSize = objectDim * objectDim * objectDim;
	// grid level: collection of bricks
	static constexpr int brickDim = 8;
	static constexpr int brickSize = brickDim * brickDim * brickDim;
	static constexpr int gridDim = objectDim / brickDim;
	static constexpr int gridSize = gridDim * gridDim * gridDim;
	// topgrid level: 1 bit for each group of bricks
	static constexpr int groupDim = 4;
	static constexpr int groupSize = groupDim * groupDim * groupDim;
	static constexpr int topGridDim = gridDim / groupDim;
	static constexpr int topGridSize = topGridDim * topGridDim * topGridDim;
	// masks
	static constexpr uint32_t topResMask = gridDim - groupDim;
	static constexpr uint32_t superMask = topResMask + topResMask * gridDim + topResMask * gridDim * gridDim;
	// non-static data
	uint32_t* grid = 0;
	uint32_t* brick = 0;
	uint32_t brickCount = (objectDim * objectDim) / 16; // will grow as needed; scales roughly quadratically with objectDim
	uint32_t freeBrickPtr = 1; // skip 1, as 0 denotes an empty brick in the topgrid.
	uint32_t* topGrid = 0;
};

#ifdef DOUBLE_PRECISION_SUPPORT

class BLASInstanceEx;
class BVH_Double : public BVHBase
{
public:
	struct BVHNode
	{
		// Double precision 'traditional' BVH node layout.
		// Compared to the default BVHNode, child node indices and triangle indices
		// are also expanded to 64bit values to support massive scenes.
		bvhdbl3 aabbMin, aabbMax; // 2x24 bytes
		uint64_t leftFirst; // 8 bytes
		uint64_t triCount; // 8 bytes, total: 64 bytes
		bool isLeaf() const { return triCount > 0; /* empty BVH leaves do not exist */ }
		double Intersect( const RayEx& ray ) const;
		double SurfaceArea() const;
	};
	struct Fragment
	{
		// Double-precision version of the fragment sruct.
		bvhdbl3 bmin, bmax;			// AABB
		uint64_t primIdx;			// index of the original primitive
	};
	BVH_Double( BVHContext ctx = {} ) { layout = LAYOUT_BVH_DOUBLE; context = ctx; }
	BVH_Double( BVH_Double&& );
	BVH_Double& operator=( const BVH_Double& ) = default;
	~BVH_Double();
	void Build( const bvhdbl3* vertices, const uint64_t primCount );
	void Build( BLASInstanceEx* bvhs, const uint64_t instCount, BVH_Double** blasses, const uint64_t blasCount );
	void Build( void (*customGetAABB)(const uint64_t, bvhdbl3&, bvhdbl3&), const uint64_t primCount );
	void PrepareBuild( const bvhdbl3* vertices, const uint64_t primCount );
	void Build( uint64_t nodeIdx = 0, uint32_t depth = 0 );
	double SAHCost( const uint64_t nodeIdx = 0 ) const;
	int32_t Intersect( RayEx& ray ) const;
	bool IsOccluded( const RayEx& ray ) const;
	bool IsOccludedTLAS( const RayEx& ray ) const;
	int32_t IntersectTLAS( RayEx& ray ) const;
	bvhdbl3* verts = 0;				// pointer to input primitive array, double-precision, 3x24 bytes per tri.
	Fragment* fragment = 0;			// input primitive bounding boxes, double-precision.
	BVHNode* bvhNode = 0;			// BVH node, double precision format.
	uint64_t* primIdx = 0;			// primitive index array for double-precision bvh.
	BLASInstanceEx* instList = 0;	// instance array, for top-level acceleration structure.
	BVH_Double** blasList = 0;		// blas array, for TLAS traversal.
	uint64_t blasCount = 0;			// number of blasses in blasList.
	// 64-bit base overrides
	uint64_t newNodePtr = 0;		// next free bvh pool entry to allocate
	uint64_t usedNodes = 0;			// number of nodes used for the BVH.
	uint64_t allocatedNodes = 0;	// number of nodes allocated for the BVH.
	uint64_t triCount = 0;			// number of primitives in the BVH.
	uint64_t idxCount = 0;			// number of primitive indices.
	bvhdbl3 aabbMin, aabbMax;		// bounds of the root node of the BVH.
	bool threadedBuild = true;		// will be disabled for small meshes.
	// Custom geometry intersection callback
	bool (*customIntersect)(RayEx&, uint64_t) = 0;
	bool (*customIsOccluded)(const RayEx&, uint64_t) = 0;
private:
	// Atomic counter for threaded builds
	std::atomic<uint64_t>* atomicNewNodePtr = 0;
	// job system for parallel builds
	JobSystem* subtreeJobs = 0;
	JobSystem* binningJobs = 0;
};

#endif // DOUBLE_PRECISION_SUPPORT

class BVH_GPU : public BVHBase
{
public:
	struct BVHNode
	{
		// Alternative 64-byte BVH node layout, which specifies the bounds of
		// the children rather than the node itself. This layout is used by
		// Aila and Laine in their seminal GPU ray tracing paper.
		bvhvec3 lmin; uint32_t left;
		bvhvec3 lmax; uint32_t right;
		bvhvec3 rmin; uint32_t triCount;
		bvhvec3 rmax; uint32_t firstTri; // total: 64 bytes
		bool isLeaf() const { return triCount > 0; }
	};
	BVH_GPU( BVHContext ctx = {} ) { layout = LAYOUT_BVH_GPU; context = ctx; }
	BVH_GPU( const BVH& original ) { /* DEPRICATED */ ConvertFrom( original ); }
	BVH_GPU( BVH_GPU&& );
	BVH_GPU& operator=( const BVH_GPU& ) = default;
	~BVH_GPU();
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( BLASInstance* instances, const uint32_t instCount, BVHBase** blasses, const uint32_t blasCount );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Optimize( const uint32_t iterations = 25, bool extreme = false );
	float SAHCost( const uint32_t nodeIdx = 0 ) const { return bvh.SAHCost( nodeIdx ); }
	void ConvertFrom( const BVH& original, bool compact = true );
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const { FALLBACK_SHADOW_QUERY( ray ); }
	// BVH data
	BVHNode* bvhNode = 0;			// BVH node in Aila & Laine format.
	BVH bvh;						// BVH4 is created from BVH and uses its data.
	bool ownBVH = true;				// False when ConvertFrom receives an external bvh.
};

class BVH_SoA : public BVHBase
{
public:
	struct BVHNode
	{
		// Second alternative 64-byte BVH node layout, same as BVHAilaLaine but
		// with child AABBs stored in SoA order.
		SIMDVEC4 xxxx, yyyy, zzzz;
		uint32_t left, right, triCount, firstTri; // total: 64 bytes
		bool isLeaf() const { return triCount > 0; }
	};
	BVH_SoA( BVHContext ctx = {} ) { layout = LAYOUT_BVH_SOA; context = ctx; }
	BVH_SoA( const BVH& original ) { /* DEPRICATED */ layout = LAYOUT_BVH_SOA; ConvertFrom( original ); }
	BVH_SoA( BVH_SoA&& );
	BVH_SoA& operator=( const BVH_SoA& ) = default;
	~BVH_SoA();
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Optimize( const uint32_t iterations = 25, bool extreme = false );
	float SAHCost( const uint32_t nodeIdx = 0 ) const { return bvh.SAHCost( nodeIdx ); }
	void Save( const char* fileName );
	bool Load( const char* fileName, const bvhvec4* vertices, const uint32_t primCount );
	bool Load( const char* fileName, const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	bool Load( const char* fileName, const bvhvec4slice& vertices, const uint32_t* indices = 0, const uint32_t primCount = 0 );
	void ConvertFrom( const BVH& original, bool compact = true );
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const;
	// BVH data
	BVHNode* bvhNode = 0;			// BVH node in 'structure of arrays' format.
	BVH bvh;						// BVH_SoA is created from BVH and uses its data.
	bool ownBVH = true;				// False when ConvertFrom receives an external bvh.
};

class BVH_Verbose : public BVHBase
{
public:
	struct BVHNode
	{
		// This node layout has some extra data per node: It stores left and right
		// child node indices explicitly, and stores the index of the parent node.
		// This format exists primarily for the BVH optimizer.
		bvhvec3 aabbMin; uint32_t left;
		bvhvec3 aabbMax; uint32_t right;
		uint32_t triCount, firstTri, parent;
		float dummy[5]; // total: 64 bytes.
		bool isLeaf() const { return triCount > 0; }
		float SA() const { return BVH::SA( aabbMin, aabbMax ); }
	};
	BVH_Verbose( BVHContext ctx = {} ) { layout = LAYOUT_BVH_VERBOSE; context = ctx; }
	BVH_Verbose( const BVH& original ) { /* DEPRECATED */ layout = LAYOUT_BVH_VERBOSE; ConvertFrom( original ); }
	BVH_Verbose( BVH_Verbose&& );
	BVH_Verbose& operator=( const BVH_Verbose& ) = default;
	~BVH_Verbose();
	void ConvertFrom( const BVH& original, bool compact = true );
	float SAHCost( const uint32_t nodeIdx = 0 ) const;
	int32_t NodeCount() const;
	int32_t PrimCount( const uint32_t nodeIdx = 0 ) const;
	void Refit( const uint32_t nodeIdx = 0, bool skipLeafs = false );
	void CheckFit( const uint32_t nodeIdx = 0, bool skipLeafs = false );
	void Compact();
	void SortIndices();
	void SplitLeafs( const uint32_t maxPrims = 1 );
	void MergeLeafs();
	void Optimize( const uint32_t iterations = 25, bool extreme = false, bool stochastic = false );
private:
	struct SortItem { uint32_t idx; float cost; };
	void RefitUp( uint32_t nodeIdx );
	float SAHCostUp( uint32_t nodeIdx ) const;
	uint32_t FindBestNewPosition( const uint32_t Lid ) const;
	uint32_t CountSubtreeTris( const uint32_t nodeIdx, uint32_t* counters );
	void MergeSubtree( const uint32_t nodeIdx, uint32_t* newIdx, uint32_t& newIdxPtr );
public:
	// BVH data
	bvhvec4slice verts = {};		// pointer to input primitive array: 3x16 bytes per tri.
	Fragment* fragment = 0;			// input primitive bounding boxes, double-precision.
	uint32_t* primIdx = 0;			// primitive index array - pointer copied from original.
	BVHNode* bvhNode = 0;			// BVH node with additional info, for BVH optimizer.
};

template <int M> class MBVH : public BVHBase
{
public:
	struct MBVHNode
	{
		// M-wide (aka 'shallow') BVH layout.
		bvhvec3 aabbMin; uint32_t firstTri;
		bvhvec3 aabbMax; uint32_t triCount;
		uint32_t child[M];
		uint32_t childCount;
		uint32_t dummy[((30 - M) & 3) + 1]; // dummies are for alignment.
		bool isLeaf() const { return triCount > 0; }
	};
	MBVH( BVHContext ctx = {} ) { layout = LAYOUT_MBVH; context = ctx; }
	MBVH( const BVH& original ) { /* DEPRECATED */ layout = LAYOUT_MBVH; ConvertFrom( original ); }
	MBVH( MBVH&& );
	MBVH& operator=( const MBVH& ) = default;
	~MBVH();
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Optimize( const uint32_t iterations = 25, bool extreme = false );
	void Refit( const uint32_t nodeIdx = 0 );
	uint32_t LeafCount( const uint32_t nodeIdx = 0 ) const;
	float SAHCost( const uint32_t nodeIdx = 0 ) const;
	void ConvertFrom( const BVH& original, bool compact = true );
	// BVH data
	MBVHNode* mbvhNode = 0;			// BVH node for M-wide BVH.
	BVH bvh;						// MBVH<M> is created from BVH and uses its data.
	bool ownBVH = true;				// False when ConvertFrom receives an external bvh.
};

class BVH4_GPU : public BVHBase
{
public:
	struct BVHNode // actual struct is unused; left here to show structure of data in bvh4Data.
	{
		// 4-way BVH node, optimized for GPU rendering
		struct aabb8 { uint8_t xmin, ymin, zmin, xmax, ymax, zmax; }; // quantized
		bvhvec3 aabbMin; uint32_t c0Info;			// 16
		bvhvec3 aabbExt; uint32_t c1Info;			// 16
		aabb8 c0bounds, c1bounds; uint32_t c2Info;	// 16
		aabb8 c2bounds, c3bounds; uint32_t c3Info;	// 16; total: 64 bytes
		// childInfo, 32bit:
		// msb:        0=interior, 1=leaf
		// leaf:       16 bits: relative start of triangle data, 15 bits: triangle count.
		// interior:   31 bits: child node address, in float4s from BVH data start.
		// Triangle data: directly follows nodes with leaves. Per tri:
		// - bvhvec4 vert0, vert1, vert2
		// - uint vert0.w stores original triangle index.
		// We can make the node smaller by storing child nodes sequentially, but
		// there is no way we can shave off a full 16 bytes, unless aabbExt is stored
		// as chars as well, as in CWBVH.
	};
	BVH4_GPU( BVHContext ctx = {} ) { layout = LAYOUT_BVH4_GPU; context = ctx; }
	BVH4_GPU( const MBVH<4>& bvh4 ) { /* DEPRECATED */ layout = LAYOUT_BVH4_GPU; ConvertFrom( bvh4 ); }
	BVH4_GPU( BVH4_GPU&& );
	BVH4_GPU& operator=( const BVH4_GPU& ) = default;
	~BVH4_GPU();
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Optimize( const uint32_t iterations = 25, bool extreme = false );
	void ConvertFrom( const MBVH<4>& original, bool compact = true );
	float SAHCost( const uint32_t nodeIdx = 0 ) const { return bvh4.SAHCost( nodeIdx ); }
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const { FALLBACK_SHADOW_QUERY( ray ); }
	// BVH data
	bvhvec4* bvh4Data = 0;			// 64-byte 4-wide BVH node for efficient GPU rendering.
	uint32_t allocatedBlocks = 0;	// node data and triangles are stored in 16-byte blocks.
	uint32_t usedBlocks = 0;		// actually used storage.
	MBVH<4> bvh4;					// BVH4_GPU is created from BVH4 and uses its data.
	bool ownBVH4 = true;			// False when ConvertFrom receives an external bvh.
};

class BVH4_CPU : public BVHBase
{
public:
	enum { EMPTY_BIT = 1 << 31, LEAF_BIT = 1 << 30 };
	struct BVHNode
	{
		// 4-way BVH node, optimized for CPU rendering.
		// SSE version.
		SIMDVEC4 xmin4, xmax4;
		SIMDVEC4 ymin4, ymax4;
		SIMDVEC4 zmin4, zmax4;
		SIMDIVEC4 child4, perm4; // total 128 bytes.
	};
	struct CacheLine { SIMDVEC4 a, b, c, d; };
	BVH4_CPU( BVHContext ctx = {} ) { layout = LAYOUT_BVH4_CPU; context = ctx; c_int = 2; l_quads = true; }
	BVH4_CPU( BVH4_CPU&& );
	BVH4_CPU& operator=( const BVH4_CPU& ) = default;
	~BVH4_CPU();
	void Save( const char* fileName );
	bool Load( const char* fileName, const uint32_t expectedTris );
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims );
	void Optimize( const uint32_t iterations, bool extreme );
	void Refit();
	float SAHCost( const uint32_t nodeIdx ) const;
	void ConvertFrom( MBVH<4>& original );
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const;
	// Intersect / IsOccluded specialize for ray octant using templated functions.
	template <bool posX, bool posY, bool posZ> int32_t Intersect( Ray& ray ) const;
	template <bool posX, bool posY, bool posZ> bool IsOccluded( const Ray& ray ) const;
	// BVH4 data
	CacheLine* bvh4Data = 0;		// Interleaved interior (128b) and leaf (192b) data.
	MBVH<4> bvh4;					// BVH4_CPU is created from BVH4 and uses its data.
	bool ownBVH4 = true;			// false when ConvertFrom receives an external bvh4.
	uint32_t allocatedBlocks = 0;	// node data and triangles are stored in 16-byte blocks.
	uint32_t usedBlocks = 0;		// the amount of data actually used.
};

class BVH8_CWBVH : public BVHBase
{
public:
	BVH8_CWBVH( BVHContext ctx = {} ) { layout = LAYOUT_CWBVH; context = ctx; }
	BVH8_CWBVH( MBVH<8>& bvh8 ) { /* DEPRECATED */ layout = LAYOUT_CWBVH; ConvertFrom( bvh8 ); }
	BVH8_CWBVH( BVH8_CWBVH&& );
	BVH8_CWBVH& operator=( const BVH8_CWBVH& ) = default;
	~BVH8_CWBVH();
	void Save( const char* fileName );
	bool Load( const char* fileName, const uint32_t expectedTris );
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount );
	void Optimize( const uint32_t iterations = 25, bool extreme = false );
	void ConvertFrom( MBVH<8>& original, bool compact = true );
	float SAHCost( const uint32_t nodeIdx = 0 ) const;
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const { FALLBACK_SHADOW_QUERY( ray ); }
	// BVH8 data
	bvhvec4* bvh8Data = 0;			// nodes in CWBVH format.
	bvhvec4* bvh8Tris = 0;			// triangle data for CWBVH nodes.
	uint32_t allocatedBlocks = 0;	// node data is stored in blocks of 16 byte.
	uint32_t usedBlocks = 0;		// actually used blocks.
	MBVH<8> bvh8;					// BVH8_CWBVH is created from BVH8 and uses its data.
	bool ownBVH8 = true;			// false when ConvertFrom receives an external bvh8.
};

// Storage for up to four triangles, in SoA layout, for BVH8_CPU.
struct BVHTri4Leaf
{
	SIMDVEC4 v0x4, v0y4, v0z4;
	SIMDVEC4 e1x4, e1y4, e1z4;
	SIMDVEC4 e2x4, e2y4, e2z4;
	uint32_t primIdx[4];		// total: 160 bytes.
	SIMDVEC4 dummy0, dummy1;	// pad to 3 full cachelines.
	inline void SetData( const bvhvec3& v0, const bvhvec3& e1, const bvhvec3& e2, const uint32_t pidx, const uint32_t slot )
	{
		((float*)&v0x4)[slot] = v0.x, ((float*)&v0y4)[slot] = v0.y, ((float*)&v0z4)[slot] = v0.z;
		((float*)&e1x4)[slot] = e1.x, ((float*)&e1y4)[slot] = e1.y, ((float*)&e1z4)[slot] = e1.z;
		((float*)&e2x4)[slot] = e2.x, ((float*)&e2y4)[slot] = e2.y, ((float*)&e2z4)[slot] = e2.z, primIdx[slot] = pidx;
	}
};

// Storage for a single triangle, for BVH8_CPU.
struct BVHTri1Leaf
{
	bvhvec3 v0, e1, e2;
	uint32_t primIdx;			// total: 40 bytes
};

class BVH8_CPU : public BVHBase
{
public:
	enum { EMPTY_BIT = 1 << 31, LEAF_BIT = 1 << 30 };
	struct BVHNode
	{
		// 8-way BVH node, optimized for CPU rendering.
		// Based on: "Accelerated Single Ray Tracing for Wide Vector Units", Fuetterling et al., 2017,
		// and the implementation by Mathijs Molenaar, https://github.com/mathijs727/pandora
		SIMDVEC8 xmin8, xmax8;
		SIMDVEC8 ymin8, ymax8;
		SIMDVEC8 zmin8, zmax8;
		SIMDIVEC8 child8; // bits: 31..29 = flags, 28..0: node index.
		SIMDIVEC8 perm8;
		// flag bits: 000 is an empty node, 010 is an interior node. 1xx is leaf; xx = tricount.
	};
	struct BVHNodeCompact
	{
		// Novel 128-byte 8-way BVH node.
		SIMDIVEC8 child8, perm8;									// 64
		float d0, bminx, bminy, bminz, d1, bextx, bexty, bextz;		// 32
		SIMDIVEC4 cbminmaxx8, cbminmaxy8;							// 32, total: 128
	};
	struct CacheLine { SIMDVEC8 a, b; };
	BVH8_CPU( BVHContext ctx = {} ) { layout = LAYOUT_BVH8_AVX2; context = ctx; c_int = 2; l_quads = true; }
	BVH8_CPU( BVH8_CPU&& );
	BVH8_CPU& operator=( const BVH8_CPU& ) = default;
	~BVH8_CPU();
	void Save( const char* fileName );
	bool Load( const char* fileName, const uint32_t expectedTris );
	void Build( const bvhvec4* vertices, const uint32_t primCount );
	void Build( const bvhvec4slice& vertices );
	void Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims );
	void Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims );
	void BuildHQ( const bvhvec4* vertices, const uint32_t primCount );
	void BuildHQ( const bvhvec4slice& vertices );
	void BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims );
	void BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims );
	void Optimize( const uint32_t iterations, bool extreme );
	void Refit();
	float SAHCost( const uint32_t nodeIdx ) const;
	void ConvertFrom( MBVH<8>& original );
	int32_t Intersect( Ray& ray ) const;
	bool IsOccluded( const Ray& ray ) const;
	// Intersect / IsOccluded specialize for ray octant using templated functions.
	template <bool posX, bool posY, bool posZ> int32_t Intersect( Ray& ray ) const;
	template <bool posX, bool posY, bool posZ> bool IsOccluded( const Ray& ray ) const;
	// BVH8 data
	CacheLine* bvh8Data = 0;		// Interleaved interior (256b) and leaf (192b) data.
	MBVH<8> bvh8;					// BVH8_CPU is created from BVH8 and uses its data.
	bool ownBVH8 = true;			// false when ConvertFrom receives an external bvh8.
	uint32_t allocatedBlocks = 0;	// node data and triangles are stored in 16-byte blocks.
	uint32_t usedBlocks = 0;		// the amount of data actually used.
};

// BLASInstance: A TLAS is built over BLAS instances, where a single BLAS can be
// used with multiple transforms, and multiple BLASses can be combined in a complex
// scene. The TLAS is built over the world-space AABBs of the BLAS root nodes.
class ALIGNED( 64 ) BLASInstance
{
public:
	BLASInstance() = default;
	BLASInstance( uint32_t idx ) : blasIdx( idx ) {}
	bvhmat4 transform; // defaults to identity
	bvhmat4 invTransform; // defaults to identity
	bvhvec3 aabbMin = bvhvec3( BVH_FAR );
	uint32_t blasIdx = 0;
	bvhvec3 aabbMax = bvhvec3( -BVH_FAR );
	uint32_t mask = (uint32_t)RAY_MASK_INTERSECT_ALL; // set to intersect with all rays by default
	uint32_t dummy[8]; // pad struct to 64 byte
	void Update( BVHBase * blas );
	void InvertTransform();
};

#ifdef DOUBLE_PRECISION_SUPPORT

// BLASInstanceEx: Double-precision version of BLASInstance.
class BLASInstanceEx
{
public:
	BLASInstanceEx() = default;
	BLASInstanceEx( uint64_t idx ) : blasIdx( idx ) {}
	double transform[16] = { 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1 }; // identity
	double invTransform[16] = { 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1, 0, 0, 0, 0, 1 }; // identity
	bvhdbl3 aabbMin = bvhdbl3( BVH_DBL_FAR );
	uint64_t blasIdx = 0;
	bvhdbl3 aabbMax = bvhdbl3( -BVH_DBL_FAR );
	uint64_t mask = RAY_MASK_INTERSECT_ALL; // set to intersect with all rays by default
	void Update( BVH_Double* blas );
	void InvertTransform();
};

#endif

} // namespace tinybvh

#endif // TINY_BVH_H_

// ============================================================================
//
//        I M P L E M E N T A T I O N
//
// ============================================================================

#ifdef TINYBVH_IMPLEMENTATION

#include <assert.h>			// for assert
#ifdef _MSC_VER
#include <intrin.h>			// for __lzcnt
#endif
#include <fstream>			// fstream

// job manager includes
#include <functional>
#include <algorithm>
#include <condition_variable>
#include <mutex>
#include <deque>

// We need quite a bit of type reinterpretation, so we'll
// turn off the gcc warning here until the end of the file.
#ifdef __GNUC__
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wstrict-aliasing"
#endif

// Some constexpr stuff to produce nice-looking branches in
// *::Intersect with proper dead code elinimation.
#ifdef ENABLE_INDEXED_GEOMETRY
static constexpr bool indexedEnabled = true;
#else
static constexpr bool indexedEnabled = false;
#endif
#ifdef ENABLE_CUSTOM_GEOMETRY
static constexpr bool customEnabled = true;
#else
static constexpr bool customEnabled = false;
#endif

namespace tinybvh {

#if defined BVH_USESSE || defined BVH_USENEON
inline uint32_t __bfind( uint32_t x ) // https://github.com/mackron/refcode/blob/master/lzcnt.c
{
#if defined _MSC_VER && !defined __clang__
	return 31 - __lzcnt( x );
#elif defined __EMSCRIPTEN__
	return 31 - __builtin_clz( x );
#elif defined __GNUC__ || defined __clang__
#if defined __APPLE__ || __has_builtin(__builtin_clz)
	return 31 - __builtin_clz( x );
#else
	uint32_t r;
	__asm__ __volatile__( "lzcnt{l %1, %0| %0, %1}" : "=r"(r) : "r"(x) : "cc" );
	return 31 - r;
#endif
#endif
}
#endif

// array element counting; https://stackoverflow.com/questions/12784136
#define BVH_NUM_ELEMS(a) (sizeof(a)/sizeof 0[a])

// random numbers
uint32_t tinybvh_rnduint( uint32_t& s ) { s ^= s << 13, s ^= s >> 17, s ^= s << 5; return s; }
float tinybvh_rndfloat( uint32_t& s ) { return tinybvh_rnduint( s ) * 2.3283064365387e-10f; }

// random unit vector
bvhvec3 tinybvh_rndvec3( uint32_t& s )
{
	bvhvec3 R;
loop: R = bvhvec3( tinybvh_rndfloat( s ) - 0.5f, tinybvh_rndfloat( s ) * 0.5f, tinybvh_rndfloat( s ) * 0.5f );
	if (tinybvh_dot( R, R ) > 0.25f) goto loop;
	return tinybvh_normalize( R );
}

// radix sort
static inline unsigned FloatToKey( const float value )
{
	// Integer comparisons between numbers returned from this function behave
	// as if the original float values where compared.
	// Simple reinterpretation works only for [0, ...], but this also handles negatives
	const unsigned f = *(unsigned*)&value, mask = (unsigned)((int)f >> 31 | (1 << 31));
	return f ^ mask;
}
template<typename T, typename Func> static inline void RadixSort( T* input, T* output, int len, Func getKey )
{
	// http://stereopsis.com/radix.html - Beats std::sort unless for small inputs (say len <= ~64)
	const int radixSize = 11, binSize = 1 << radixSize, mask = binSize - 1, passes = 3;
	int prefixSum[binSize * passes] = { 0 };
	auto getPrefixSumRef = [=, &prefixSum]( unsigned key, int pass ) -> int& {
		const unsigned radix = (key >> (pass * radixSize)) & mask;
		int& offset = prefixSum[radix + pass * binSize];
		return offset;
		};
	// Compute histogram for all passes
	for (int i = 0; i < len; i++)
	{
		const unsigned key = getKey( input[i] );
		getPrefixSumRef( key, 0 )++, getPrefixSumRef( key, 1 )++, getPrefixSumRef( key, 2 )++;
	}
	// Compute prefix sum for all passes
	int sum0 = 0, sum1 = 0, sum2 = 0;
	for (int i = 0; i < binSize; i++)
	{
		const int temp0 = prefixSum[i + 0 * binSize];
		const int temp1 = prefixSum[i + 1 * binSize];
		const int temp2 = prefixSum[i + 2 * binSize];
		prefixSum[i + 0 * binSize] = sum0;
		prefixSum[i + 1 * binSize] = sum1;
		prefixSum[i + 2 * binSize] = sum2;
		sum0 += temp0, sum1 += temp1, sum2 += temp2;
	}
	// Sort from LSB to MSB in radix-sized steps
	for (int i = 0; i < passes; i++)
	{
		for (int j = 0; j < len; j++)
		{
			const T element = input[j];
			const unsigned key0 = getKey( element );
			output[getPrefixSumRef( key0, i )++] = element;
		}
		tinybvh_swap( input, output );
	}
}

// error handling
#ifdef _WINDOWS_ // windows.h has been included
#ifdef _MSC_VER // avoid _CRT_SECURE_NO_WARNING requirement for MSC
#define BVH_FATAL_ERROR_IF(c,s) if (c) { char t[512]; sprintf_s( t, 512, \
	"Fatal error in tiny_bvh.h, line %i:\n%s\n", __LINE__, s ); \
	MessageBoxA( NULL, t, "Fatal error", MB_OK ); exit( 1 ); }
#else
#define BVH_FATAL_ERROR_IF(c,s) if (c) { char t[512]; sprintf( t, \
	"Fatal error in tiny_bvh.h, line %i:\n%s\n", __LINE__, s ); \
	MessageBoxA( NULL, t, "Fatal error", MB_OK ); exit( 1 ); }
#endif
#else
#define BVH_FATAL_ERROR_IF(c,s) if (c) { fprintf( stderr, \
	"Fatal error in tiny_bvh.h, line %i:\n%s\n", __LINE__, s ); exit( 1 ); }
#endif
#define BVH_FATAL_ERROR(s) BVH_FATAL_ERROR_IF(1,s)

// Fallbacks to be used in the absence of HW SIMD support.
#if !defined BVH_USESSE || defined BVH_USENEON
int32_t BVH4_CPU::Intersect( Ray& ) const { BVH_FATAL_ERROR( "BVH4_CPU::Intersect requires SSE. " ); }
bool BVH4_CPU::IsOccluded( const Ray& ) const { BVH_FATAL_ERROR( "BVH4_CPU::IsOccluded requires SSE. " ); }
#endif
#if !defined BVH_USEAVX || defined BVH_USENEON
void BVH::BuildAVX( const bvhvec4*, const uint32_t ) { BVH_FATAL_ERROR( "BVH::BuildAVX requires AVX." ); }
void BVH::BuildAVX( const bvhvec4slice& ) { BVH_FATAL_ERROR( "BVH::BuildAVX requires AVX." ); }
void BVH::BuildAVX( const bvhvec4*, const uint32_t*, const uint32_t ) { BVH_FATAL_ERROR( "BVH::BuildAVX requires AVX." ); }
void BVH::BuildAVX( const bvhvec4slice&, const uint32_t*, const uint32_t ) { BVH_FATAL_ERROR( "BVH::BuildAVX requires AVX." ); }
int32_t BVH8_CWBVH::Intersect( Ray& ) const { BVH_FATAL_ERROR( "BVH8_CWBVH::Intersect requires AVX." ); }
#endif // BVH_USEAVX
#if !defined BVH_USEAVX2 || defined BVH_USENEON
int32_t BVH8_CPU::Intersect( Ray& ) const { BVH_FATAL_ERROR( "BVH8_CPU::Intersect requires AVX2 and FMA." ); }
bool BVH8_CPU::IsOccluded( const Ray& ) const { BVH_FATAL_ERROR( "BVH8_CPU::IsOccluded requires AVX2 and FMA." ); }
#endif // BVH_USEAVX2
#if !defined BVH_USEAVX && !defined BVH_USENEON
int32_t BVH_SoA::Intersect( Ray& ) const { BVH_FATAL_ERROR( "BVH_SoA::Intersect requires AVX or NEON." ); }
bool BVH_SoA::IsOccluded( const Ray& ) const { BVH_FATAL_ERROR( "BVH_SoA::IsOccluded requires AVX or NEON." ); }
#endif // !(BVH_USEAVX && BVH_USENEON)

// code compaction: Moeller-Trumbore ray/tri test.
#define MOLLER_TRUMBORE_TEST( tmax, exit ) \
	const bvhvec3 h = tinybvh_cross( ray.D, e2 );	\
	const float a = tinybvh_dot( e1, h );			\
	if (fabs( a ) < 0.000001f) exit;				\
	const float f = 1 / a;							\
	const bvhvec3 s = ray.O - v0;					\
	const float u = f * tinybvh_dot( s, h );		\
	const bvhvec3 q = tinybvh_cross( s, e1 );		\
	const float v = f * tinybvh_dot( ray.D, q );	\
	const bool miss = u < 0 || v < 0 || u + v > 1;	\
	if (miss) exit;									\
	const float t = f * tinybvh_dot( e2, q );		\
	if (t < 0 || t > tmax) exit;

// code compaction: fetching triangle vertices, with or without indices.
#define GET_PRIM_INDICES_I0_I1_I2( bvh, idx ) if (indexedEnabled && bvh.vertIdx != 0) \
	i0 = bvh.vertIdx[idx * 3], i1 = bvh.vertIdx[idx * 3 + 1], i2 = bvh.vertIdx[idx * 3 + 2]; \
	else i0 = idx * 3, i1 = idx * 3 + 1, i2 = idx * 3 + 2;

// ray validation: throw an error if the input ray contains nans.
#define VALIDATE_RAY(r) { float test = r.D.x + r.D.y + r.D.z + ray.hit.t + r.O.x \
	+ r.O.y + r.O.z; BVH_FATAL_ERROR_IF( tinybvh_isnan( test ), "Input ray contains NaNs." ); }

#ifndef TINYBVH_USE_CUSTOM_VECTOR_TYPES

bvhmat4 operator*( const float s, const bvhmat4& a )
{
	bvhmat4 r;
	for (uint32_t i = 0; i < 16; i++) r[i] = a[i] * s;
	return r;
}
bvhmat4 operator*( const bvhmat4& a, const bvhmat4& b )
{
	bvhmat4 r;
	for (uint32_t i = 0; i < 16; i += 4) for (uint32_t j = 0; j < 4; ++j)
		r[i + j] = (a[i + 0] * b[j + 0]) + (a[i + 1] * b[j + 4]) +
		(a[i + 2] * b[j + 8]) + (a[i + 3] * b[j + 12]);
	return r;
}
bvhvec4::bvhvec4( const bvhvec3& a ) { x = a.x; y = a.y; z = a.z; w = 0; }
bvhvec4::bvhvec4( const bvhvec3& a, float b ) { x = a.x; y = a.y; z = a.z; w = b; }

#endif

bvhvec4slice::bvhvec4slice( const bvhvec4* data, uint32_t count, uint32_t stride ) :
	data{ reinterpret_cast<const int8_t*>(data) },
	count{ count }, stride{ stride } {
}

const bvhvec4& bvhvec4slice::operator[]( size_t i ) const
{
#ifdef PARANOID
	BVH_FATAL_ERROR_IF( i >= count, "bvhvec4slice::[..], Reading outside slice." );
#endif
	return *reinterpret_cast<const bvhvec4*>(data + stride * i);
}

void* BVHBase::AlignedAlloc( size_t size )
{
	return context.malloc ? context.malloc( size, context.userdata ) : nullptr;
}

void BVHBase::AlignedFree( void* ptr )
{
	if (context.free && ptr)
		context.free( ptr, context.userdata );
}

void BVHBase::CopyBasePropertiesFrom( const BVHBase& original )
{
	this->rebuildable = original.rebuildable;
	this->refittable = original.refittable;
	this->may_have_holes = original.may_have_holes;
	this->bvh_over_aabbs = original.bvh_over_aabbs;
	this->bvh_over_indices = original.bvh_over_indices;
	this->context = original.context;
	this->triCount = original.triCount;
	this->idxCount = original.idxCount;
	this->c_int = original.c_int, this->c_trav = original.c_trav;
	this->aabbMin = original.aabbMin, this->aabbMax = original.aabbMax;
}

#ifdef MT_USE_JOBSYSTEM

// Wicked job system, condensed. https://github.com/turanszkij/WickedEngine
// Removed: Thread priority, Dispatch, graceful shutdown; not needed in TinyBVH.
class JobSystem
{
public:
	JobSystem() { Initialize(); }
	~JobSystem()
	{
		if (res.numThreads == 0) return;
		res.alive.store( false );
		bool wake_loop = true;
		std::thread waker( [&] { while (wake_loop) res.sleepingCondition.notify_all(); } );
		for (auto& thread : res.threads) thread.join();
		wake_loop = false;
		waker.join();
		res.jobQueue.reset();
		res.threads.clear();
		res.numThreads = 0;
	}
	struct context { std::atomic<uint32_t> counter{ 0 }; } ctx;
	struct Job
	{
		std::function<void()> task;
		inline uint32_t execute( context& ctx ) { task(); return ctx.counter.fetch_sub( 1 ); }
	};
	struct JobQueue
	{
		std::deque<Job> queue;
		std::mutex locker;
		inline void push_back( const Job& item ) { std::scoped_lock lock( locker ); queue.push_back( item ); }
		inline bool pop_front( Job& item )
		{
			std::scoped_lock lock( locker );
			if (queue.empty()) return false; else item = std::move( queue.front() );
			queue.pop_front();
			return true;
		}
	};
	struct Resources
	{
		uint32_t numThreads = 0;
		std::vector<std::thread> threads;
		std::unique_ptr<JobQueue[]> jobQueue;
		std::atomic<uint32_t> nextQueue{ 0 };
		std::condition_variable sleepingCondition;
		std::mutex sleepingMutex;
		std::condition_variable waitingCondition;
		std::mutex waitingMutex;
		std::atomic_bool alive{ true };
		inline void work( context& ctx, uint32_t startingQueue )
		{
			Job job;
			for (uint32_t i = 0; i < numThreads; ++i) while (jobQueue[startingQueue++ % numThreads].pop_front( job ))
				if (job.execute( ctx ) == 1) { std::unique_lock<std::mutex> lock( waitingMutex ); waitingCondition.notify_all(); }
		}
	} res;
	void Initialize()
	{
		res.numThreads = std::thread::hardware_concurrency();
		res.jobQueue.reset( new JobQueue[res.numThreads] );
		res.threads.reserve( res.numThreads );
		context& c = ctx;
		Resources& r = res;
		for (uint32_t threadID = 0; threadID < res.numThreads; threadID++)
			res.threads.emplace_back( [&c, threadID, &r]
				{	while (r.alive.load()) {
			r.work( c, threadID );
			std::unique_lock<std::mutex> lock( r.sleepingMutex );
			r.sleepingCondition.wait( lock );
		} } );
	}
	void Execute( const std::function<void()>& task )
	{
		ctx.counter.fetch_add( 1 );
		Job job;
		job.task = task;
		res.jobQueue[res.nextQueue.fetch_add( 1 ) % res.numThreads].push_back( job );
		res.sleepingCondition.notify_one();
	}
	void Wait()
	{
		if (!IsBusy()) return;
		res.sleepingCondition.notify_all();
		res.work( ctx, res.nextQueue.fetch_add( 1 ) % res.numThreads );
		while (IsBusy())
		{
			std::unique_lock<std::mutex> lock( res.waitingMutex );
			if (IsBusy()) res.waitingCondition.wait( lock, [this] { return !IsBusy(); } );
		}
	}
	bool IsBusy() { return ctx.counter.load() > 0; }
};

static thread_local JobSystem* globalSubtreeJobs = 0;
static thread_local JobSystem* globalBinningJobs = 0;

#endif

// BVH implementation
// ----------------------------------------------------------------------------

// static variable declarations
#ifdef BVH_USESSE
__m128 BVH::binmul3 = _mm_set1_ps( AVXBINS * 0.49999f );
__m128i BVH::maxbin4 = _mm_set1_epi32( 7 );
__m128 BVH::half4 = _mm_set1_ps( 0.5f );
__m128 BVH::two4 = _mm_set1_ps( 2.0f ), BVH::min1 = _mm_set1_ps( -1 );
__m128 BVH::mask3 = _mm_cmpeq_ps( _mm_setr_ps( 0, 0, 0, 1 ), _mm_setzero_ps() );
#endif
#ifdef BVH_USEAVX
__m256 BVH::max8 = _mm256_set1_ps( -BVH_FAR ), BVH::mask6 = _mm256_set_m128( mask3, mask3 );
__m256 BVH::signFlip8 = _mm256_setr_ps( -0.0f, -0.0f, -0.0f, 0.0f, 0.0f, 0.0f, 0.0f, 0.0f );
#endif

BVH::BVH( BVH&& other ) noexcept
{
	// shallow copy of parameters, options, and pointers
	*this = other;
	// mark 'other' as deleted to avoid double-free
	other.primIdx = 0;
	other.bvhNode = 0;
	other.fragment = 0;
}

BVH::~BVH()
{
	AlignedFree( bvhNode );
	bvhNode = 0;
	AlignedFree( primIdx );
	primIdx = 0;
	AlignedFree( fragment );
	fragment = 0;
}

void BVH::Save( const char* fileName )
{
	// saving is easy, it's the loadingn that will be complex.
	std::fstream s{ fileName, s.binary | s.out };
	uint32_t header = TINY_BVH_CACHE_VERSION /* 16 bit */ + (layout << 24);
	s.write( (char*)&header, sizeof( uint32_t ) );
	s.write( (char*)&triCount, sizeof( uint32_t ) );
	s.write( (char*)this, sizeof( BVH ) );
	s.write( (char*)bvhNode, usedNodes * sizeof( BVHNode ) );
	s.write( (char*)primIdx, idxCount * sizeof( uint32_t ) );
}

bool BVH::Load( const char* fileName, const bvhvec4* vertices, const uint32_t primCount )
{
	return Load( fileName, bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}

bool BVH::Load( const char* fileName, const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount )
{
	return Load( fileName, bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) }, indices, primCount );
}

bool BVH::Load( const char* fileName, const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount )
{
	// open file and check contents
	std::fstream s{ fileName, s.binary | s.in };
	if (!s) return false;
	BVHContext ctxBackup = context;
	bool expectIndexed = (indices != nullptr), saveNewVersion = false;
	uint32_t header, fileTriCount;
	s.read( (char*)&header, sizeof( uint32_t ) );
	if ((header >> 24) != layout) return false;
	if ((header & 0xffffff) != TINY_BVH_CACHE_VERSION) return false;
	s.read( (char*)&fileTriCount, sizeof( uint32_t ) );
	if (expectIndexed && fileTriCount != primCount) return false;
	if (!expectIndexed && fileTriCount != vertices.count / 3) return false;
	// backup pointers to JobSystems: can't deserialize pointers.
	JobSystem* subtreeBackup = subtreeJobs;
	JobSystem* binningBackup = binningJobs;
	// all checks passed; safe to overwrite *this
	s.read( (char*)this, sizeof( BVH ) );
	bool fileIsIndexed = vertIdx != nullptr;
	if (expectIndexed != fileIsIndexed) return false; // not what we expected.
	if (blasList != nullptr || instList != nullptr) return false; // can't load/save TLAS.
	context = ctxBackup; // can't load context; function pointers will differ.
	subtreeJobs = subtreeBackup;
	binningJobs = binningBackup;
	bvhNode = (BVHNode*)AlignedAlloc( allocatedNodes * sizeof( BVHNode ) );
	primIdx = (uint32_t*)AlignedAlloc( idxCount * sizeof( uint32_t ) );
	fragment = 0; // no need for this in a BVH that can't be rebuilt.
	s.read( (char*)bvhNode, usedNodes * sizeof( BVHNode ) );
	s.read( (char*)primIdx, idxCount * sizeof( uint32_t ) );
	verts = vertices; // we can't load vertices since the BVH doesn't own this data.
	vertIdx = (uint32_t*)indices;
	// all ok.
	if (saveNewVersion) Save( fileName );
	return true;
}

void BVH::BuildDefault( const bvhvec4* vertices, const uint32_t primCount )
{
	// access point for builds over a raw list of vertices. The stride of
	// the vertex data must be 16 bytes.
	// The list will be encapsulated in a 'slice'. The slice can also be used
	// directly to provide data with a different stride, which enables use of
	// vertex buffers created for rasterization.
	BuildDefault( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}

void BVH::BuildDefault( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount )
{
	// access point for builders over an indexed list of raw vertices.
	BuildDefault( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) }, indices, primCount );
}

void BVH::BuildDefault( const bvhvec4slice& vertices )
{
	// default builder: used internally when constructing a BVH layout requires
	// a regular BVH. Currently, this is the case for all of them.
#if defined BVH_USEAVX
	// if AVX is supported, BuildAVX is the optimal option. Tree quality is
	// identical to the reference builder, but speed is much better.
	BuildAVX( vertices );
#elif defined BVH_USENEON
	BuildNEON( vertices );
#else
	// fallback option, in case neither AVX or NEON is not supported: the reference
	// builder, which should work on all platforms.
	Build( vertices );
#endif
}

void BVH::BuildDefault( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount )
{
	// default builder for indexed vertices. See notes above.
#if defined BVH_USEAVX
	BuildAVX( vertices, indices, primCount );
#elif defined BVH_USENEON
	BuildNEON( vertices, indices, primCount );
#else
	Build( vertices, indices, primCount );
#endif
}

void BVH::ConvertFrom( const BVH_Verbose& original, bool compact )
{
	// allocate space
	const uint32_t spaceNeeded = compact ? original.usedNodes : original.allocatedNodes;
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		bvhNode = (BVHNode*)AlignedAlloc( triCount * 2 * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
	}
	memset( bvhNode, 0, sizeof( BVHNode ) * spaceNeeded );
	CopyBasePropertiesFrom( original );
	this->verts = original.verts;
	this->primIdx = original.primIdx;
	// start conversion
	uint32_t srcNodeIdx = 0, dstNodeIdx = 0;
	newNodePtr = 2;
	uint32_t srcStack[1024], dstStack[1024], stackPtr = 0;
	while (1)
	{
		const BVH_Verbose::BVHNode& orig = original.bvhNode[srcNodeIdx];
		bvhNode[dstNodeIdx].aabbMin = orig.aabbMin;
		bvhNode[dstNodeIdx].aabbMax = orig.aabbMax;
		if (orig.isLeaf())
		{
			bvhNode[dstNodeIdx].triCount = orig.triCount;
			bvhNode[dstNodeIdx].leftFirst = orig.firstTri;
			if (stackPtr == 0) break;
			srcNodeIdx = srcStack[--stackPtr];
			dstNodeIdx = dstStack[stackPtr];
		}
		else
		{
			bvhNode[dstNodeIdx].leftFirst = newNodePtr;
			uint32_t srcRightIdx = orig.right;
			srcNodeIdx = orig.left, dstNodeIdx = newNodePtr++;
			srcStack[stackPtr] = srcRightIdx;
			dstStack[stackPtr++] = newNodePtr++;
		}
	}
	usedNodes = original.usedNodes;
}

float BVH::SAHCost( const uint32_t nodeIdx ) const
{
	// Determine the SAH cost of the tree. This provides an indication
	// of the quality of the BVH: Lower is better.
	const BVHNode& n = bvhNode[nodeIdx];
	if (n.isLeaf()) return c_int * n.SurfaceArea() * n.triCount;
	float cost = c_trav * n.SurfaceArea() + SAHCost( n.leftFirst ) + SAHCost( n.leftFirst + 1 );
	return nodeIdx == 0 ? (cost / n.SurfaceArea()) : cost;
}

float BVH::PrimArea( const uint32_t p ) const
{
	uint32_t vidx = primIdx[p] * 3;
	bvhvec3 v0, v1, v2;
	if (vertIdx) v0 = verts[vertIdx[vidx]], v1 = verts[vertIdx[vidx + 1]], v2 = verts[vertIdx[vidx + 2]];
	else v0 = verts[vidx], v1 = verts[vidx + 1], v2 = verts[vidx + 2];
	return 0.5f * tinybvh_length( tinybvh_cross( v1 - v0, v2 - v0 ) );
}

float BVH::EPOArea( const uint32_t subtreeRoot, const uint32_t nodeIdx ) const
{
	// abort if we reached the subtree
	if (nodeIdx == subtreeRoot) return 0;
	const BVHNode& n = bvhNode[nodeIdx];
	const BVHNode& subtree = bvhNode[subtreeRoot];
	// handle case where n is a leaf node
	float area = 0;
	if (n.isLeaf())
	{
		// clip triangles to AABB of subtreeRoot and sum resulting areas
		const bvhvec3 bmin = subtree.aabbMin, bmax = subtree.aabbMax;
		for (unsigned i = 0; i < n.triCount; i++)
		{
			// Sutherland-Hodgeman against six bounding planes
			uint32_t Nin = 3, vidx = primIdx[n.leftFirst + i] * 3;
			bvhvec3 vin[10], vout[10], C;
			if (vertIdx)
				vin[0] = verts[vertIdx[vidx]], vin[1] = verts[vertIdx[vidx + 1]], vin[2] = verts[vertIdx[vidx + 2]];
			else
				vin[0] = verts[vidx], vin[1] = verts[vidx + 1], vin[2] = verts[vidx + 2];
			for (uint32_t a = 0; a < 3; a++)
			{
				uint32_t Nout = 0;
				const float l = bmin[a], r = bmax[a];
				for (uint32_t v = 0; v < Nin; v++)
				{
					bvhvec3 v0 = vin[v], v1 = vin[(v + 1) % Nin];
					const bool v0in = v0[a] >= l, v1in = v1[a] >= l;
					if (!(v0in || v1in)) continue; else if (v0in ^ v1in)
						C = v0 + (l - v0[a]) / (v1[a] - v0[a]) * (v1 - v0),
						C[a] = l /* accurate */, vout[Nout++] = C;
					if (v1in) vout[Nout++] = v1;
				}
				Nin = 0;
				for (uint32_t v = 0; v < Nout; v++)
				{
					bvhvec3 v0 = vout[v], v1 = vout[(v + 1) % Nout];
					const bool v0in = v0[a] <= r, v1in = v1[a] <= r;
					if (!(v0in || v1in)) continue; else if (v0in ^ v1in)
						C = v0 + (r - v0[a]) / (v1[a] - v0[a]) * (v1 - v0),
						C[a] = r /* accurate */, vin[Nin++] = C;
					if (v1in) vin[Nin++] = v1;
				}
			}
			if (Nin < 3) continue;
			// calculate area of remaining convex shape in vin
			const uint32_t tris = Nin - 2;
			bvhvec3 v0 = vin[0], v1, v2;
			for (uint32_t j = 0; j < tris; j++)
				v1 = vin[j + 1], v2 = vin[j + 2],
				area += 0.5f * tinybvh_length( tinybvh_cross( v1 - v0, v2 - v0 ) );
		}
		return area;
	}
	// recurse if n is an inner node
	BVHNode& left = bvhNode[n.leftFirst], & right = bvhNode[n.leftFirst + 1];
	if (tinybvh_aabbs_overlap( left.aabbMin, left.aabbMax, subtree.aabbMin, subtree.aabbMax ))
		area += EPOArea( subtreeRoot, n.leftFirst );
	if (tinybvh_aabbs_overlap( right.aabbMin, right.aabbMax, subtree.aabbMin, subtree.aabbMax ))
		area += EPOArea( subtreeRoot, n.leftFirst + 1 );
	return area;
}

float BVH::EPOCost( const uint32_t nodeIdx ) const
{
	// Determine the EPO cost of the tree. See:
	// "On Quality Metrics of Bounding Volume Hierarchies", Aila et al., 2013.
	const BVHNode& n = bvhNode[nodeIdx];
	float area = EPOArea( nodeIdx );
	float cost = (n.isLeaf() ? (c_int * n.triCount) : c_trav) * area;
	if (!n.isLeaf()) cost += EPOCost( n.leftFirst ) + EPOCost( n.leftFirst + 1 );
	if (nodeIdx > 0) return cost;
	// recursion ends with node 0: Finalize EPO calculation
	float totalArea = 0;
	for (unsigned i = 0; i < triCount; i++) totalArea += PrimArea( i );
	cost /= totalArea;
	return (1.0f - W_EPO) * SAHCost( 0 ) + W_EPO * cost;
}

void BVH::SplitLeafs( const uint32_t maxPrims )
{
	uint32_t stack[64], stackPtr = 0, nodeIdx = 0;
	while (1)
	{
		BVHNode& node = bvhNode[nodeIdx];
		if (node.isLeaf())
		{
			if (node.triCount > maxPrims)
			{
				BVHNode& left = bvhNode[newNodePtr], & right = bvhNode[newNodePtr + 1];
				left = node, right = node;
				right.leftFirst = node.leftFirst + maxPrims;
				right.triCount = node.triCount - maxPrims;
				left.triCount = maxPrims, node.leftFirst = newNodePtr, node.triCount = 0, newNodePtr += 2;
			}
			else
			{
				if (!stackPtr) break;
				nodeIdx = stack[--stackPtr];
			}
		}
		else
		{
			nodeIdx = node.leftFirst;
			stack[stackPtr++] = node.leftFirst + 1;
		}
	}
	usedNodes = newNodePtr;
}

int32_t BVH::PrimCount( const uint32_t nodeIdx ) const
{
	// Determine the total number of primitives / fragments in leaf nodes.
	const BVHNode& n = bvhNode[nodeIdx];
	return n.isLeaf() ? n.triCount : (PrimCount( n.leftFirst ) + PrimCount( n.leftFirst + 1 ));
}

// Basic single-function BVH builder, using mid-point splits.
// This builder yields a correct BVH in little time, but the quality of the
// structure will be low. Use this only if build time is the bottleneck in
// your application, e.g., when you need to trace few rays.
void BVH::BuildQuick( const bvhvec4* vertices, const uint32_t primCount )
{
	// build the BVH with a continuous array of bvhvec4 vertices:
	// in this case, the stride for the slice is 16 bytes.
	BuildQuick( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}

void BVH::BuildQuick( const bvhvec4slice& vertices )
{
	BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::BuildQuick( .. ), primCount == 0." );
	// allocate on first build
	const uint32_t primCount = vertices.count / 3;
	const uint32_t spaceNeeded = primCount * 2; // upper limit
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 );	// node 1 remains unused, for cache line alignment.
		primIdx = (uint32_t*)AlignedAlloc( primCount * sizeof( uint32_t ) );
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	else BVH_FATAL_ERROR_IF( !rebuildable, "BVH::BuildQuick( .. ), bvh not rebuildable." );
	verts = vertices; // note: we're not copying this data; don't delete.
	idxCount = triCount = primCount;
	// reset node pool
	newNodePtr = 2;
	// assign all triangles to the root node
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = triCount, root.aabbMin = bvhvec3( BVH_FAR ), root.aabbMax = bvhvec3( -BVH_FAR );
	// initialize fragments and initialize root node bounds
	for (uint32_t i = 0; i < triCount; i++)
	{
		fragment[i].bmin = tinybvh_min( tinybvh_min( verts[i * 3], verts[i * 3 + 1] ), verts[i * 3 + 2] );
		fragment[i].bmax = tinybvh_max( tinybvh_max( verts[i * 3], verts[i * 3 + 1] ), verts[i * 3 + 2] );
		root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
		root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
	}
	// subdivide recursively
	uint32_t task[256], taskCount = 0, nodeIdx = 0;
	while (1)
	{
		while (1)
		{
			BVHNode& node = bvhNode[nodeIdx];
			// in-place partition against midpoint on longest axis
			uint32_t j = node.leftFirst + node.triCount, src = node.leftFirst;
			bvhvec3 extent = node.aabbMax - node.aabbMin;
			uint32_t axis = 0;
			if (extent.y > extent.x && extent.y > extent.z) axis = 1;
			if (extent.z > extent.x && extent.z > extent.y) axis = 2;
			float splitPos = node.aabbMin[axis] + extent[axis] * 0.5f, centroid;
			bvhvec3 lbmin( BVH_FAR ), lbmax( -BVH_FAR ), rbmin( BVH_FAR ), rbmax( -BVH_FAR ), fmin, fmax;
			for (uint32_t fi, i = 0; i < node.triCount; i++)
			{
				fi = primIdx[src], fmin = fragment[fi].bmin, fmax = fragment[fi].bmax;
				centroid = (fmin[axis] + fmax[axis]) * 0.5f;
				if (centroid < splitPos)
					lbmin = tinybvh_min( lbmin, fmin ), lbmax = tinybvh_max( lbmax, fmax ), src++;
				else
				{
					rbmin = tinybvh_min( rbmin, fmin ), rbmax = tinybvh_max( rbmax, fmax );
					tinybvh_swap( primIdx[src], primIdx[--j] );
				}
			}
			// create child nodes
			const uint32_t leftCount = src - node.leftFirst, rightCount = node.triCount - leftCount;
			if (leftCount == 0 || rightCount == 0 || taskCount == BVH_NUM_ELEMS( task )) break; // split did not work out.
			const int32_t lci = newNodePtr++, rci = newNodePtr++;
			bvhNode[lci].aabbMin = lbmin, bvhNode[lci].aabbMax = lbmax;
			bvhNode[lci].leftFirst = node.leftFirst, bvhNode[lci].triCount = leftCount;
			bvhNode[rci].aabbMin = rbmin, bvhNode[rci].aabbMax = rbmax;
			bvhNode[rci].leftFirst = j, bvhNode[rci].triCount = rightCount;
			node.leftFirst = lci, node.triCount = 0;
			// recurse
			task[taskCount++] = rci, nodeIdx = lci;
		}
		// fetch subdivision task from stack
		if (taskCount == 0) break; else nodeIdx = task[--taskCount];
	}
	// all done.
	aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
	refittable = true; // not using spatial splits: can refit this BVH
	may_have_holes = false; // the reference builder produces a continuous list of nodes
	usedNodes = newNodePtr;
}

// Basic single-function binned-SAH-builder.
// This is the reference builder; it yields a decent tree suitable for ray tracing on the CPU.
// This code uses no SIMD instructions. Faster code, using SSE/AVX, is available for x64 CPUs.
// For GPU rendering: The resulting BVH should be converted to a more optimal
// format after construction, e.g. BVH_GPU, BVH4_GPU or BVH8_CWBVH.
void BVH::Build( const bvhvec4* vertices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices:
	// in this case, the stride for the slice is 16 bytes.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) } );
}

void BVH::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH::Build( const bvhvec4slice& vertices ) { Build( vertices, 0, 0 ); }
void BVH::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
#ifdef SLICEDUMP
	// this code dumps the passed geometry data to a file - for debugging only.
	std::fstream df{ "dump.bin", df.binary | df.out };
	uint32_t vcount = vertices.count, indexed = indices == 0 ? 0 : 1, stride = vertices.stride;
	uint32_t pcount = indices ? prims : (vertices.count / 3);
	df.write( (char*)&pcount, 4 );
	df.write( (char*)&vcount, 4 );
	df.write( (char*)&stride, 4 );
	df.write( (char*)&indexed, sizeof( uint32_t ) );
	df.write( (char*)vertices.data, vertices.stride * vertices.count );
	if (indexed) df.write( (char*)indices, prims * 3 * 4 );
#endif
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	PrepareBuild( vertices, indices, prims );
	if (useFullSweep) BuildFullSweep(); else Build();
}

void BVH::BuildAABB( const bvhvec4* aabbs, const uint32_t primCount )
{
	BVH_FATAL_ERROR_IF( primCount == 0, " BVH::BuildAABB(const bvhvec4* aabbs, const uint32_t primCount), primCount == 0." );
	triCount = idxCount = primCount;
	const uint32_t spaceNeeded = primCount * 2; // upper limit
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 );	// node 1 remains unused, for cache line alignment.
		primIdx = (uint32_t*)AlignedAlloc( primCount * sizeof( uint32_t ) );
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	// copy relevant data from instance array
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = primCount, root.aabbMin = bvhvec3( BVH_FAR ), root.aabbMax = bvhvec3( -BVH_FAR );
	// * 2 since, primCount is the amount of aabbs and each aabb is made of 2 bvhvec4
	int realIndex = 0;
	for (uint32_t i = 0; i < primCount; i++)
	{
		//customGetAABB(i, fragment[i].bmin, fragment[i].bmax);
		fragment[i].bmin = bvhvec3( aabbs[realIndex] );
		fragment[i].bmax = bvhvec3( aabbs[realIndex + 1] );
		fragment[i].primIdx = i, fragment[i].clipped = 0, primIdx[i] = i;
		root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
		root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax );

		realIndex += 2;
	}
	// start build
	newNodePtr = 2;
	Build(); // or BuildAVX, for large TLAS.
}

void BVH::Build( void (*customGetAABB)(const unsigned, bvhvec3&, bvhvec3&), const uint32_t primCount )
{
	BVH_FATAL_ERROR_IF( primCount == 0, "BVH::Build( void (*customGetAABB)( .. ), instCount ), instCount == 0." );
	triCount = idxCount = primCount;
	const uint32_t spaceNeeded = primCount * 2; // upper limit
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 );	// node 1 remains unused, for cache line alignment.
		primIdx = (uint32_t*)AlignedAlloc( primCount * sizeof( uint32_t ) );
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	// copy relevant data from instance array
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = primCount, root.aabbMin = bvhvec3( BVH_FAR ), root.aabbMax = bvhvec3( -BVH_FAR );
	for (uint32_t i = 0; i < primCount; i++)
	{
		customGetAABB( i, fragment[i].bmin, fragment[i].bmax );
		fragment[i].primIdx = i, fragment[i].clipped = 0, primIdx[i] = i;
		root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
		root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax );
	}
	// start build
	newNodePtr = 2;
	Build(); // or BuildAVX, for large TLAS.
}

void BVH::Build( BLASInstance* instances, const uint32_t instCount, BVHBase** blasses, const uint32_t bCount )
{
	BVH_FATAL_ERROR_IF( instCount == 0, "BVH::Build( BLASInstance*, instCount ), instCount == 0." );
	triCount = idxCount = instCount;
	const uint32_t spaceNeeded = instCount * 2; // upper limit
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 );	// node 1 remains unused, for cache line alignment.
		primIdx = (uint32_t*)AlignedAlloc( instCount * sizeof( uint32_t ) );
		fragment = (Fragment*)AlignedAlloc( instCount * sizeof( Fragment ) );
	}
	instList = instances;
	blasList = blasses;
	blasCount = bCount;
	// copy relevant data from instance array
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = instCount, root.aabbMin = bvhvec3( BVH_FAR ), root.aabbMax = bvhvec3( -BVH_FAR );
	for (uint32_t i = 0; i < instCount; i++)
	{
		if (blasList) // if a null pointer is passed, we'll assume the BLASInstances have been updated elsewhere.
		{
			uint32_t blasIdx = instList[i].blasIdx;
			BVH* blas = (BVH*)blasList[blasIdx];
			instList[i].Update( blas );
		}
		fragment[i].bmin = instList[i].aabbMin, fragment[i].primIdx = i;
		fragment[i].bmax = instList[i].aabbMax, fragment[i].clipped = 0;
		root.aabbMin = tinybvh_min( root.aabbMin, instList[i].aabbMin );
		root.aabbMax = tinybvh_max( root.aabbMax, instList[i].aabbMax ), primIdx[i] = i;
	}
	// start build
	newNodePtr = 2;
	Build(); // or BuildAVX, for large TLAS.
}

float BVH::SplitPriority( const Fragment& f ) const
{
	const bvhvec3 extent = f.bmax - f.bmin;
	const float extentPrio = tinybvh_sqrf( extent[tinybvh_maxdim( extent )] );
	const float boxArea = 2 * tinybvh_halfarea( extent ); // TODO: half of this seems more appropriate?
	const float triArea = PrimArea( f.primIdx );
	const float emptyAreaPrio = boxArea - triArea;
	return cbrtf( extentPrio * emptyAreaPrio );
}

int BVH::SplitCount( const float prio, const float sumPrio, const int triCount, const float factor ) const
{
	const float shareOfTris = prio / sumPrio * triCount;
	return 1 + (int)(shareOfTris * presplitFactor);
}

float BVH::GetNodeSize( const float extent, const float globalSize )
{
	// transform into [0.0, 1.0]
	float alpha = extent / globalSize;
	// compute 2^(floor(log2(alpha)))
	uint32_t exponentBits = (*(uint32_t*)&alpha) & (255u << 23);
	// transform back into global space
	return *(float*)&exponentBits * globalSize;
}

uint32_t BVH::Presplit()
{
	// Based on Section 5 of "Fast Parallel Construction of High-Quality Bounding 
	// Volume Hierarchies", Karras and Aila, 2013, and BoyBaykiller's implementation,
	// which is in turn based on code by MadMann91.
	uint32_t fragCount = triCount;
	float factor = presplitFactor;
	const uint32_t splitBudget = (int)(triCount * presplitFactor);
	// determine per-triangle split count.
	float* prio = new float[triCount + splitBudget];
	int* splits = new int[triCount + splitBudget], s;
	while (1)
	{
		float summedPrio = 0, p;
		for (uint32_t i = 0; i < triCount; i++)
			p = SplitPriority( fragment[i] ),
			prio[i] = p, summedPrio += p;
		uint32_t totalSplits = 0;
		for (uint32_t i = 0; i < triCount; i++)
			s = SplitCount( prio[i], summedPrio, triCount, factor ),
			splits[i] = s, totalSplits += s;
		if (totalSplits <= triCount + splitBudget) break;
		factor *= 0.95f; // TODO: will this ever happen? also: tweak based on excess.
	}
	// do actual splitting.
	const BVHNode& root = bvhNode[0];
	const bvhvec3 rootExtent = root.aabbMax - root.aabbMin;
	ALIGNED( 64 ) Fragment part1, part2; // keep all clipping in a single cacheline.
	for (uint32_t i = 0; i < fragCount; ) if (splits[i] == 1) i++; else
	{
		const Fragment& f = fragment[i];
		const bvhvec3 extent = f.bmax - f.bmin;
		const int splitAxis = tinybvh_maxdim( extent );
		const float nodeSize = GetNodeSize( extent[splitAxis], rootExtent[splitAxis] );
		// snap mid position to nearest split plane
		const float midPos = (f.bmin[splitAxis] + f.bmax[splitAxis]) * 0.5f;
		const float index = roundf( (midPos - root.aabbMin[splitAxis]) / nodeSize );
		const float splitPos = root.aabbMin[splitAxis] + index * nodeSize;
		// actual split
		if (!SplitFrag( fragment[i], part1, part2, splitAxis, splitPos )) { i++; continue; /* split failed; rare. */ }
		fragment[i] = part1, fragment[fragCount] = part2;
		// distribute available splits over part1 and part2
		const int toDivide = splits[i];
		const bvhvec3 p1Extent = part1.bmax - part1.bmin, p2Extent = part2.bmax - part2.bmin;
		const float p1Size = p1Extent[tinybvh_maxdim( p1Extent )];
		const float p2Size = p2Extent[tinybvh_maxdim( p2Extent )];
		const int p1Count = (int)((float)toDivide * p1Size / (p1Size + p2Size));
		splits[i] = tinybvh_clamp( p1Count, 1, toDivide - 1 );
		splits[fragCount] = toDivide - splits[i], primIdx[fragCount] = fragCount++;
	}
	// cleanup
	delete prio;
	delete splits;
	// all done.
	return fragCount;
}

void BVH::PrepareBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t prims )
{
	const uint32_t primCount = prims > 0 ? prims : vertices.count / 3;
	const uint32_t splitBudget = usePresplitting ? ((int)(primCount * presplitFactor)) : 0;
	const uint32_t spaceNeeded = (primCount + splitBudget) * 2; // upper limit
	// allocate memory on first build
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 );	// node 1 remains unused, for cache line alignment.
		primIdx = (uint32_t*)AlignedAlloc( (primCount + splitBudget) * sizeof( uint32_t ) );
		if (vertices) fragment = (Fragment*)AlignedAlloc( (primCount + splitBudget) * sizeof( Fragment ) );
		else BVH_FATAL_ERROR_IF( fragment == 0, "BVH::PrepareBuild( 0, .. ), not called from ::Build( aabb )." );
	}
	else BVH_FATAL_ERROR_IF( !rebuildable, "BVH::PrepareBuild( .. ), bvh not rebuildable." );
	// set verts, vertIdx
	triCount = primCount;
	verts = vertices;
	vertIdx = (uint32_t*)indices;
	// prepare root node
	BVHNode& root = bvhNode[0];
	root.aabbMin = bvhvec3( BVH_FAR );
	root.aabbMax = bvhvec3( -BVH_FAR );
	// prepare fragments
	BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareBuild( .. ), empty vertex slice." );
	if (!indices)
	{
		BVH_FATAL_ERROR_IF( prims != 0, "BVH::PrepareBuild( .. ), indices == 0." );
		// building a BVH over triangles specified as three 16-byte vertices each.
		for (uint32_t i = 0; i < primCount; i++)
		{
			const bvhvec4 v0 = verts[i * 3], v1 = verts[i * 3 + 1], v2 = verts[i * 3 + 2];
			const bvhvec4 fmin = tinybvh_min( v0, tinybvh_min( v1, v2 ) );
			const bvhvec4 fmax = tinybvh_max( v0, tinybvh_max( v1, v2 ) );
			fragment[i].bmin = fmin, fragment[i].bmax = fmax, fragment[i].primIdx = i, fragment[i].clipped = 0;
			root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
			root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
		}
	}
	else
	{
		BVH_FATAL_ERROR_IF( prims == 0, "BVH::PrepareBuild( .. ), prims == 0." );
		// building a BVH over triangles consisting of vertices indexed by 'indices'.
		for (uint32_t i = 0; i < primCount; i++)
		{
			const uint32_t i0 = indices[i * 3], i1 = indices[i * 3 + 1], i2 = indices[i * 3 + 2];
			const bvhvec4 v0 = verts[i0], v1 = verts[i1], v2 = verts[i2];
			const bvhvec4 fmin = tinybvh_min( v0, tinybvh_min( v1, v2 ) );
			const bvhvec4 fmax = tinybvh_max( v0, tinybvh_max( v1, v2 ) );
			fragment[i].bmin = fmin, fragment[i].bmax = fmax, fragment[i].primIdx = i, fragment[i].clipped = 0;
			root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
			root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
		}
	}
	// presplitting
	uint32_t fragCount = usePresplitting ? Presplit() : primCount;
	// finalize root node
	root.leftFirst = 0;
	root.triCount = idxCount = triCount = fragCount;
	// reset node pool
	newNodePtr = 2;
	bvh_over_indices = indices != nullptr;
	// all set; actual build happens in BVH::Build.
}

#ifndef MT_USE_JOBSYSTEM
void Build_( uint32_t n, uint32_t d, BVH* bvh ) { bvh->Build( n, d ); }
#endif
void BVH::Build( uint32_t nodeIdx, uint32_t depth )
{
	// avoid threaded building for small meshes: not efficient; build multiple in parallel instead.
	if (depth == 0)
	{
	#ifdef NO_THREADED_BUILDS
		threadedBuild = false;
	#else
		if (triCount < MT_BUILD_THRESHOLD) threadedBuild = false; else
		{
		#ifdef MT_USE_JOBSYSTEM
			if (!globalSubtreeJobs) globalSubtreeJobs = new JobSystem();
			subtreeJobs = globalSubtreeJobs;
		#endif
			atomicNewNodePtr = new std::atomic<uint32_t>( newNodePtr );
		}
	#endif
	}
	// subdivide root node recursively
	uint32_t task[256], taskCount = 0;
	BVHNode& root = bvhNode[0];
	bvhvec3 minDim = (root.aabbMax - root.aabbMin) * 1e-20f;
	bvhvec3 bestLMin( 0 ), bestLMax( 0 ), bestRMin( 0 ), bestRMax( 0 );
	while (1)
	{
		while (1)
		{
			BVHNode& node = bvhNode[nodeIdx];
			const float SA = node.SurfaceArea();
			if (SA == 0) break; // can't split an infinitely small node.
			// find optimal object split
			bvhvec3 binMin[3][BVHBINS], binMax[3][BVHBINS];
			for (uint32_t a = 0; a < 3; a++) for (uint32_t i = 0; i < BVHBINS; i++)
				binMin[a][i] = bvhvec3( BVH_FAR ), binMax[a][i] = bvhvec3( -BVH_FAR );
			uint32_t count[3][BVHBINS] = { 0 };
			const bvhvec3 extent = node.aabbMax - node.aabbMin;
			const bvhvec3 nmin3 = node.aabbMin, rpd3 = bvhvec3(
				extent.x > minDim.x ? (BVHBINS / extent.x) : 0,
				extent.y > minDim.y ? (BVHBINS / extent.y) : 0,
				extent.z > minDim.z ? (BVHBINS / extent.z) : 0
			);
			for (uint32_t i = 0; i < node.triCount; i++) // process all tris for x,y and z at once
			{
				const uint32_t fi = primIdx[node.leftFirst + i];
				bvhint3 bi = bvhint3( ((fragment[fi].bmin + fragment[fi].bmax) * 0.5f - nmin3) * rpd3 );
				bi.x = tinybvh_clamp( bi.x, 0, BVHBINS - 1 );
				bi.y = tinybvh_clamp( bi.y, 0, BVHBINS - 1 );
				bi.z = tinybvh_clamp( bi.z, 0, BVHBINS - 1 );
				binMin[0][bi.x] = tinybvh_min( binMin[0][bi.x], fragment[fi].bmin );
				binMax[0][bi.x] = tinybvh_max( binMax[0][bi.x], fragment[fi].bmax ), count[0][bi.x]++;
				binMin[1][bi.y] = tinybvh_min( binMin[1][bi.y], fragment[fi].bmin );
				binMax[1][bi.y] = tinybvh_max( binMax[1][bi.y], fragment[fi].bmax ), count[1][bi.y]++;
				binMin[2][bi.z] = tinybvh_min( binMin[2][bi.z], fragment[fi].bmin );
				binMax[2][bi.z] = tinybvh_max( binMax[2][bi.z], fragment[fi].bmax ), count[2][bi.z]++;
			}
			// calculate per-split totals
			float splitCost = BVH_FAR;
			uint32_t bestAxis = 0, bestPos = 0;
			for (int32_t a = 0; a < 3; a++) if (extent[a] > minDim[a])
			{
				bvhvec3 lBMin[BVHBINS - 1], rBMin[BVHBINS - 1], l1( BVH_FAR ), l2( -BVH_FAR );
				bvhvec3 lBMax[BVHBINS - 1], rBMax[BVHBINS - 1], r1( BVH_FAR ), r2( -BVH_FAR );
				float ANL[BVHBINS - 1], ANR[BVHBINS - 1];
				for (uint32_t lN = 0, rN = 0, i = 0; i < BVHBINS - 1; i++)
				{
					lBMin[i] = l1 = tinybvh_min( l1, binMin[a][i] );
					rBMin[BVHBINS - 2 - i] = r1 = tinybvh_min( r1, binMin[a][BVHBINS - 1 - i] );
					lBMax[i] = l2 = tinybvh_max( l2, binMax[a][i] );
					rBMax[BVHBINS - 2 - i] = r2 = tinybvh_max( r2, binMax[a][BVHBINS - 1 - i] );
					lN += count[a][i], rN += count[a][BVHBINS - 1 - i];
					ANL[i] = lN == 0 ? BVH_FAR : (tinybvh_halfarea( l2 - l1 ) * (float)lN);
					ANR[BVHBINS - 2 - i] = rN == 0 ? BVH_FAR : (tinybvh_halfarea( r2 - r1 ) * (float)rN);
				}
				// evaluate bin totals to find best position for object split
				for (uint32_t i = 0; i < BVHBINS - 1; i++)
				{
					const float C = ANL[i] + ANR[i];
					if (C < splitCost)
					{
						splitCost = C, bestAxis = a, bestPos = i;
						bestLMin = lBMin[i], bestRMin = rBMin[i], bestLMax = lBMax[i], bestRMax = rBMax[i];
					}
				}
			}
			splitCost = c_trav + c_int * splitCost / SA;
			const float noSplitCost = (float)node.triCount * c_int;
			if (splitCost >= noSplitCost)
			{
				if (node.triCount > 512) printf( "Warning: failed to split large node (%i tris).\n", node.triCount );
				break; // not splitting is better.
			}
			// in-place partition
			uint32_t j = node.leftFirst + node.triCount, src = node.leftFirst;
			const float rpd = rpd3[bestAxis], nmin = nmin3[bestAxis];
			for (uint32_t i = 0; i < node.triCount; i++)
			{
				const uint32_t fi = primIdx[src];
				int32_t bi = (uint32_t)(((fragment[fi].bmin[bestAxis] + fragment[fi].bmax[bestAxis]) * 0.5f - nmin) * rpd);
				bi = tinybvh_clamp( bi, 0, BVHBINS - 1 );
				if ((uint32_t)bi <= bestPos) src++; else tinybvh_swap( primIdx[src], primIdx[--j] );
			}
			// create child nodes
			uint32_t leftCount = src - node.leftFirst, rightCount = node.triCount - leftCount;
			if (leftCount == 0 || rightCount == 0 || taskCount == BVH_NUM_ELEMS( task )) break; // should not happen.
			int32_t n;
			if (threadedBuild) n = atomicNewNodePtr->fetch_add( 2 ); else n = newNodePtr, newNodePtr += 2;
			bvhNode[n].aabbMin = bestLMin, bvhNode[n].aabbMax = bestLMax;
			bvhNode[n].leftFirst = node.leftFirst, bvhNode[n].triCount = leftCount;
			bvhNode[n + 1].aabbMin = bestRMin, bvhNode[n + 1].aabbMax = bestRMax;
			bvhNode[n + 1].leftFirst = j, bvhNode[n + 1].triCount = rightCount;
			node.leftFirst = n, node.triCount = 0;
		#ifdef MT_USE_JOBSYSTEM
			if (depth < MT_JOBSYSTEM_DEPTH && threadedBuild)
			{
				BVH* thisBVH = this; // avoid warnings / complexities of capturing this
				subtreeJobs->Execute( [=]() { thisBVH->Build( n, depth + 1 ); } );
				subtreeJobs->Execute( [=]() { thisBVH->Build( n + 1, depth + 1 ); } );
				break;
			}
		#else
			if (tinybvh_min( leftCount, rightCount ) >= (1 << 11) && threadedBuild)
			{
				std::thread t( &Build_, n, depth + 1, this );
				Build( n + 1, depth + 1 );
				t.join();
				break;
			}
		#endif
			task[taskCount++] = n + 1, nodeIdx = n;
		}
		// fetch subdivision task from stack
		if (taskCount == 0) break; else nodeIdx = task[--taskCount];
	}
	// all done.
	if (depth == 0)
	{
		if (threadedBuild)
		{
		#ifdef MT_USE_JOBSYSTEM
			subtreeJobs->Wait();
		#endif
			newNodePtr = atomicNewNodePtr->load();
			delete atomicNewNodePtr;
			atomicNewNodePtr = 0;
		}
		usedNodes = newNodePtr;
		aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
		refittable = usePresplitting ? false : true; // only if not using spatial splits
		may_have_holes = false; // the reference builder produces a continuous list of nodes
		bvh_over_aabbs = (verts == 0); // bvh over aabbs is suitable as TLAS
		if (usePresplitting)
		{
			// finalize indices in index array
			for (uint32_t i = 0; i < triCount; i++) primIdx[i] = fragment[primIdx[i]].primIdx;
		}
	}
}

// Full-sweep SAH builder.
// Instead of using binning, this builder evaluates all possible split plane
// candidates for each axis. Not efficient; e.g. sorting for each split can
// be prevented.
#ifndef MT_USE_JOBSYSTEM
void BuildFullSweep_( uint32_t n, uint32_t d, BVH* bvh ) { bvh->BuildFullSweep( n, d ); }
#endif
void BVH::BuildFullSweep( uint32_t nodeIdx, uint32_t depth )
{
	if (depth == 0)
	{
		// allocate data for O(N) stable partition
		flag = (uint8_t*)AlignedAlloc( triCount );
		for (int a = 0; a < 3; a++) sortedIdx[a] = (uint32_t*)AlignedAlloc( triCount * 4 );
		BVH* thisBVH = this;
		for (uint32_t a = 0; a < 3; a++)
			RadixSort( primIdx, sortedIdx[a], triCount, [=]( int index ) {
			return FloatToKey( thisBVH->fragment[index].bmin[a] + thisBVH->fragment[index].bmax[a] );
				} );
		// allocate space for right sweep
		SARs = (float*)AlignedAlloc( triCount * sizeof( float ) );
		// prepare threading
	#ifdef NO_THREADED_BUILDS
		threadedBuild = false;
	#else
		if (triCount < MT_BUILD_THRESHOLD) threadedBuild = false; else
		{
		#ifdef MT_USE_JOBSYSTEM
			if (!globalSubtreeJobs) globalSubtreeJobs = new JobSystem();
			subtreeJobs = globalSubtreeJobs;
		#endif
			atomicNewNodePtr = new std::atomic<uint32_t>( newNodePtr );
		}
	#endif
	}
	// subdivide root node recursively
	uint32_t task[256], taskCount = 0;
	bvhvec3 minDim = (bvhNode->aabbMax - bvhNode->aabbMin) * 1e-20f;
	while (1)
	{
		while (1)
		{
			BVHNode& node = bvhNode[nodeIdx];
			// update node bounds
			node.aabbMin = bvhvec3( BVH_FAR ), node.aabbMax = bvhvec3( -BVH_FAR );
			for (uint32_t i = 0; i < node.triCount; i++)
			{
				const uint32_t fi = sortedIdx[0][node.leftFirst + i];
				node.aabbMin = tinybvh_min( node.aabbMin, fragment[fi].bmin );
				node.aabbMax = tinybvh_max( node.aabbMax, fragment[fi].bmax );
			}
			if (node.triCount == 1) break; // can't split one triangle.
			const float rSAV = 1.0f / node.SurfaceArea();
			const bvhvec3 extent = node.aabbMax - node.aabbMin;
			// iterate over x,y,z
			float splitCost = (float)node.triCount;
			uint32_t splitAxis = 0, splitPos = 0;
			for (uint32_t a = 0; a < 3; a++) if (extent[a] > minDim[a])
			{
				// sweep from right to left
				bvhvec3 Rmin( BVH_FAR ), Rmax( -BVH_FAR );
				uint32_t firstRightTri = 1;
				for (uint32_t i = 0; i < node.triCount; i++)
				{
					const uint32_t fi = sortedIdx[a][node.leftFirst + node.triCount - i - 1];
					const float SAR = (float)i * tinybvh_halfarea( Rmax - Rmin ) * rSAV;
					SARs[node.leftFirst + node.triCount - i - 1] = SAR;
					Rmin = tinybvh_min( Rmin, fragment[fi].bmin );
					Rmax = tinybvh_max( Rmax, fragment[fi].bmax );
					if (SAR >= splitCost)
					{
						// right side's cost is already greater than lowest cost and will only increase. Stop early
						firstRightTri = node.triCount - i;
						break;
					}
				}
				// sweep from left to right
				bvhvec3 Lmin( BVH_FAR ), Lmax( -BVH_FAR );
				for (uint32_t i = 0; i < firstRightTri - 1; i++)
				{
					const uint32_t fi = sortedIdx[a][node.leftFirst + i];
					Lmin = tinybvh_min( Lmin, fragment[fi].bmin );
					Lmax = tinybvh_max( Lmax, fragment[fi].bmax );
				}
				for (uint32_t i = firstRightTri - 1; i < node.triCount - 1; i++)
				{
					const uint32_t fi = sortedIdx[a][node.leftFirst + i];
					Lmin = tinybvh_min( Lmin, fragment[fi].bmin );
					Lmax = tinybvh_max( Lmax, fragment[fi].bmax );
					const float SAL = (float)(i + 1) * tinybvh_halfarea( Lmax - Lmin ) * rSAV;
					const float C = SAL + SARs[node.leftFirst + i];
					if (C < splitCost) splitCost = C, splitPos = i + 1, splitAxis = a;
					else if (SAL >= splitCost) break;
				}
			}
			float noSplitCost = c_int * (float)node.triCount;
			splitCost = c_trav + (c_int * splitCost);
			if (splitCost >= noSplitCost) break; // not splitting turns out to be better.
			// partition
			for (uint32_t i = 0; i < splitPos; i++) flag[sortedIdx[splitAxis][node.leftFirst + i]] = 0; // "left"
			for (uint32_t i = splitPos; i < node.triCount; i++) flag[sortedIdx[splitAxis][node.leftFirst + i]] = 1; // "right"

			// stable partition needs temp buffer, let's reuse memory
			uint32_t* tmp = (uint32_t*)SARs;
			for (uint32_t a = 0; a < 3; a++) if (a != splitAxis)
			{
				int p0 = 0, p1 = 0;
				for (uint32_t i = 0; i < node.triCount; i++)
				{
					const uint32_t fi = sortedIdx[a][node.leftFirst + i];
					if (flag[fi]) tmp[node.leftFirst + p1++] = fi; else sortedIdx[a][node.leftFirst + p0++] = fi;
				}
				memcpy( &sortedIdx[a][node.leftFirst + p0], tmp + node.leftFirst, p1 * 4 );
			}
			// create child nodes
			uint32_t leftCount = splitPos, rightCount = node.triCount - leftCount;
			if (leftCount >= node.triCount || rightCount >= node.triCount || taskCount == BVH_NUM_ELEMS( task )) break;
			memcpy( primIdx + node.leftFirst, sortedIdx[splitAxis] + node.leftFirst, node.triCount * 4 );
			int32_t n;
			if (threadedBuild) n = atomicNewNodePtr->fetch_add( 2 ); else n = newNodePtr, newNodePtr += 2;
			bvhNode[n].leftFirst = node.leftFirst;
			bvhNode[n].triCount = leftCount;
			bvhNode[n + 1].leftFirst = node.leftFirst + leftCount;
			bvhNode[n + 1].triCount = rightCount;
			node.leftFirst = n, node.triCount = 0;
		#ifdef MT_USE_JOBSYSTEM
			if (threadedBuild && depth < MT_JOBSYSTEM_DEPTH)
			{
				BVH* thisBVH = this; // avoid warnings / complexities of capturing this
				subtreeJobs->Execute( [=]() { thisBVH->BuildFullSweep( n, depth + 1 ); } );
				subtreeJobs->Execute( [=]() { thisBVH->BuildFullSweep( n + 1, depth + 1 ); } );
				break;
			}
		#else
			if (tinybvh_min( leftCount, rightCount ) >= (1 << 11) && threadedBuild)
			{
				std::thread t( &BuildFullSweep_, n, depth + 1, this );
				BuildFullSweep( n + 1, depth + 1 );
				t.join();
				break;
			}
		#endif
			// recurse
			task[taskCount++] = n + 1, nodeIdx = n;
		}
		// fetch subdivision task from stack
		if (taskCount == 0) break; else nodeIdx = task[--taskCount];
	}
	// cleanup allocated buffers when done
	if (depth == 0)
	{
		if (threadedBuild)
		{
		#ifdef MT_USE_JOBSYSTEM
			subtreeJobs->Wait();
		#endif
			newNodePtr = atomicNewNodePtr->load();
			delete atomicNewNodePtr;
			atomicNewNodePtr = 0;
		}
		for (int a = 0; a < 3; a++) AlignedFree( sortedIdx[a] );
		AlignedFree( SARs );
		AlignedFree( flag );
		aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
		refittable = true; // not using spatial splits: can refit this BVH
		may_have_holes = false; // this builder produces a continuous list of nodes
		bvh_over_aabbs = (verts == 0); // bvh over aabbs is suitable as TLAS
		usedNodes = newNodePtr;
	}
}

// SBVH builder.
// Besides the regular object splits used in the reference builder, the SBVH
// algorithm also considers spatial splits, where primitives may be cut in
// multiple parts. This increases primitive count but may reduce overlap of
// BVH nodes. The cost of each option is considered per split.
// For typical geometry, SBVH yields a tree that can be traversed 25% faster.
// This comes at greatly increased construction cost, making the SBVH
// primarily useful for static geometry.
void BVH::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}

void BVH::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	BuildHQ( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH::BuildHQ( const bvhvec4slice& vertices )
{
	PrepareHQBuild( vertices, 0, 0 );
	BuildHQ();
}

void BVH::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	PrepareHQBuild( vertices, indices, prims );
	BuildHQ();
}

void BVH::PrepareHQBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t prims )
{
	BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareHQBuild( .. ), zero primitives." );
	uint32_t primCount = prims > 0 ? prims : vertices.count / 3;
	const uint32_t slack = primCount >> 1; // for split prims
	const uint32_t spaceNeeded = primCount * 3;
	// allocate memory on first build
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 );	// node 1 remains unused, for cache line alignment.
		primIdx = (uint32_t*)AlignedAlloc( (primCount + slack) * sizeof( uint32_t ) );
		fragment = (Fragment*)AlignedAlloc( (primCount + slack) * sizeof( Fragment ) );
	}
	else BVH_FATAL_ERROR_IF( !rebuildable, "BVH::PrepareHQBuild( .. ), bvh not rebuildable." );
	verts = vertices; // note: we're not copying this data; don't delete.
	idxCount = primCount + slack, triCount = primCount, vertIdx = (uint32_t*)indices;
	// prepare fragments
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = triCount, root.aabbMin = bvhvec3( BVH_FAR ), root.aabbMax = bvhvec3( -BVH_FAR );
	if (!indices)
	{
		BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareHQBuild( .. ), primCount == 0." );
		BVH_FATAL_ERROR_IF( prims != 0, "BVH::PrepareHQBuild( .. ), indices == 0." );
		// building a BVH over triangles specified as three 16-byte vertices each.
		for (uint32_t i = 0; i < triCount; i++)
		{
			const bvhvec4 v0 = verts[i * 3], v1 = verts[i * 3 + 1], v2 = verts[i * 3 + 2];
			const bvhvec4 fmin = tinybvh_min( v0, tinybvh_min( v1, v2 ) );
			const bvhvec4 fmax = tinybvh_max( v0, tinybvh_max( v1, v2 ) );
			fragment[i].bmin = fmin, fragment[i].bmax = fmax, fragment[i].primIdx = i, fragment[i].clipped = 0;
			root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
			root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
		}
	}
	else
	{
		BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareHQBuild( .. ), empty vertex slice." );
		BVH_FATAL_ERROR_IF( prims == 0, "BVH::PrepareHQBuild( .. ), prims == 0." );
		// building a BVH over triangles consisting of vertices indexed by 'indices'.
		for (uint32_t i = 0; i < triCount; i++)
		{
			const uint32_t i0 = indices[i * 3], i1 = indices[i * 3 + 1], i2 = indices[i * 3 + 2];
			const bvhvec4 v0 = verts[i0], v1 = verts[i1], v2 = verts[i2];
			const bvhvec4 fmin = tinybvh_min( v0, tinybvh_min( v1, v2 ) );
			const bvhvec4 fmax = tinybvh_max( v0, tinybvh_max( v1, v2 ) );
			fragment[i].bmin = fmin, fragment[i].bmax = fmax, fragment[i].primIdx = i, fragment[i].clipped = 0;
			root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
			root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
		}
	}
	// clear remainder of index array
	memset( primIdx + triCount, 0, slack * 4 );
	bvh_over_indices = indices != nullptr;
	// threading
#ifdef NO_THREADED_BUILDS
	threadedBuild = false;
#else
	if (primCount < MT_BUILD_THRESHOLD) threadedBuild = false;
#endif
	// all set; actual build happens in BVH::BuildHQ.
}

float BVH::SplitCostSAH( const float rAparent, const float Aleft, const int Nleft, const float Aright, const int Nright ) const
{
	const int lN = l_quads ? (((Nleft + 3) >> 2) * 4) : Nleft;
	const int rN = l_quads ? (((Nright + 3) >> 2) * 4) : Nright;
	return c_trav + c_int * rAparent * (Aleft * (float)lN + Aright * (float)rN);
}

float BVH::NoSplitCostSAH( const int Nparent ) const
{
	return (float)(l_quads ? (((Nparent + 3) >> 2) * 4) : Nparent) * c_int;
}

#ifndef MT_USE_JOBSYSTEM
void BuildHQTask_(
	uint32_t n, uint32_t d, uint32_t s0, uint32_t s1, uint32_t* t,
	BVH* bvh ) {
	bvh->BuildHQTask( n, d, s0, s1, t );
}
#endif
void BVH::BuildHQTask( uint32_t nodeIdx, uint32_t depth,
	uint32_t sliceStart, uint32_t sliceEnd, uint32_t* idxTmp
)
{
	// prepare subdivision
	ALIGNED( 64 ) SubdivTask localTask[512];
	uint32_t localTasks = 0;
	bvhvec3 bestLMin( 0 ), bestLMax( 0 ), bestRMin( 0 ), bestRMax( 0 );
	BVHNode& root = bvhNode[0];
	const float rootArea = tinybvh_halfarea( root.aabbMax - root.aabbMin );
	const bvhvec3 minDim = (root.aabbMax - root.aabbMin) * 1e-7f /* don't touch, carefully picked */;
	// subdivide
	uint32_t binCount = hqbvhbins;
	while (1)
	{
		while (1)
		{
			// fetch node to subdivide
			BVHNode& node = bvhNode[nodeIdx];
			// alternating bin counts for optimizer.
			if (hqbvhoddeven) binCount = hqbvhbins + (depth & 1); // odd levels get one more
			// find optimal object split
			bvhvec3 binMin[3][MAXHQBINS], binMax[3][MAXHQBINS];
			for (uint32_t a = 0; a < 3; a++) for (uint32_t i = 0; i < binCount; i++)
				binMin[a][i] = bvhvec3( BVH_FAR ), binMax[a][i] = bvhvec3( -BVH_FAR );
			uint32_t count[3][MAXHQBINS];
			for (uint32_t i = 0; i < 3; i++) memset( count[i], 0, binCount * 4 );
			const bvhvec3 rpd3 = bvhvec3( bvhvec3( (float)binCount ) / (node.aabbMax - node.aabbMin) ), nmin3 = node.aabbMin;
			for (uint32_t i = 0; i < node.triCount; i++) // process all tris for x,y and z at once
			{
				const uint32_t fi = primIdx[node.leftFirst + i];
				bvhint3 bi = bvhint3( ((fragment[fi].bmin + fragment[fi].bmax) * 0.5f - nmin3) * rpd3 );
				bi.x = tinybvh_clamp( bi.x, 0, binCount - 1 );
				bi.y = tinybvh_clamp( bi.y, 0, binCount - 1 );
				bi.z = tinybvh_clamp( bi.z, 0, binCount - 1 );
				binMin[0][bi.x] = tinybvh_min( binMin[0][bi.x], fragment[fi].bmin );
				binMax[0][bi.x] = tinybvh_max( binMax[0][bi.x], fragment[fi].bmax ), count[0][bi.x]++;
				binMin[1][bi.y] = tinybvh_min( binMin[1][bi.y], fragment[fi].bmin );
				binMax[1][bi.y] = tinybvh_max( binMax[1][bi.y], fragment[fi].bmax ), count[1][bi.y]++;
				binMin[2][bi.z] = tinybvh_min( binMin[2][bi.z], fragment[fi].bmin );
				binMax[2][bi.z] = tinybvh_max( binMax[2][bi.z], fragment[fi].bmax ), count[2][bi.z]++;
			}
			// calculate per-split totals
			float noSplitCost = NoSplitCostSAH( node.triCount );
			float splitCost = noSplitCost, rSAV = 1.0f / node.SurfaceArea();
			uint32_t bestAxis = 0, bestPos = 0;
			for (int32_t a = 0; a < 3; a++) if ((node.aabbMax[a] - node.aabbMin[a]) > minDim[a])
			{
				bvhvec3 lBMin[MAXHQBINS - 1], rBMin[MAXHQBINS - 1], l1( BVH_FAR ), l2( -BVH_FAR );
				bvhvec3 lBMax[MAXHQBINS - 1], rBMax[MAXHQBINS - 1], r1( BVH_FAR ), r2( -BVH_FAR );
				float AL[MAXHQBINS - 1], AR[MAXHQBINS - 1];		// left and right area per split plane
				int NL[MAXHQBINS - 1], NR[MAXHQBINS - 1];		// summed left and right tricount
				for (uint32_t lN = 0, rN = 0, i = 0; i < binCount - 1; i++)
				{
					lBMin[i] = l1 = tinybvh_min( l1, binMin[a][i] );
					rBMin[binCount - 2 - i] = r1 = tinybvh_min( r1, binMin[a][binCount - 1 - i] );
					lBMax[i] = l2 = tinybvh_max( l2, binMax[a][i] );
					rBMax[binCount - 2 - i] = r2 = tinybvh_max( r2, binMax[a][binCount - 1 - i] );
					lN += count[a][i], rN += count[a][binCount - 1 - i];
					NL[i] = lN, NR[binCount - 2 - i] = rN;
					AL[i] = lN == 0 ? BVH_FAR : tinybvh_halfarea( l2 - l1 );
					AR[binCount - 2 - i] = rN == 0 ? BVH_FAR : tinybvh_halfarea( r2 - r1 );
				}
				// evaluate bin totals to find best position for object split
				for (uint32_t i = 0; i < binCount - 1; i++)
				{
					const float C = SplitCostSAH( rSAV, AL[i], NL[i], AR[i], NR[i] );
					if (C >= splitCost) continue;
					splitCost = C, bestAxis = a, bestPos = i;
					bestLMin = lBMin[i], bestRMin = rBMin[i], bestLMax = lBMax[i], bestRMax = rBMax[i];
				}
			}
			// consider a spatial split
			bool spatial = false;
			int bestNL = 0, bestNR = 0, budget = (int)(sliceEnd - sliceStart);
			bvhvec3 spatialUnion = bestLMax - bestRMin;
			float spatialOverlap = (tinybvh_halfarea( spatialUnion )) / rootArea;
			if (budget > (int)node.triCount && (spatialOverlap > 1e-4f || splitCost >= noSplitCost))
			{
				float minSplitCost = splitCost * 0.985f; // don't accept a spatial split for minimal gain
				for (int a = 0; a < 3; a++) if ((node.aabbMax[a] - node.aabbMin[a]) > minDim[a])
				{
					// setup bins
					bvhvec3 sbinMin[MAXHQBINS], sbinMax[MAXHQBINS];
					int countIn[MAXHQBINS], countOut[MAXHQBINS];
					memset( countIn, 0, binCount * 4 );
					memset( countOut, 0, binCount * 4 );
					for (uint32_t i = 0; i < binCount; i++) sbinMin[i] = bvhvec3( BVH_FAR ), sbinMax[i] = bvhvec3( -BVH_FAR );
					// populate bins with clipped fragments
					const float planeDist = (node.aabbMax[a] - node.aabbMin[a]) / (binCount * 0.9999f);
					const float rPlaneDist = 1.0f / planeDist, nodeMin = node.aabbMin[a];
					for (unsigned i = 0; i < node.triCount; i++)
					{
						const uint32_t fi = primIdx[node.leftFirst + i];
						const int bin1 = tinybvh_clamp( (int32_t)((fragment[fi].bmin[a] - nodeMin) * rPlaneDist), 0, binCount - 1 );
						const int bin2 = tinybvh_clamp( (int32_t)((fragment[fi].bmax[a] - nodeMin) * rPlaneDist), 0, binCount - 1 );
						countIn[bin1]++, countOut[bin2]++;
						if (bin2 == bin1) // fragment fits in a single bin
							sbinMin[bin1] = tinybvh_min( sbinMin[bin1], fragment[fi].bmin ),
							sbinMax[bin1] = tinybvh_max( sbinMax[bin1], fragment[fi].bmax );
						else for (int j = bin1; j <= bin2; j++)
						{
							// clip fragment to each bin it overlaps
							bvhvec3 bmin = node.aabbMin, bmax = node.aabbMax;
							bmin[a] = nodeMin + planeDist * j;
							bmax[a] = j == (int)(binCount - 2) ? node.aabbMax[a] : (bmin[a] + planeDist);
							Fragment orig = fragment[fi];
							Fragment tmpFrag;
							if (!ClipFrag( orig, tmpFrag, bmin, bmax, a )) continue;
							sbinMin[j] = tinybvh_min( sbinMin[j], tmpFrag.bmin );
							sbinMax[j] = tinybvh_max( sbinMax[j], tmpFrag.bmax );
						}
					}
					// evaluate split candidates
					bvhvec3 lBMin[MAXHQBINS - 1], rBMin[MAXHQBINS - 1], l1( BVH_FAR ), l2( -BVH_FAR );
					bvhvec3 lBMax[MAXHQBINS - 1], rBMax[MAXHQBINS - 1], r1( BVH_FAR ), r2( -BVH_FAR );
					float AL[MAXHQBINS], AR[MAXHQBINS];
					int NL[MAXHQBINS], NR[MAXHQBINS];
					for (uint32_t lN = 0, rN = 0, i = 0; i < binCount - 1; i++)
					{
						lBMin[i] = l1 = tinybvh_min( l1, sbinMin[i] ), rBMin[binCount - 2 - i] = r1 = tinybvh_min( r1, sbinMin[binCount - 1 - i] );
						lBMax[i] = l2 = tinybvh_max( l2, sbinMax[i] ), rBMax[binCount - 2 - i] = r2 = tinybvh_max( r2, sbinMax[binCount - 1 - i] );
						lN += countIn[i], rN += countOut[binCount - 1 - i];
						AL[i] = lN == 0 ? BVH_FAR : tinybvh_halfarea( l2 - l1 );
						AR[binCount - 2 - i] = rN == 0 ? BVH_FAR : tinybvh_halfarea( r2 - r1 );
						NL[i] = lN, NR[binCount - 2 - i] = rN;
					}
					// find best position for spatial split
					for (uint32_t i = 0; i < binCount - 1; i++)
					{
						const float Cspatial = SplitCostSAH( rSAV, AL[i], NL[i], AR[i], NR[i] );
						if (Cspatial < minSplitCost && NL[i] + NR[i] < budget && NL[i] * NR[i] > 0)
						{
							spatial = true, minSplitCost = splitCost = Cspatial, bestAxis = a, bestPos = i;
							bestLMin = lBMin[i], bestLMax = lBMax[i], bestRMin = rBMin[i], bestRMax = rBMax[i];
							bestNL = NL[i], bestNR = NR[i]; // for unsplitting
							bestLMax[a] = bestRMin[a]; // accurate
						}
					}
				}
			}
			// evaluate best split cost
			if (splitCost >= noSplitCost)
			{
				for (uint32_t i = 0; i < node.triCount; i++)
					primIdx[node.leftFirst + i] = fragment[primIdx[node.leftFirst + i]].primIdx;
				break; // not splitting is better.
			}
			// double-buffered partition
			uint32_t A = sliceStart, B = sliceEnd, src = node.leftFirst;
			if (spatial)
			{
				// spatial partitioning
				const float planeDist = (node.aabbMax[bestAxis] - node.aabbMin[bestAxis]) / (binCount * 0.9999f);
				const float rPlaneDist = 1.0f / planeDist, nodeMin = node.aabbMin[bestAxis];
				for (uint32_t i = 0; i < node.triCount; i++)
				{
					const uint32_t fragIdx = primIdx[src++];
					const uint32_t bin1 = (uint32_t)tinybvh_max( (fragment[fragIdx].bmin[bestAxis] - nodeMin) * rPlaneDist, 0.0f );
					const uint32_t bin2 = (uint32_t)tinybvh_max( (fragment[fragIdx].bmax[bestAxis] - nodeMin) * rPlaneDist, 0.0f );
					if (bin2 <= bestPos) idxTmp[A++] = fragIdx; else if (bin1 > bestPos) idxTmp[--B] = fragIdx; else
					{
					#if defined SBVH_UNSPLITTING
						// unsplitting: 1. Calculate what happens if we add this primitive entirely to the left side
						if (bestNR > 1)
						{
							bvhvec3 unsplitLMin = tinybvh_min( bestLMin, fragment[fragIdx].bmin );
							bvhvec3 unsplitLMax = tinybvh_max( bestLMax, fragment[fragIdx].bmax );
							float AL = tinybvh_halfarea( unsplitLMax - unsplitLMin );
							float AR = tinybvh_halfarea( bestRMax - bestRMin );
							float CunsplitLeft = SplitCostSAH( rSAV, AL, bestNL, AR, bestNR - 1 );
							if (CunsplitLeft <= splitCost)
							{
								bestNR--, splitCost = CunsplitLeft, idxTmp[A++] = fragIdx;
								bestLMin = unsplitLMin, bestLMax = unsplitLMax;
								continue;
							}
						}
						// 2. Calculate what happens if we add this primitive entirely to the right side
						if (bestNL > 1)
						{
							const bvhvec3 unsplitRMin = tinybvh_min( bestRMin, fragment[fragIdx].bmin );
							const bvhvec3 unsplitRMax = tinybvh_max( bestRMax, fragment[fragIdx].bmax );
							const float AL = tinybvh_halfarea( bestLMax - bestLMin );
							const float AR = tinybvh_halfarea( unsplitRMax - unsplitRMin );
							const float CunsplitRight = SplitCostSAH( rSAV, AL, bestNL - 1, AR, bestNR );
							if (CunsplitRight <= splitCost)
							{
								bestNL--, splitCost = CunsplitRight, idxTmp[--B] = fragIdx;
								bestRMin = unsplitRMin, bestRMax = unsplitRMax;
								continue;
							}
						}
					#endif
						// split straddler
						ALIGNED( 64 ) Fragment part1, part2; // keep all clipping in a single cacheline.
						float splitPos = bestLMax[bestAxis];
						if (SplitFrag( fragment[fragIdx], part1, part2, bestAxis, splitPos ))
						{
							uint32_t newFragIdx = threadedBuild ? atomicNextFrag->fetch_add( 1 ) : nextFrag++;
							fragment[fragIdx] = part1, idxTmp[A++] = fragIdx,
								fragment[newFragIdx] = part2, idxTmp[--B] = newFragIdx;
						}
						else // didn't work out; see what we can do.
						{
							const float sahLeft = tinybvh_halfarea( part1.bmax - part1.bmin );
							const float sahRight = tinybvh_halfarea( part2.bmax - part2.bmin );
							if (sahLeft > 0) idxTmp[A++] = fragIdx; else idxTmp[--B] = fragIdx;
						}
					}
				}
				// for spatial splits, we fully refresh the bounds: clipping is never fully stable..
				bestLMin = bestRMin = bvhvec3( BVH_FAR ), bestLMax = bestRMax = bvhvec3( -BVH_FAR );
				for (uint32_t i = sliceStart; i < A; i++)
					bestLMin = tinybvh_min( bestLMin, fragment[idxTmp[i]].bmin ),
					bestLMax = tinybvh_max( bestLMax, fragment[idxTmp[i]].bmax );
				for (uint32_t i = B; i < sliceEnd; i++)
					bestRMin = tinybvh_min( bestRMin, fragment[idxTmp[i]].bmin ),
					bestRMax = tinybvh_max( bestRMax, fragment[idxTmp[i]].bmax );
			}
			else
			{
				// object partitioning
				const float rpd = rpd3[bestAxis], nmin = nmin3[bestAxis];
				for (uint32_t i = 0; i < node.triCount; i++)
				{
					const uint32_t fr = primIdx[src + i];
					int32_t bi = (int32_t)(((fragment[fr].bmin[bestAxis] + fragment[fr].bmax[bestAxis]) * 0.5f - nmin) * rpd);
					bi = tinybvh_clamp( bi, 0, binCount - 1 );
					if (bi <= (int32_t)bestPos) idxTmp[A++] = fr; else idxTmp[--B] = fr;
				}
			}
			// copy back slice data
			memcpy( primIdx + sliceStart, idxTmp + sliceStart, (sliceEnd - sliceStart) * 4 );
			// create child nodes
			uint32_t leftCount = A - sliceStart, rightCount = sliceEnd - B;
			if (leftCount == 0 || rightCount == 0)
			{
				// spatial split failed. We shouldn't get here, but we do sometimes..
				for (uint32_t i = 0; i < node.triCount; i++)
					primIdx[node.leftFirst + i] = fragment[primIdx[node.leftFirst + i]].primIdx;
				node.aabbMin = tinybvh_min( bestLMin, bestRMin );
				node.aabbMax = tinybvh_max( bestLMax, bestRMax );
				break;
			}
			int32_t leftChildIdx;
			if (threadedBuild) leftChildIdx = atomicNewNodePtr->fetch_add( 2 ); else leftChildIdx = newNodePtr, newNodePtr += 2;
			int32_t rightChildIdx = leftChildIdx + 1;
			bvhNode[leftChildIdx].aabbMin = bestLMin, bvhNode[leftChildIdx].aabbMax = bestLMax;
			bvhNode[leftChildIdx].leftFirst = sliceStart, bvhNode[leftChildIdx].triCount = leftCount;
			bvhNode[rightChildIdx].aabbMin = bestRMin, bvhNode[rightChildIdx].aabbMax = bestRMax;
			bvhNode[rightChildIdx].leftFirst = B, bvhNode[rightChildIdx].triCount = rightCount;
			node.leftFirst = leftChildIdx, node.triCount = 0;
			// recurse
		#ifdef MT_USE_JOBSYSTEM
			if (depth < MT_JOBSYSTEM_DEPTH && threadedBuild)
			{
				// spawn a new thread for the right branch
				BVH* thisBVH = this; // avoid warnings / complexities of capturing this
				subtreeJobs->Execute( [=]() { thisBVH->BuildHQTask( leftChildIdx, depth + 1, sliceStart, (A + B) >> 1, idxTmp ); } );
				subtreeJobs->Execute( [=]() { thisBVH->BuildHQTask( rightChildIdx, depth + 1, (A + B) >> 1, sliceEnd, idxTmp ); } );
				break;
			}
		#else
			if (tinybvh_min( leftCount, rightCount ) >= (1 << 11) && threadedBuild)
			{
				std::thread t( &BuildHQTask_, leftChildIdx, depth + 1, sliceStart, (A + B) >> 1, idxTmp, this );
				BuildHQTask( rightChildIdx, depth + 1, (A + B) >> 1, sliceEnd, idxTmp );
				t.join();
				break;
			}
		#endif
			// proceed with left child, push right child on local stack
			localTask[localTasks].node = rightChildIdx, localTask[localTasks].depth = depth;
			localTask[localTasks].sliceStart = (A + B) >> 1, localTask[localTasks++].sliceEnd = sliceEnd;
			nodeIdx = leftChildIdx, sliceEnd = (A + B) >> 1;
		}
		// pop a local task, if any are left
		if (localTasks == 0) break;
		nodeIdx = localTask[--localTasks].node, depth = localTask[localTasks].depth;
		sliceStart = localTask[localTasks].sliceStart, sliceEnd = localTask[localTasks].sliceEnd;
	}
	// all done.
#ifdef MT_USE_JOBSYSTEM
	if (depth == 0 && threadedBuild) subtreeJobs->Wait();
#endif
}

void BVH::BuildHQ()
{
	const uint32_t slack = triCount >> 1; // for split prims
	uint32_t* idxTmp = (uint32_t*)AlignedAlloc( (triCount + slack) * sizeof( uint32_t ) );
	memset( idxTmp, 0, (triCount + slack) * 4 );
	// reset node pool
	if (threadedBuild)
	{
	#ifdef MT_USE_JOBSYSTEM
		if (!globalSubtreeJobs) globalSubtreeJobs = new JobSystem();
		subtreeJobs = globalSubtreeJobs;
	#endif
		atomicNewNodePtr = new std::atomic<uint32_t>( 2 );
		atomicNextFrag = new std::atomic<uint32_t>( triCount );
	}
	else
	{
		newNodePtr = 2;
		nextFrag = triCount;
	}
	// subdivide recursively
	uint32_t nodeIdx = 0, sliceStart = 0, sliceEnd = triCount + slack, depth = 0;
	BuildHQTask( nodeIdx, depth, sliceStart, sliceEnd, idxTmp );
	// all done.
	AlignedFree( idxTmp );
	aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
	refittable = false; // can't refit an SBVH
	may_have_holes = false; // there may be holes in the index list, but not in the node list
	if (threadedBuild)
	{
		newNodePtr = atomicNewNodePtr->load();
		nextFrag = atomicNextFrag->load();
	}
	usedNodes = newNodePtr;
	delete atomicNewNodePtr;
	delete atomicNextFrag;
	atomicNewNodePtr = 0;
	atomicNextFrag = 0;
	Compact();
}

// Optimize: Will happen via BVH_Verbose.
void BVH::Optimize( const uint32_t iterations, bool extreme, bool stochastic )
{
	BVH_Verbose* verbose = new BVH_Verbose();
	verbose->ConvertFrom( *this );
	verbose->Optimize( iterations, extreme, stochastic );
	ConvertFrom( *verbose );
}

// Refitting: For animated meshes, where the topology remains intact. This
// includes trees waving in the wind, or subsequent frames for skinned
// animations. Repeated refitting tends to lead to deteriorated BVHs and
// slower ray tracing. Rebuild when this happens.
void BVH::Refit( const uint32_t /* unused */ )
{
	BVH_FATAL_ERROR_IF( !refittable, "BVH::Refit( .. ), refitting an SBVH or pre-splitted BVH." );
	BVH_FATAL_ERROR_IF( bvhNode == 0, "BVH::Refit( .. ), bvhNode == 0." );
	BVH_FATAL_ERROR_IF( may_have_holes, "BVH::Refit( .. ), bvh may have holes." );
	BVH_FATAL_ERROR_IF( isTLAS(), "BVH::Refit( .. ), do not refit a TLAS, use Build(..)." );
	for (int32_t i = usedNodes - 1; i >= 0; i--) if (i != 1)
	{
		BVHNode& node = bvhNode[i];
		if (node.isLeaf()) // leaf: adjust to current triangle vertex positions
		{
			bvhvec4 bmin( BVH_FAR ), bmax( -BVH_FAR );
			if (vertIdx) for (uint32_t first = node.leftFirst, j = 0; j < node.triCount; j++)
			{
				const uint32_t vidx = primIdx[first + j] * 3;
				const uint32_t i0 = vertIdx[vidx], i1 = vertIdx[vidx + 1], i2 = vertIdx[vidx + 2];
				const bvhvec4 v0 = verts[i0], v1 = verts[i1], v2 = verts[i2];
				const bvhvec4 t1 = tinybvh_min( v0, bmin ), t2 = tinybvh_max( v0, bmax );
				const bvhvec4 t3 = tinybvh_min( v1, v2 ), t4 = tinybvh_max( v1, v2 );
				bmin = tinybvh_min( t1, t3 ), bmax = tinybvh_max( t2, t4 );
			}
			else for (uint32_t first = node.leftFirst, j = 0; j < node.triCount; j++)
			{
				const uint32_t vidx = primIdx[first + j] * 3;
				const bvhvec4 v0 = verts[vidx], v1 = verts[vidx + 1], v2 = verts[vidx + 2];
				const bvhvec4 t1 = tinybvh_min( v0, bmin ), t2 = tinybvh_max( v0, bmax );
				const bvhvec4 t3 = tinybvh_min( v1, v2 ), t4 = tinybvh_max( v1, v2 );
				bmin = tinybvh_min( t1, t3 ), bmax = tinybvh_max( t2, t4 );
			}
			node.aabbMin = bmin, node.aabbMax = bmax;
			continue;
		}
		// interior node: adjust to child bounds
		const BVHNode& left = bvhNode[node.leftFirst], & right = bvhNode[node.leftFirst + 1];
		node.aabbMin = tinybvh_min( left.aabbMin, right.aabbMin );
		node.aabbMax = tinybvh_max( left.aabbMax, right.aabbMax );
	}
	aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
}

#define FIX_COMBINE_LEAFS 1

// CombineLeafs: Collapse subtrees if the summed leaf prim count does not
// exceed the specified number. For BVH8_CPU construction.
uint32_t BVH::CombineLeafs( const uint32_t primCount, uint32_t& firstIdx, uint32_t nodeIdx )
{
	BVHNode& node = bvhNode[nodeIdx];
	if (node.isLeaf())
	{
		firstIdx = node.leftFirst;
		return node.triCount;
	}
	uint32_t firstLeft = 0, leftCount = CombineLeafs( primCount, firstLeft, node.leftFirst );
	uint32_t firstRight = 0, rightCount = CombineLeafs( primCount, firstRight, node.leftFirst + 1 );
	firstIdx = tinybvh_min( firstLeft, firstRight );
	if (leftCount + rightCount <= primCount)
		node.triCount = leftCount + rightCount,
		node.leftFirst = firstIdx;
	return leftCount + rightCount;
}

// CombineLeafs: Combine leaf nodes if this improves tree SAH cost. For HPLOC postprocessing.
void BVH::CombineLeafs( const uint32_t nodeIdx )
{
	BVHNode& node = bvhNode[nodeIdx];
	if (node.isLeaf()) return;
	BVHNode& left = bvhNode[node.leftFirst];
	BVHNode& right = bvhNode[node.leftFirst + 1];
	if (left.isLeaf() && right.isLeaf())
	{
		int combinedCount = left.triCount + right.triCount;
		float rAnode = 1.0f / tinybvh_halfarea( node.aabbMax - node.aabbMin );
		float Cnode = c_int * combinedCount;
		float Cleft = c_int * left.triCount * tinybvh_halfarea( left.aabbMax - left.aabbMin ) * rAnode;
		float Cright = c_int * right.triCount * tinybvh_halfarea( right.aabbMax - right.aabbMin ) * rAnode;
		float Csplit = Cleft + Cright + c_trav;
		if (Cnode < Csplit) if (right.leftFirst == (left.leftFirst + left.triCount))
			node.leftFirst = left.leftFirst,
			node.triCount = combinedCount;
		return;
	}
	CombineLeafs( node.leftFirst );
	CombineLeafs( node.leftFirst + 1 );
}

bool BVH::IntersectSphere( const bvhvec3& pos, const float r ) const
{
	const bvhvec3 bmin = pos - bvhvec3( r ), bmax = pos + bvhvec3( r );
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	const float r2 = r * r;
	while (1)
	{
		if (node->isLeaf())
		{
			// check if the leaf aabb overlaps the sphere: https://gamedev.stackexchange.com/a/156877
			float dist2 = 0;
			if (pos.x < node->aabbMin.x) dist2 += (node->aabbMin.x - pos.x) * (node->aabbMin.x - pos.x);
			if (pos.x > node->aabbMax.x) dist2 += (pos.x - node->aabbMax.x) * (pos.x - node->aabbMax.x);
			if (pos.y < node->aabbMin.y) dist2 += (node->aabbMin.y - pos.y) * (node->aabbMin.y - pos.y);
			if (pos.y > node->aabbMax.y) dist2 += (pos.y - node->aabbMax.y) * (pos.y - node->aabbMax.y);
			if (pos.z < node->aabbMin.z) dist2 += (node->aabbMin.z - pos.z) * (node->aabbMin.z - pos.z);
			if (pos.z > node->aabbMax.z) dist2 += (pos.z - node->aabbMax.z) * (pos.z - node->aabbMax.z);
			if (dist2 <= r2)
			{
				// tri/sphere test: https://gist.github.com/yomotsu/d845f21e2e1eb49f647f#file-gistfile1-js-L223
				for (uint32_t i = 0; i < node->triCount; i++)
				{
					uint32_t idx = primIdx[node->leftFirst + i];
					bvhvec3 a, b, c;
					if (!vertIdx) idx *= 3, a = verts[idx], b = verts[idx + 1], c = verts[idx + 2]; else
					{
						const uint32_t i0 = vertIdx[idx * 3], i1 = vertIdx[idx * 3 + 1], i2 = vertIdx[idx * 3 + 2];
						a = verts[i0], b = verts[i1], c = verts[i2];
					}
					const bvhvec3 A = a - pos, B = b - pos, C = c - pos;
					const float rr = r * r;
					const bvhvec3 V = tinybvh_cross( B - A, C - A );
					const float d = tinybvh_dot( A, V ), e = tinybvh_dot( V, V );
					if (d * d > rr * e) continue;
					const float aa = tinybvh_dot( A, A ), ab = tinybvh_dot( A, B ), ac = tinybvh_dot( A, C );
					const float bb = tinybvh_dot( B, B ), bc = tinybvh_dot( B, C ), cc = tinybvh_dot( C, C );
					if ((aa > rr && ab > aa && ac > aa) || (bb > rr && ab > bb && bc > bb) ||
						(cc > rr && ac > cc && bc > cc)) continue;
					const bvhvec3 AB = B - A, BC = C - B, CA = A - C;
					const float d1 = ab - aa, d2 = bc - bb, d3 = ac - cc;
					const float e1 = tinybvh_dot( AB, AB ), e2 = tinybvh_dot( BC, BC ), e3 = tinybvh_dot( CA, CA );
					const bvhvec3 Q1 = A * e1 - AB * d1, Q2 = B * e2 - BC * d2, Q3 = C * e3 - CA * d3;
					const bvhvec3 QC = C * e1 - Q1, QA = A * e2 - Q2, QB = B * e3 - Q3;
					if ((tinybvh_dot( Q1, Q1 ) > rr * e1 * e1 && tinybvh_dot( Q1, QC ) >= 0) ||
						(tinybvh_dot( Q2, Q2 ) > rr * e2 * e2 && tinybvh_dot( Q2, QA ) >= 0) ||
						(tinybvh_dot( Q3, Q3 ) > rr * e3 * e3 && tinybvh_dot( Q3, QB ) >= 0)) continue;
					// const float dist = sqrtf( d * d / e ) - r; // we're not using this.
					return true;
				}
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst], * child2 = &bvhNode[node->leftFirst + 1];
		bool hit1 = child1->Intersect( bmin, bmax ), hit2 = child2->Intersect( bmin, bmax );
		if (hit1 && hit2) stack[stackPtr++] = child2, node = child1;
		else if (hit1) node = child1; else if (hit2) node = child2;
		else { if (stackPtr == 0) break; node = stack[--stackPtr]; }
	}
	return false;
}

#define SLAB_TEST_TWO_NODES \
	float tx1a = (posX ? child1->aabbMin.x : child1->aabbMax.x) * ray.rD.x - rox; /* expect fma. */ \
	float ty1a = (posY ? child1->aabbMin.y : child1->aabbMax.y) * ray.rD.y - roy; \
	float tz1a = (posZ ? child1->aabbMin.z : child1->aabbMax.z) * ray.rD.z - roz; \
	float tx1b = (posX ? child2->aabbMin.x : child2->aabbMax.x) * ray.rD.x - rox; \
	float ty1b = (posY ? child2->aabbMin.y : child2->aabbMax.y) * ray.rD.y - roy; \
	float tz1b = (posZ ? child2->aabbMin.z : child2->aabbMax.z) * ray.rD.z - roz; \
	float tx2a = (posX ? child1->aabbMax.x : child1->aabbMin.x) * ray.rD.x - rox; \
	float ty2a = (posY ? child1->aabbMax.y : child1->aabbMin.y) * ray.rD.y - roy; \
	float tz2a = (posZ ? child1->aabbMax.z : child1->aabbMin.z) * ray.rD.z - roz; \
	float tx2b = (posX ? child2->aabbMax.x : child2->aabbMin.x) * ray.rD.x - rox; \
	float ty2b = (posY ? child2->aabbMax.y : child2->aabbMin.y) * ray.rD.y - roy; \
	float tz2b = (posZ ? child2->aabbMax.z : child2->aabbMin.z) * ray.rD.z - roz; \
	float tmina = tinybvh_max( tinybvh_max( tx1a, ty1a ), tinybvh_max( tz1a, 0.0f ) ); \
	float tminb = tinybvh_max( tinybvh_max( tx1b, ty1b ), tinybvh_max( tz1b, 0.0f ) ); \
	float tmaxa = tinybvh_min( tinybvh_min( tx2a, ty2a ), tinybvh_min( tz2a, ray.hit.t ) ); \
	float tmaxb = tinybvh_min( tinybvh_min( tx2b, ty2b ), tinybvh_min( tz2b, ray.hit.t ) ); \
	if (tmaxa >= tmina) dist1 = tmina; \
	if (tmaxb >= tminb) dist2 = tminb;

int32_t BVH::Intersect( Ray& ray ) const
{
	VALIDATE_RAY( ray );
	if (!isTLAS())
	{
		const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
		if (!posX) goto negx1;
		if (posY) { if (posZ) return Intersect<true, true, true>( ray ); else return Intersect<true, true, false>( ray ); }
		if (posZ) return Intersect<true, false, true>( ray ); else return Intersect<true, false, false>( ray );
	negx1:
		if (posY) { if (posZ) return Intersect<false, true, true>( ray ); else return Intersect<false, true, false>( ray ); }
		if (posZ) return Intersect<false, false, true>( ray ); else return Intersect<false, false, false>( ray );
	}
	else
	{
		const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
		if (!posX) goto negx2;
		if (posY) { if (posZ) return IntersectTLAS<true, true, true>( ray ); else return IntersectTLAS<true, true, false>( ray ); }
		if (posZ) return IntersectTLAS<true, false, true>( ray ); else return IntersectTLAS<true, false, false>( ray );
	negx2:
		if (posY) { if (posZ) return IntersectTLAS<false, true, true>( ray ); else return IntersectTLAS<false, true, false>( ray ); }
		if (posZ) return IntersectTLAS<false, false, true>( ray ); else return IntersectTLAS<false, false, false>( ray );
	}
}

template <bool posX, bool posY, bool posZ> int32_t BVH::Intersect( Ray& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[256];
	uint32_t stackPtr = 0;
	float cost = 0;
	const float rox = ray.O.x * ray.rD.x;
	const float roy = ray.O.y * ray.rD.y;
	const float roz = ray.O.z * ray.rD.z;
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			// Performance note: if indexed primitives (ENABLE_INDEXED_GEOMETRY) and custom
			// geometry (ENABLE_CUSTOM_GEOMETRY) are both disabled, this leaf code reduces
			// to a regular loop over triangles. Otherwise, the extra flexibility comes at
			// a small performance cost.
			if (indexedEnabled && vertIdx != 0) for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t pi = primIdx[node->leftFirst + i];
				const uint32_t i0 = vertIdx[pi * 3], i1 = vertIdx[pi * 3 + 1], i2 = vertIdx[pi * 3 + 2];
				IntersectTri( ray, pi, verts, i0, i1, i2 );
			}
			else if (customEnabled && customIntersect != 0) for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				if ((*customIntersect)(ray, primIdx[node->leftFirst + i]))
				{
				#if INST_IDX_BITS == 32
					ray.hit.inst = ray.instIdx;
				#else
					ray.hit.prim = (ray.hit.prim & PRIM_IDX_MASK) + ray.instIdx;
				#endif
				}
			}
			else for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t pi = primIdx[node->leftFirst + i];
				IntersectTri( ray, pi, verts, pi * 3, pi * 3 + 1, pi * 3 + 2 );
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst], * child2 = &bvhNode[node->leftFirst + 1];
		float dist1 = BVH_FAR, dist2 = BVH_FAR;
		SLAB_TEST_TWO_NODES;
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return (int32_t)cost; // cast to not break interface.
}

template <bool posX, bool posY, bool posZ> int32_t BVH::IntersectTLAS( Ray& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	float cost = 0;
	const float rox = ray.O.x * ray.rD.x;
	const float roy = ray.O.y * ray.rD.y;
	const float roz = ray.O.z * ray.rD.z;
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			Ray tmpRay;
			for (uint32_t i = 0; i < node->triCount; i++)
			{
				// BLAS traversal
				const uint32_t instIdx = primIdx[node->leftFirst + i];
				const BLASInstance& inst = instList[instIdx];
				// Check if the ray should intersect this BLAS Instance, otherwise skip it
				if (!(inst.mask & ray.mask)) continue;
				const BVHBase* blas = blasList[inst.blasIdx];
				// 1. Transform ray with the inverse of the instance transform
				tmpRay.O = tinybvh_transform_point( ray.O, inst.invTransform );
				tmpRay.D = tinybvh_transform_vector( ray.D, inst.invTransform );
				tmpRay.instIdx = instIdx << (32 - INST_IDX_BITS);
				tmpRay.hit = ray.hit;
				tmpRay.rD = tinybvh_rcp( tmpRay.D );
				// 2. Traverse BLAS with the transformed ray
				// Note: Valid BVH layout options for BLASses are the regular BVH layout,
				// the AVX-optimized BVH_SOA layout and the wide BVH4_CPU layout. When all
				// BLASses are of the same layout this reduces to nearly zero cost for
				// a small set of predictable branches.
				assert( blas->layout == LAYOUT_BVH || blas->layout == LAYOUT_BVH4_CPU ||
					blas->layout == LAYOUT_BVH_SOA || blas->layout == LAYOUT_BVH8_AVX2 );
				if (blas->layout == LAYOUT_BVH)
				{
					// regular (triangle) BVH traversal
					cost += ((BVH*)blas)->Intersect( tmpRay );
				}
				else
				{
				#ifdef BVH_USESSE
					if (blas->layout == LAYOUT_BVH4_CPU) cost += ((BVH4_CPU*)blas)->Intersect( tmpRay );
				#endif
				#ifdef BVH_USEAVX
					if (blas->layout == LAYOUT_BVH_SOA) cost += ((BVH_SoA*)blas)->Intersect( tmpRay );
				#endif
				#ifdef BVH_USEAVX2
					if (blas->layout == LAYOUT_BVH8_AVX2) cost += ((BVH8_CPU*)blas)->Intersect( tmpRay );
				#endif
					if (blas->layout == LAYOUT_VOXELSET) cost += ((VoxelSet*)blas)->Intersect( tmpRay );
				}
				// 3. Restore ray
				ray.hit = tmpRay.hit;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst], * child2 = &bvhNode[node->leftFirst + 1];
		float dist1 = BVH_FAR, dist2 = BVH_FAR;
		SLAB_TEST_TWO_NODES;
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return (int32_t)cost;
}

bool BVH::IsOccluded( const Ray& ray ) const
{
	VALIDATE_RAY( ray );
	if (!isTLAS())
	{
		const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
		if (!posX) goto negx1;
		if (posY) { if (posZ) return IsOccluded<true, true, true>( ray ); else return IsOccluded<true, true, false>( ray ); }
		if (posZ) return IsOccluded<true, false, true>( ray ); else return IsOccluded<true, false, false>( ray );
	negx1:
		if (posY) { if (posZ) return IsOccluded<false, true, true>( ray ); else return IsOccluded<false, true, false>( ray ); }
		if (posZ) return IsOccluded<false, false, true>( ray ); else return IsOccluded<false, false, false>( ray );
	}
	else
	{
		const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
		if (!posX) goto negx2;
		if (posY) { if (posZ) return IsOccludedTLAS<true, true, true>( ray ); else return IsOccludedTLAS<true, true, false>( ray ); }
		if (posZ) return IsOccludedTLAS<true, false, true>( ray ); else return IsOccludedTLAS<true, false, false>( ray );
	negx2:
		if (posY) { if (posZ) return IsOccludedTLAS<false, true, true>( ray ); else return IsOccludedTLAS<false, true, false>( ray ); }
		if (posZ) return IsOccludedTLAS<false, false, true>( ray ); else return IsOccludedTLAS<false, false, false>( ray );
	}
}

template <bool posX, bool posY, bool posZ> bool BVH::IsOccluded( const Ray& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	const float rox = ray.O.x * ray.rD.x;
	const float roy = ray.O.y * ray.rD.y;
	const float roz = ray.O.z * ray.rD.z;
	while (1)
	{
		if (node->isLeaf())
		{
			if (indexedEnabled && vertIdx != 0) for (uint32_t i = 0; i < node->triCount; i++)
			{
				const uint32_t pi = primIdx[node->leftFirst + i], vi0 = pi * 3;
				const uint32_t i0 = vertIdx[vi0], i1 = vertIdx[vi0 + 1], i2 = vertIdx[vi0 + 2];
				if (TriOccludes( ray, verts, pi, i0, i1, i2 )) return true;
			}
			else if (customEnabled && customIsOccluded != 0)
			{
				for (uint32_t i = 0; i < node->triCount; i++)
					if ((*customIsOccluded)(ray, primIdx[node->leftFirst + i])) return true;
			}
			else for (uint32_t i = 0; i < node->triCount; i++)
			{
				const uint32_t pi = primIdx[node->leftFirst + i], vi0 = pi * 3;
				if (TriOccludes( ray, verts, pi, vi0, vi0 + 1, vi0 + 2 )) return true;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst];
		BVHNode* child2 = &bvhNode[node->leftFirst + 1];
		float dist1 = BVH_FAR, dist2 = BVH_FAR;
		SLAB_TEST_TWO_NODES;
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return false;
}

template <bool posX, bool posY, bool posZ> bool BVH::IsOccludedTLAS( const Ray& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	Ray tmpRay;
	const float rox = ray.O.x * ray.rD.x;
	const float roy = ray.O.y * ray.rD.y;
	const float roz = ray.O.z * ray.rD.z;
	while (1)
	{
		if (node->isLeaf())
		{
			for (uint32_t i = 0; i < node->triCount; i++)
			{
				// BLAS traversal
				BLASInstance& inst = instList[primIdx[node->leftFirst + i]];
				const BVHBase* blas = blasList[inst.blasIdx];
				// Check if the ray should intersect this BLAS Instance, otherwise skip it
				if (!(inst.mask & ray.mask)) continue;
				// 1. Transform ray with the inverse of the instance transform
				tmpRay.O = tinybvh_transform_point( ray.O, inst.invTransform );
				tmpRay.D = tinybvh_transform_vector( ray.D, inst.invTransform );
				tmpRay.hit.t = ray.hit.t;
				tmpRay.rD = tinybvh_rcp( tmpRay.D );
				// 2. Traverse BLAS with the transformed ray
				assert( blas->layout == LAYOUT_BVH || blas->layout == LAYOUT_BVH_SOA ||
					blas->layout == LAYOUT_BVH8_AVX2 || blas->layout == LAYOUT_BVH4_CPU );
				if (blas->layout == LAYOUT_BVH)
				{
					// regular (triangle) BVH traversal
					if (((BVH*)blas)->IsOccluded( tmpRay )) return true;
				}
				else
				{
				#ifdef BVH_USESSE
					if (blas->layout == LAYOUT_BVH4_CPU) { if (((BVH4_CPU*)blas)->IsOccluded( tmpRay )) return true; }
				#endif
				#ifdef BVH_USEAVX
					if (blas->layout == LAYOUT_BVH_SOA) { if (((BVH_SoA*)blas)->IsOccluded( tmpRay )) return true; }
				#endif
				#ifdef BVH_USEAVX2
					if (blas->layout == LAYOUT_BVH8_AVX2) { if (((BVH8_CPU*)blas)->IsOccluded( tmpRay )) return true; }
				#endif
					if (blas->layout == LAYOUT_VOXELSET) { if (((VoxelSet*)blas)->IsOccluded( tmpRay )) return true; }
				}
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst], * child2 = &bvhNode[node->leftFirst + 1];
		float dist1 = BVH_FAR, dist2 = BVH_FAR;
		SLAB_TEST_TWO_NODES;
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return false;
}

// Intersect a WALD_32BYTE BVH with a ray packet.
// The 256 rays travel together to better utilize the caches and to amortize the cost
// of memory transfers over the rays in the bundle.
// Note that this basic implementation assumes a specific layout of the rays. Provided
// as 'proof of concept', should not be used in production code.
// Based on Large Ray Packets for Real-time Whitted Ray Tracing, Overbeck et al., 2008,
// extended with sorted traversal and reduced stack traffic.
void BVH::Intersect256Rays( Ray* packet ) const
{
	// convenience macro
#define CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( r ) const bvhvec3 rD = packet[r].rD, t1 = o1 * rD, t2 = o2 * rD; \
	const float tmin = tinybvh_max( tinybvh_max( tinybvh_min( t1.x, t2.x ), tinybvh_min( t1.y, t2.y ) ), tinybvh_min( t1.z, t2.z ) ); \
	const float tmax = tinybvh_min( tinybvh_min( tinybvh_max( t1.x, t2.x ), tinybvh_max( t1.y, t2.y ) ), tinybvh_max( t1.z, t2.z ) );
	// Corner rays are: 0, 51, 204 and 255
	// Construct the bounding planes, with normals pointing outwards
	const bvhvec3 O = packet[0].O; // same for all rays in this case
	const bvhvec3 p0 = packet[0].O + packet[0].D; // top-left
	const bvhvec3 p1 = packet[51].O + packet[51].D; // top-right
	const bvhvec3 p2 = packet[204].O + packet[204].D; // bottom-left
	const bvhvec3 p3 = packet[255].O + packet[255].D; // bottom-right
	const bvhvec3 plane0 = tinybvh_normalize( tinybvh_cross( p0 - O, p0 - p2 ) ); // left plane
	const bvhvec3 plane1 = tinybvh_normalize( tinybvh_cross( p3 - O, p3 - p1 ) ); // right plane
	const bvhvec3 plane2 = tinybvh_normalize( tinybvh_cross( p1 - O, p1 - p0 ) ); // top plane
	const bvhvec3 plane3 = tinybvh_normalize( tinybvh_cross( p2 - O, p2 - p3 ) ); // bottom plane
	const int32_t sign0x = plane0.x < 0 ? 4 : 0, sign0y = plane0.y < 0 ? 5 : 1, sign0z = plane0.z < 0 ? 6 : 2;
	const int32_t sign1x = plane1.x < 0 ? 4 : 0, sign1y = plane1.y < 0 ? 5 : 1, sign1z = plane1.z < 0 ? 6 : 2;
	const int32_t sign2x = plane2.x < 0 ? 4 : 0, sign2y = plane2.y < 0 ? 5 : 1, sign2z = plane2.z < 0 ? 6 : 2;
	const int32_t sign3x = plane3.x < 0 ? 4 : 0, sign3y = plane3.y < 0 ? 5 : 1, sign3z = plane3.z < 0 ? 6 : 2;
	const float d0 = tinybvh_dot( O, plane0 ), d1 = tinybvh_dot( O, plane1 );
	const float d2 = tinybvh_dot( O, plane2 ), d3 = tinybvh_dot( O, plane3 );
	// Traverse the tree with the packet
	int32_t first = 0, last = 255; // first and last active ray in the packet
	const BVHNode* node = &bvhNode[0];
	ALIGNED( 64 ) uint32_t stack[64], stackPtr = 0;
	while (1)
	{
		if (node->isLeaf())
		{
			// handle leaf node
			for (uint32_t j = 0; j < node->triCount; j++)
			{
				const uint32_t idx = primIdx[node->leftFirst + j], vid = idx * 3;
				const bvhvec3 e1 = verts[vid + 1] - verts[vid], e2 = verts[vid + 2] - verts[vid];
				const bvhvec3 s = O - bvhvec3( verts[vid] );
				for (int32_t i = first; i <= last; i++)
				{
					Ray& ray = packet[i];
					const bvhvec3 h = tinybvh_cross( ray.D, e2 );
					const float a = tinybvh_dot( e1, h );
					if (fabs( a ) < 0.0000001f) continue; // ray parallel to triangle
					const float f = 1 / a, u = f * tinybvh_dot( s, h );
					const bvhvec3 q = tinybvh_cross( s, e1 );
					const float v = f * tinybvh_dot( ray.D, q );
					if (u < 0 || v < 0 || u + v > 1) continue;
					const float t = f * tinybvh_dot( e2, q );
					if (t <= 0 || t >= ray.hit.t) continue;
					ray.hit.t = t, ray.hit.u = u, ray.hit.v = v;
				#if INST_IDX_BITS == 32
					ray.hit.prim = idx, ray.hit.inst = ray.instIdx;
				#else
					ray.hit.prim = idx + ray.instIdx;
				#endif
				}
			}
			if (stackPtr == 0) break; else // pop
				last = stack[--stackPtr], node = bvhNode + stack[--stackPtr],
				first = last >> 8, last &= 255;
		}
		else
		{
			// fetch pointers to child nodes
			const BVHNode* left = bvhNode + node->leftFirst;
			const BVHNode* right = bvhNode + node->leftFirst + 1;
			bool visitLeft = true, visitRight = true;
			int32_t leftFirst = first, leftLast = last, rightFirst = first, rightLast = last;
			float distLeft, distRight;
			{
				// see if we want to intersect the left child
				const bvhvec3 o1( left->aabbMin.x - O.x, left->aabbMin.y - O.y, left->aabbMin.z - O.z );
				const bvhvec3 o2( left->aabbMax.x - O.x, left->aabbMax.y - O.y, left->aabbMax.z - O.z );
				// 1. Early-in test: if first ray hits the node, the packet visits the node
				bool earlyHit;
				{
					CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( first );
					earlyHit = (tmax >= tmin && tmin < packet[first].hit.t && tmax >= 0);
					distLeft = tmin;
				}
				if (!earlyHit) // 2. Early-out test: if the node aabb is outside the four planes, we skip the node
				{
					float* minmax = (float*)left;
					bvhvec3 c0( minmax[sign0x], minmax[sign0y], minmax[sign0z] );
					bvhvec3 c1( minmax[sign1x], minmax[sign1y], minmax[sign1z] );
					bvhvec3 c2( minmax[sign2x], minmax[sign2y], minmax[sign2z] );
					bvhvec3 c3( minmax[sign3x], minmax[sign3y], minmax[sign3z] );
					if (tinybvh_dot( c0, plane0 ) > d0 || tinybvh_dot( c1, plane1 ) > d1 ||
						tinybvh_dot( c2, plane2 ) > d2 || tinybvh_dot( c3, plane3 ) > d3)
						visitLeft = false;
					else // 3. Last resort: update first and last, stay in node if first > last
					{
						for (; leftFirst <= leftLast; leftFirst++)
						{
							CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( leftFirst );
							if (tmax >= tmin && tmin < packet[leftFirst].hit.t && tmax >= 0) { distLeft = tmin; break; }
						}
						for (; leftLast >= leftFirst; leftLast--)
						{
							CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( leftLast );
							if (tmax >= tmin && tmin < packet[leftLast].hit.t && tmax >= 0) break;
						}
						visitLeft = leftLast >= leftFirst;
					}
				}
			}
			{
				// see if we want to intersect the right child
				const bvhvec3 o1( right->aabbMin.x - O.x, right->aabbMin.y - O.y, right->aabbMin.z - O.z );
				const bvhvec3 o2( right->aabbMax.x - O.x, right->aabbMax.y - O.y, right->aabbMax.z - O.z );
				// 1. Early-in test: if first ray hits the node, the packet visits the node
				bool earlyHit;
				{
					CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( first );
					earlyHit = (tmax >= tmin && tmin < packet[first].hit.t && tmax >= 0);
					distRight = tmin;
				}
				if (!earlyHit) // 2. Early-out test: if the node aabb is outside the four planes, we skip the node
				{
					float* minmax = (float*)right;
					bvhvec3 c0( minmax[sign0x], minmax[sign0y], minmax[sign0z] );
					bvhvec3 c1( minmax[sign1x], minmax[sign1y], minmax[sign1z] );
					bvhvec3 c2( minmax[sign2x], minmax[sign2y], minmax[sign2z] );
					bvhvec3 c3( minmax[sign3x], minmax[sign3y], minmax[sign3z] );
					if (tinybvh_dot( c0, plane0 ) > d0 || tinybvh_dot( c1, plane1 ) > d1 ||
						tinybvh_dot( c2, plane2 ) > d2 || tinybvh_dot( c3, plane3 ) > d3)
						visitRight = false;
					else // 3. Last resort: update first and last, stay in node if first > last
					{
						for (; rightFirst <= rightLast; rightFirst++)
						{
							CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( rightFirst );
							if (tmax >= tmin && tmin < packet[rightFirst].hit.t && tmax >= 0) { distRight = tmin; break; }
						}
						for (; rightLast >= first; rightLast--)
						{
							CALC_TMIN_TMAX_WITH_SLABTEST_ON_RAY( rightLast );
							if (tmax >= tmin && tmin < packet[rightLast].hit.t && tmax >= 0) break;
						}
						visitRight = rightLast >= rightFirst;
					}
				}
			}
			// process intersection result
			if (visitLeft && visitRight)
			{
				if (distLeft < distRight) // push right, continue with left
				{
					stack[stackPtr++] = node->leftFirst + 1;
					stack[stackPtr++] = (rightFirst << 8) + rightLast;
					node = left, first = leftFirst, last = leftLast;
				}
				else // push left, continue with right
				{
					stack[stackPtr++] = node->leftFirst;
					stack[stackPtr++] = (leftFirst << 8) + leftLast;
					node = right, first = rightFirst, last = rightLast;
				}
			}
			else if (visitLeft) // continue with left
				node = left, first = leftFirst, last = leftLast;
			else if (visitRight) // continue with right
				node = right, first = rightFirst, last = rightLast;
			else if (stackPtr == 0) break; else // pop
				last = stack[--stackPtr], node = bvhNode + stack[--stackPtr],
				first = last >> 8, last &= 255;
		}
	}
}

int32_t BVH::NodeCount() const
{
	// Determine the number of nodes in the tree. Typically the result should
	// be usedNodes - 1 (second node is always unused), but some builders may
	// have unused nodes besides node 1. TODO: Support more layouts.
	uint32_t retVal = 0, nodeIdx = 0, stack[64], stackPtr = 0;
	while (1)
	{
		const BVHNode& n = bvhNode[nodeIdx];
		retVal++;
		if (n.isLeaf()) { if (stackPtr == 0) break; else nodeIdx = stack[--stackPtr]; }
		else nodeIdx = n.leftFirst, stack[stackPtr++] = n.leftFirst + 1;
	}
	return retVal;
}

int32_t BVH::LeafCount() const
{
	// Determine the number of nodes in the tree. Typically the result should
	// be usedNodes - 1 (second node is always unused), but some builders may
	// have unused nodes besides node 1. TODO: Support more layouts.
	uint32_t retVal = 0, nodeIdx = 0, stack[64], stackPtr = 0;
	while (1)
	{
		const BVHNode& n = bvhNode[nodeIdx];
		if (n.isLeaf()) { retVal++; if (stackPtr == 0) break; else nodeIdx = stack[--stackPtr]; }
		else nodeIdx = n.leftFirst, stack[stackPtr++] = n.leftFirst + 1;
	}
	return retVal;
}

// Compact: Reduce the size of a BVH by removing any unused nodes.
// This is useful after an SBVH build or multi-threaded build, but also after
// calling MergeLeafs. Some operations, such as Optimize, *require* a
// compacted tree to work correctly.
void BVH::Compact()
{
	BVH_FATAL_ERROR_IF( bvhNode == 0, "BVH::Compact(), bvhNode == 0." );
	if (bvhNode[0].isLeaf()) return; // nothing to compact.
	BVHNode* tmpNodes = (BVHNode*)AlignedAlloc( sizeof( BVHNode ) * allocatedNodes /* do *not* trim */ );
	uint32_t* idx = (uint32_t*)AlignedAlloc( sizeof( uint32_t ) * idxCount );
	memcpy( tmpNodes, bvhNode, 2 * sizeof( BVHNode ) );
	newNodePtr = 2;
	uint32_t newIdxPtr = 0, nodeIdx = 0, stack[128], stackPtr = 0;
	while (1)
	{
		BVHNode& node = tmpNodes[nodeIdx];
		if (node.isLeaf())
		{
			const uint32_t leafStart = newIdxPtr;
			for (uint32_t i = 0; i < node.triCount; i++) idx[newIdxPtr++] = primIdx[node.leftFirst + i];
			node.leftFirst = leafStart;
			if (!stackPtr) break;
			nodeIdx = stack[--stackPtr];
		}
		else
		{
			const BVHNode& left = bvhNode[node.leftFirst];
			const BVHNode& right = bvhNode[node.leftFirst + 1];
			tmpNodes[newNodePtr] = left, tmpNodes[newNodePtr + 1] = right;
			const uint32_t todo1 = newNodePtr, todo2 = newNodePtr + 1;
			node.leftFirst = newNodePtr, newNodePtr += 2;
			nodeIdx = todo1, stack[stackPtr++] = todo2;
		}
	}
	AlignedFree( bvhNode );
	AlignedFree( primIdx );
	usedNodes = newNodePtr, bvhNode = tmpNodes, primIdx = idx;
}

// VoxelSet implementation
// ----------------------------------------------------------------------------

VoxelSet::VoxelSet()
{
	grid = (uint32_t*)AlignedAlloc( gridSize * sizeof( uint32_t ) );
	memset( grid, 0, gridSize * sizeof( uint32_t ) );
	brick = (uint32_t*)AlignedAlloc( brickSize * brickCount * sizeof( uint32_t ) );
	memset( brick, 0, brickSize * brickCount * sizeof( uint32_t ) );
	topGrid = (uint32_t*)AlignedAlloc( topGridSize / 8 );
	freeBrickPtr = 1; // first available brick; we'll skip 0
	aabbMin = bvhvec3( 0 ), aabbMax = bvhvec3( 1 ); // a voxel object is always (1,1,1) in object space.
}

void VoxelSet::Set( const uint32_t x, const uint32_t y, const uint32_t z, const uint32_t v )
{
	// note: not thread-safe.
	const uint32_t bx = x / brickDim, by = y / brickDim, bz = z / brickDim;
	const uint32_t gridIdx = bx + by * gridDim + bz * gridDim * gridDim;
	uint32_t brickIdx = grid[gridIdx];
	if (!brickIdx)
	{
		if (freeBrickPtr == brickCount) // we ran out; reallocate
		{
			uint32_t newBrickCount = brickCount + (brickCount >> 2);
			uint32_t* newBrickPool = (uint32_t*)AlignedAlloc( newBrickCount * brickSize * sizeof( uint32_t ) );
			memcpy( newBrickPool, brick, brickCount * brickSize * sizeof( uint32_t ) );
			memset( newBrickPool + brickCount * brickSize, 0, (newBrickCount - brickCount) * brickSize * sizeof( uint32_t ) );
			AlignedFree( brick );
			brick = newBrickPool, brickCount = newBrickCount;
		}
		brickIdx = grid[gridIdx] = freeBrickPtr++;
	}
	const uint32_t voxelIdx = (x & (brickDim - 1)) + (y & (brickDim - 1)) * brickDim + (z & (brickDim - 1)) * brickDim * brickDim;
	brick[brickIdx * brickSize + voxelIdx] = v;
}

void VoxelSet::UpdateTopGrid()
{
	memset( topGrid, 0, topGridSize / 8 );
	for (int x = 0; x < topGridDim; x++) for (int y = 0; y < topGridDim; y++) for (int z = 0; z < topGridDim; z++)
	{
		uint32_t* gridBase = grid + x * groupDim + y * groupDim * gridDim + z * groupDim * gridDim * gridDim;
		bool hasContent = false;
		for (int u = 0; u < groupDim; u++) for (int v = 0; v < groupDim; v++) for (int w = 0; w < groupDim; w++)
			if (gridBase[u + v * gridDim + w * gridDim * gridDim])
			{
				hasContent = true;
				goto break3;
			}
	break3:
		if (!hasContent) continue;
		uint32_t topIdx = x + y * topGridDim + z * topGridDim * topGridDim;
		topGrid[topIdx >> 5] |= 1 << (topIdx & 31);
	}
}

bool VoxelSet::Setup3DDDA( const Ray& ray, const bvhvec3& Dsign, DDAState& state, const bvhint3& step, bvhvec3& tdelta, float& t ) const
{
	// if ray is not inside the object aabb: advance until it is
	if (!(ray.O.x >= 0 && ray.O.x <= 1 && ray.O.y >= 0 && ray.O.y <= 1 && ray.O.z >= 0 && ray.O.z <= 1))
	{
		float tx1 = -ray.O.x * ray.rD.x, tx2 = (1 - ray.O.x) * ray.rD.x;
		float tmin = tinybvh_min( tx1, tx2 ), tmax = tinybvh_max( tx1, tx2 );
		float ty1 = -ray.O.y * ray.rD.y, ty2 = (1 - ray.O.y) * ray.rD.y;
		tmin = tinybvh_max( tmin, tinybvh_min( ty1, ty2 ) );
		tmax = tinybvh_min( tmax, tinybvh_max( ty1, ty2 ) );
		float tz1 = -ray.O.z * ray.rD.z, tz2 = (1 - ray.O.z) * ray.rD.z;
		tmin = tinybvh_max( tmin, tinybvh_min( tz1, tz2 ) );
		tmax = tinybvh_min( tmax, tinybvh_max( tz1, tz2 ) );
		if (tmax < tmin || tmin > ray.hit.t || tmax < 0) return false; else t = tmin;
	}
	// setup amanatides & woo - assume object size is 1x1x1, from (0,0,0) to (1,1,1)
	static const float cellSize = 1.0f / topGridDim;
	const bvhvec3 posInGrid = (ray.O + ray.D * (t + 0.0000025f)) * (float)topGridDim;
	const bvhvec3 gridPlanes = (bvhvec3( ceilf( posInGrid.x ), ceilf( posInGrid.y ), ceilf( posInGrid.z ) ) - Dsign) * cellSize;
	const bvhint3 P(
		tinybvh_clamp( (int)posInGrid.x, 0, topGridDim - 1 ),
		tinybvh_clamp( (int)posInGrid.y, 0, topGridDim - 1 ),
		tinybvh_clamp( (int)posInGrid.z, 0, topGridDim - 1 )
	);
	state.X = P.x, state.Y = P.y, state.Z = P.z;
	state.tmax = (gridPlanes - ray.O) * ray.rD;
	tdelta = bvhvec3( (float)step.x, (float)step.y, (float)step.z ) * cellSize * ray.rD;
	// proceed with traversal
	return true;
}

bvhvec3 VoxelSet::GetNormal( const Ray& ray ) const
{
	const bvhvec3 I1 = (ray.O + ray.hit.t * ray.D) * (float)objectDim; // our object is (1,1,1) in object space, so this scales each voxel to (1,1,1)
	const bvhvec3 fG( I1.x - floorf( I1.x ), I1.y - floorf( I1.y ), I1.z - floorf( I1.z ) );
	const bvhvec3 d = tinybvh_min( fG, 1.0f - fG );
	const float mind = tinybvh_min( tinybvh_min( d.x, d.y ), d.z );
	if (mind == d.x) return bvhvec3( ray.D.x > 0 ? -1.0f : 1.0f, 0, 0 );
	else if (mind == d.y) return bvhvec3( 0, ray.D.y > 0 ? -1.0f : 1.0f, 0 );
	else return bvhvec3( 0, 0, ray.D.z > 0 ? -1.0f : 1.0f );
}

int32_t VoxelSet::Intersect( Ray& ray ) const
{
	// setup Amanatides & Woo grid traversal
	ALIGNED( 64 ) DDAState l1_, l2_;
	const uint32_t xsign = *(uint32_t*)&ray.D.x >> 31;
	const uint32_t ysign = *(uint32_t*)&ray.D.y >> 31;
	const uint32_t zsign = *(uint32_t*)&ray.D.z >> 31;
	const bvhvec3 Dsign = bvhvec3( (float)xsign, (float)ysign, (float)zsign );
	const bvhint3 step( 1 - (int)xsign * 2, 1 - (int)ysign * 2, 1 - (int)zsign * 2 );
	bvhvec3 tdelta;
	float t = 0;
	if (!Setup3DDDA( ray, Dsign, l1_, step, tdelta, t )) return 0;
	const bvhvec3 l2tdelta = tdelta * (1.0f / groupDim);
	const bvhvec3 l3tdelta = l2tdelta * (1.0f / brickDim);
	uint32_t* gridBase = 0;
	int32_t steps = 0;
	// start stepping:
	while (1)
	{
		const uint32_t tidx = l1_.X + l1_.Y * topGridDim + l1_.Z * topGridDim * topGridDim;
		const uint32_t cell = topGrid[tidx >> 5] & (1 << (tidx & 31));
		if (cell)
		{
			// setup midlevel traversal
			const bvhvec3 posInGrid = (ray.O + (t + 0.0000025f) * ray.D) * (float)gridDim;
			const bvhvec3 gridPlanes = (bvhvec3( ceilf( posInGrid.x ), ceilf( posInGrid.y ), ceilf( posInGrid.z ) ) - Dsign) * (1.0f / gridDim);
			l2_.X = tinybvh_clamp( (int)posInGrid.x, l1_.X * groupDim, l1_.X * groupDim + (groupDim - 1) );
			l2_.Y = tinybvh_clamp( (int)posInGrid.y, l1_.Y * groupDim, l1_.Y * groupDim + (groupDim - 1) );
			l2_.Z = tinybvh_clamp( (int)posInGrid.z, l1_.Z * groupDim, l1_.Z * groupDim + (groupDim - 1) );
			l2_.tmax = (gridPlanes - ray.O) * ray.rD;
			gridBase = grid + ((l2_.X + l2_.Y * gridDim + l2_.Z * gridDim * gridDim) & superMask);
			l2_.X &= groupDim - 1, l2_.Y &= groupDim - 1, l2_.Z &= groupDim - 1;
			// step through midlevel cells
			while (1)
			{
				const uint32_t brickCell = gridBase[l2_.X + l2_.Y * gridDim + l2_.Z * gridDim * gridDim];
				if (brickCell)
				{
					// setup 3DDDA for brick traversal
					uint32_t* brickData = brick + brickCell * brickSize;
					const bvhvec3 posInBrick = (ray.O + (t + 0.0000025f) * ray.D) * (float)objectDim;
					uint32_t X = tinybvh_clamp( (int)posInBrick.x - (l2_.X + l1_.X * groupDim) * brickDim, 0, brickDim - 1 );
					uint32_t Y = tinybvh_clamp( (int)posInBrick.y - (l2_.Y + l1_.Y * groupDim) * brickDim, 0, brickDim - 1 );
					uint32_t Z = tinybvh_clamp( (int)posInBrick.z - (l2_.Z + l1_.Z * groupDim) * brickDim, 0, brickDim - 1 );
					const bvhvec3 brickPlanes = (bvhvec3( ceilf( posInBrick.x ), ceilf( posInBrick.y ), ceilf( posInBrick.z ) ) - Dsign) * (1.0f / objectDim);
					bvhvec3 tmax = (brickPlanes - ray.O) * ray.rD;
					// step through brick
					while (1)
					{
						steps++;
						const uint32_t v = brickData[X + Y * brickDim + Z * brickDim * brickDim];
						if (v)
						{
							ray.hit.t = t;
						#if INST_IDX_BITS == 32
							ray.hit.prim = v;
							ray.hit.inst = ray.instIdx; // store in dedicated field
						#elif INST_IDX_BITS == 8
							ray.hit.prim = v + (ray.instIdx << 24); // store in alpha
						#else
							ray.hit.prim = v;
							ray.hit.u = *(float*)&ray.instIdx; // store in u; hack
						#endif
							return steps;
						}
						if (tmax.x < tmax.y)
						{
							if (tmax.x < tmax.z)
							{
								if ((X += step.x) >= brickDim) break;
								t = tmax.x, tmax.x += l3tdelta.x;
							}
							else
							{
								if ((Z += step.z) >= brickDim) break;
								t = tmax.z, tmax.z += l3tdelta.z;
							}
						}
						else
						{
							if (tmax.y < tmax.z)
							{
								if ((Y += step.y) >= brickDim) break;
								t = tmax.y, tmax.y += l3tdelta.y;
							}
							else
							{
								if ((Z += step.z) >= brickDim) break;
								t = tmax.z, tmax.z += l3tdelta.z;
							}
						}
					}
				}
				if (l2_.tmax.x < l2_.tmax.y)
				{
					if (l2_.tmax.x < l2_.tmax.z)
					{
						if ((l2_.X += step.x) >= groupDim) break;
						t = l2_.tmax.x, l2_.tmax.x += l2tdelta.x;
					}
					else
					{
						if ((l2_.Z += step.z) >= groupDim) break;
						t = l2_.tmax.z, l2_.tmax.z += l2tdelta.z;
					}
				}
				else
				{
					if (l2_.tmax.y < l2_.tmax.z)
					{
						if ((l2_.Y += step.y) >= groupDim) break;
						t = l2_.tmax.y, l2_.tmax.y += l2tdelta.y;
					}
					else
					{
						if ((l2_.Z += step.z) >= groupDim) break;
						t = l2_.tmax.z, l2_.tmax.z += l2tdelta.z;
					}
				}
			}
		}
		if (l1_.tmax.x < l1_.tmax.y)
		{
			if (l1_.tmax.x < l1_.tmax.z)
			{
				if ((l1_.X += step.x) >= topGridDim) break;
				t = l1_.tmax.x, l1_.tmax.x += tdelta.x;
			}
			else
			{
				if ((l1_.Z += step.z) >= topGridDim) break;
				t = l1_.tmax.z, l1_.tmax.z += tdelta.z;
			}
		}
		else
		{
			if (l1_.tmax.y < l1_.tmax.z)
			{
				if ((l1_.Y += step.y) >= topGridDim) break;
				t = l1_.tmax.y, l1_.tmax.y += tdelta.y;
			}
			else
			{
				if ((l1_.Z += step.z) >= topGridDim) break;
				t = l1_.tmax.z, l1_.tmax.z += tdelta.z;
			}
		}
	}
	return 0;
}

bool VoxelSet::IsOccluded( const Ray& ray ) const
{
	// setup Amanatides & Woo grid traversal
	ALIGNED( 64 ) DDAState l1_, l2_;
	const uint32_t xsign = *(uint32_t*)&ray.D.x >> 31;
	const uint32_t ysign = *(uint32_t*)&ray.D.y >> 31;
	const uint32_t zsign = *(uint32_t*)&ray.D.z >> 31;
	const bvhvec3 Dsign = bvhvec3( (float)xsign, (float)ysign, (float)zsign );
	const bvhint3 step( 1 - (int)xsign * 2, 1 - (int)ysign * 2, 1 - (int)zsign * 2 );
	bvhvec3 tdelta;
	float t = 0;
	if (!Setup3DDDA( ray, Dsign, l1_, step, tdelta, t )) return false;
	const bvhvec3 l2tdelta = tdelta * (1.0f / groupDim);
	const bvhvec3 l3tdelta = l2tdelta * (1.0f / brickDim);
	uint32_t* gridBase = 0;
	// start stepping:
	while (t < ray.hit.t)
	{
		const uint32_t tidx = l1_.X + l1_.Y * topGridDim + l1_.Z * topGridDim * topGridDim;
		const uint32_t cell = topGrid[tidx >> 5] & (1 << (tidx & 31));
		if (cell)
		{
			// setup midlevel traversal
			const bvhvec3 posInGrid = (ray.O + (t + 0.0000025f) * ray.D) * (float)gridDim;
			const bvhvec3 gridPlanes = (bvhvec3( ceilf( posInGrid.x ), ceilf( posInGrid.y ), ceilf( posInGrid.z ) ) - Dsign) * (1.0f / gridDim);
			l2_.X = tinybvh_clamp( (int)posInGrid.x, l1_.X * groupDim, l1_.X * groupDim + (groupDim - 1) );
			l2_.Y = tinybvh_clamp( (int)posInGrid.y, l1_.Y * groupDim, l1_.Y * groupDim + (groupDim - 1) );
			l2_.Z = tinybvh_clamp( (int)posInGrid.z, l1_.Z * groupDim, l1_.Z * groupDim + (groupDim - 1) );
			l2_.tmax = (gridPlanes - ray.O) * ray.rD;
			gridBase = grid + ((l2_.X + l2_.Y * gridDim + l2_.Z * gridDim * gridDim) & superMask);
			l2_.X &= groupDim - 1, l2_.Y &= groupDim - 1, l2_.Z &= groupDim - 1;
			// step through midlevel cells
			while (1)
			{
				const uint32_t brickCell = gridBase[l2_.X + l2_.Y * gridDim + l2_.Z * gridDim * gridDim];
				if (brickCell)
				{
					// setup 3DDDA for brick traversal
					uint32_t* brickData = brick + brickCell * brickSize;
					const bvhvec3 posInBrick = (ray.O + (t + 0.0000025f) * ray.D) * (float)objectDim;
					uint32_t X = tinybvh_clamp( (int)posInBrick.x - (l2_.X + l1_.X * groupDim) * brickDim, 0u, brickDim - 1 );
					uint32_t Y = tinybvh_clamp( (int)posInBrick.y - (l2_.Y + l1_.Y * groupDim) * brickDim, 0u, brickDim - 1 );
					uint32_t Z = tinybvh_clamp( (int)posInBrick.z - (l2_.Z + l1_.Z * groupDim) * brickDim, 0u, brickDim - 1 );
					const bvhvec3 brickPlanes = (bvhvec3( ceilf( posInBrick.x ), ceilf( posInBrick.y ), ceilf( posInBrick.z ) ) - Dsign) * (1.0f / objectDim);
					bvhvec3 tmax = (brickPlanes - ray.O) * ray.rD;
					// step through brick
					while (1)
					{
						const uint32_t v = brickData[X + Y * brickDim + Z * brickDim * brickDim];
						if (v) return t < ray.hit.t;
						if (tmax.x < tmax.y)
						{
							if (tmax.x < tmax.z)
							{
								if ((X += step.x) >= brickDim) break;
								t = tmax.x, tmax.x += l3tdelta.x;
							}
							else
							{
								if ((Z += step.z) >= brickDim) break;
								t = tmax.z, tmax.z += l3tdelta.z;
							}
						}
						else
						{
							if (tmax.y < tmax.z)
							{
								if ((Y += step.y) >= brickDim) break;
								t = tmax.y, tmax.y += l3tdelta.y;
							}
							else
							{
								if ((Z += step.z) >= brickDim) break;
								t = tmax.z, tmax.z += l3tdelta.z;
							}
						}
					}
				}
				if (l2_.tmax.x < l2_.tmax.y)
				{
					if (l2_.tmax.x < l2_.tmax.z)
					{
						if ((l2_.X += step.x) >= groupDim) break;
						t = l2_.tmax.x, l2_.tmax.x += l2tdelta.x;
					}
					else
					{
						if ((l2_.Z += step.z) >= groupDim) break;
						t = l2_.tmax.z, l2_.tmax.z += l2tdelta.z;
					}
				}
				else
				{
					if (l2_.tmax.y < l2_.tmax.z)
					{
						if ((l2_.Y += step.y) >= groupDim) break;
						t = l2_.tmax.y, l2_.tmax.y += l2tdelta.y;
					}
					else
					{
						if ((l2_.Z += step.z) >= groupDim) break;
						t = l2_.tmax.z, l2_.tmax.z += l2tdelta.z;
					}
				}
			}
		}
		if (l1_.tmax.x < l1_.tmax.y)
		{
			if (l1_.tmax.x < l1_.tmax.z)
			{
				if ((l1_.X += step.x) >= topGridDim) break;
				t = l1_.tmax.x, l1_.tmax.x += tdelta.x;
			}
			else
			{
				if ((l1_.Z += step.z) >= topGridDim) break;
				t = l1_.tmax.z, l1_.tmax.z += tdelta.z;
			}
		}
		else
		{
			if (l1_.tmax.y < l1_.tmax.z)
			{
				if ((l1_.Y += step.y) >= topGridDim) break;
				t = l1_.tmax.y, l1_.tmax.y += tdelta.y;
			}
			else
			{
				if ((l1_.Z += step.z) >= topGridDim) break;
				t = l1_.tmax.z, l1_.tmax.z += tdelta.z;
			}
		}
	}
	// we shouldn't get here
	return false;
}

// BVH_Verbose implementation
// ----------------------------------------------------------------------------

BVH_Verbose::BVH_Verbose( BVH_Verbose&& other )
{
	*this = other;
	other.bvhNode = 0;
}

BVH_Verbose::~BVH_Verbose()
{
	AlignedFree( bvhNode );
}

void BVH_Verbose::ConvertFrom( const BVH& original, bool /* unused here */ )
{
	// allocate space
	uint32_t spaceNeeded = original.triCount * (refittable ? 2 : 3);
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		bvhNode = (BVHNode*)AlignedAlloc( sizeof( BVHNode ) * spaceNeeded );
		allocatedNodes = spaceNeeded;
	}
	memset( bvhNode, 0, sizeof( BVHNode ) * spaceNeeded );
	CopyBasePropertiesFrom( original );
	this->verts = original.verts;
	this->fragment = original.fragment;
	this->primIdx = original.primIdx;
	bvhNode[0].parent = 0xffffffff; // root sentinel
	// convert
	uint32_t nodeIdx = 0, parent = 0xffffffff, stack[128], stackPtr = 0;
	while (1)
	{
		const BVH::BVHNode& orig = original.bvhNode[nodeIdx];
		bvhNode[nodeIdx].aabbMin = orig.aabbMin, bvhNode[nodeIdx].aabbMax = orig.aabbMax;
		bvhNode[nodeIdx].triCount = orig.triCount, bvhNode[nodeIdx].parent = parent;
		if (orig.isLeaf())
		{
			bvhNode[nodeIdx].firstTri = orig.leftFirst;
			if (stackPtr == 0) break;
			nodeIdx = stack[--stackPtr];
			parent = stack[--stackPtr];
		}
		else
		{
			bvhNode[nodeIdx].left = orig.leftFirst;
			bvhNode[nodeIdx].right = orig.leftFirst + 1;
			stack[stackPtr++] = nodeIdx;
			stack[stackPtr++] = orig.leftFirst + 1;
			parent = nodeIdx;
			nodeIdx = orig.leftFirst;
		}
	}
	usedNodes = original.usedNodes;
}

int32_t BVH_Verbose::NodeCount() const
{
	// Determine the number of nodes in the tree. Typically the result should
	// be usedNodes - 1 (second node is always unused), but some builders may
	// have unused nodes besides node 1. TODO: Support more layouts.
	uint32_t retVal = 0, nodeIdx = 0, stack[64], stackPtr = 0;
	while (1)
	{
		const BVHNode& n = bvhNode[nodeIdx];
		retVal++;
		if (n.isLeaf()) { if (stackPtr == 0) break; else nodeIdx = stack[--stackPtr]; }
		else nodeIdx = n.left, stack[stackPtr++] = n.right;
	}
	return retVal;
}

float BVH_Verbose::SAHCost( const uint32_t nodeIdx ) const
{
	// Determine the SAH cost of the tree. This provides an indication
	// of the quality of the BVH: Lower is better.
	const BVHNode& n = bvhNode[nodeIdx];
	const float SAn = SA( n.aabbMin, n.aabbMax );
	if (n.isLeaf()) return c_int * SAn * n.triCount;
	float cost = c_trav * SAn + SAHCost( n.left ) + SAHCost( n.right );
	return nodeIdx == 0 ? (cost / SAn) : cost;
}

void BVH_Verbose::Refit( const uint32_t nodeIdx, bool skipLeafs )
{
	BVH_FATAL_ERROR_IF( !refittable && !skipLeafs, "BVH_Verbose::Refit( .. ), refitting an SBVH." );
	BVH_FATAL_ERROR_IF( bvhNode == 0, "BVH_Verbose::Refit( .. ), bvhNode == 0." );
	BVH_FATAL_ERROR_IF( bvh_over_indices && !skipLeafs, "BVH_Verbose::Refit( .. ), bvh used indexed tris." );
	BVHNode& node = bvhNode[nodeIdx];
	if (node.isLeaf()) // leaf: adjust to current triangle vertex positions
	{
		if (skipLeafs) return;
		bvhvec3 bmin( BVH_FAR ), bmax( -BVH_FAR );
		for (uint32_t first = node.firstTri, j = 0; j < node.triCount; j++)
		{
			const uint32_t vertIdx = primIdx[first + j] * 3;
			const bvhvec3 v0 = verts[vertIdx];
			const bvhvec3 v1 = verts[vertIdx + 1];
			const bvhvec3 v2 = verts[vertIdx + 2];
			bmin = tinybvh_min( bmin, v0 ), bmax = tinybvh_max( bmax, v0 );
			bmin = tinybvh_min( bmin, v1 ), bmax = tinybvh_max( bmax, v1 );
			bmin = tinybvh_min( bmin, v2 ), bmax = tinybvh_max( bmax, v2 );
		}
		node.aabbMin = bmin, node.aabbMax = bmax;
	}
	else
	{
		Refit( node.left, skipLeafs );
		Refit( node.right, skipLeafs );
		node.aabbMin = tinybvh_min( bvhNode[node.left].aabbMin, bvhNode[node.right].aabbMin );
		node.aabbMax = tinybvh_max( bvhNode[node.left].aabbMax, bvhNode[node.right].aabbMax );
	}
	if (nodeIdx == 0) aabbMin = node.aabbMin, aabbMax = node.aabbMax;
}

void BVH_Verbose::CheckFit( const uint32_t nodeIdx, bool skipLeafs )
{
	BVHNode& node = bvhNode[nodeIdx];
	bvhvec3 bmin( BVH_FAR ), bmax( -BVH_FAR );
	if (node.isLeaf()) // leaf: adjust to current triangle vertex positions
	{
		if (skipLeafs) return;
		for (uint32_t first = node.firstTri, j = 0; j < node.triCount; j++)
		{
			const uint32_t vertIdx = primIdx[first + j] * 3;
			const bvhvec3 v0 = verts[vertIdx];
			const bvhvec3 v1 = verts[vertIdx + 1];
			const bvhvec3 v2 = verts[vertIdx + 2];
			bmin = tinybvh_min( bmin, v0 ), bmax = tinybvh_max( bmax, v0 );
			bmin = tinybvh_min( bmin, v1 ), bmax = tinybvh_max( bmax, v1 );
			bmin = tinybvh_min( bmin, v2 ), bmax = tinybvh_max( bmax, v2 );
		}
	}
	else
	{
		CheckFit( node.left, skipLeafs );
		CheckFit( node.right, skipLeafs );
		bmin = tinybvh_min( bvhNode[node.left].aabbMin, bvhNode[node.right].aabbMin );
		bmax = tinybvh_max( bvhNode[node.left].aabbMax, bvhNode[node.right].aabbMax );
	}
}

void BVH_Verbose::Compact()
{
	BVH_FATAL_ERROR_IF( bvhNode == 0, "BVH_Verbose::Compact(), bvhNode == 0." );
	if (bvhNode[0].isLeaf()) return; // nothing to compact.
	BVHNode* tmp = (BVHNode*)AlignedAlloc( sizeof( BVHNode ) * usedNodes );
	memcpy( tmp, bvhNode, 2 * sizeof( BVHNode ) );
	uint32_t newNodePtr = 2, nodeIdx = 0, stack[64], stackPtr = 0;
	while (1)
	{
		BVHNode& node = tmp[nodeIdx];
		const BVHNode& left = bvhNode[node.left];
		const BVHNode& right = bvhNode[node.right];
		tmp[newNodePtr] = left, tmp[newNodePtr + 1] = right;
		const uint32_t todo1 = newNodePtr, todo2 = newNodePtr + 1;
		node.left = newNodePtr++, node.right = newNodePtr++;
		if (!left.isLeaf()) stack[stackPtr++] = todo1;
		if (!right.isLeaf()) stack[stackPtr++] = todo2;
		if (!stackPtr) break;
		nodeIdx = stack[--stackPtr];
	}
	usedNodes = newNodePtr;
	AlignedFree( bvhNode );
	bvhNode = tmp;
}

void BVH_Verbose::SortIndices()
{
	// create a new primIdx array which has the primitive indices sorted by depth-first traversal order.
	uint32_t nodeIdx = 0, stack[256], stackPtr = 0, * tmp = new uint32_t[triCount], nextIdx = 0;
	while (1)
	{
		BVHNode& node = bvhNode[nodeIdx];
		if (node.isLeaf())
		{
			uint32_t tmpFirst = nextIdx;
			for (unsigned i = 0; i < node.triCount; i++) tmp[nextIdx++] = primIdx[node.firstTri + i];
			node.firstTri = tmpFirst;
			if (stackPtr == 0) break; else nodeIdx = stack[--stackPtr];
			continue;
		}
		nodeIdx = node.left;
		stack[stackPtr++] = node.right;
	}
	memcpy( primIdx, tmp, triCount * 4 );
	delete[] tmp;
}

void BVH_Verbose::Optimize( const uint32_t iterations, const bool extreme, bool stochastic )
{
	// allocate array for sorting; size is upper-bound.
	SortItem* sortList = (SortItem*)AlignedAlloc( usedNodes * sizeof( SortItem ) );
	// optimize by reinserting subtrees with a high cost - Section 3.4 of the paper.
	for (uint32_t i = 0; i < iterations; i++)
	{
		// calculate combined cost for all nodes
		uint32_t interiorNodes = 0;
		for (uint32_t j = 2; j < usedNodes; j++)
		{
			const BVHNode& node = bvhNode[j];
			if (node.isLeaf()) continue;
			if (node.parent == 0) continue;
			if (bvhNode[node.parent].parent == 0) continue;
			const float A = node.SA(), AL = bvhNode[node.left].SA(), AR = bvhNode[node.right].SA();
			float Mmin = A / tinybvh_min( 1e-10f, tinybvh_min( AL, AR ) );
			float Msum = A / tinybvh_min( 1e-10f, 0.5f * (AL + AR) );
			float Mcomb = A * Msum * Mmin;
			sortList[interiorNodes].idx = j, sortList[interiorNodes++].cost = Mcomb;
		}
		// last couple of iterations we will process more nodes.
		const float portion = stochastic ? 0.5f : (extreme ? (0.01f + (0.6f * (float)i) / (float)iterations) : 0.01f);
		const int limit = (uint32_t)(portion * (float)interiorNodes);
		const int step = tinybvh_max( 1, (int)(portion / 0.02f) );
		// sort list - partial quick sort.
		struct Task { uint32_t first, last; } stack[4096];
		int pivot, first = 0, last = (int)interiorNodes - 1, stackPtr = 0;
		while (1)
		{
			if (first >= last)
			{
				if (stackPtr == 0) break; else first = stack[--stackPtr].first, last = stack[stackPtr].last;
				continue;
			}
			pivot = first;
			SortItem t, e = sortList[first];
			for (int j = first + 1; j <= last; j++) if (sortList[j].cost > e.cost)
				t = sortList[j], sortList[j] = sortList[++pivot], sortList[pivot] = t;
			t = sortList[pivot], sortList[pivot] = sortList[first], sortList[first] = t;
			if (pivot < limit) stack[stackPtr].first = pivot + 1, stack[stackPtr++].last = last;
			last = pivot - 1;
		}
		// reinsert selected nodes
		BVHNode bckp[5];
		int start = 0;
		if (stochastic)
		{
			float r = (float)rand() / RAND_MAX;
			r = tinybvh_max( 0.0f, (r * 1.2f) - 0.3f ); // 0 .. 0.9f
			start = (int)((float)limit * r);
		}
		bool finishingTouch = false;
		for (int j = start; j < limit; j += stochastic ? ((rand() & 63) + 1) : step)
		{
			// prepare change
			const uint32_t Nid = sortList[j].idx;
			BVHNode& N = bvhNode[Nid];
			if (N.parent == 0) continue;
			const uint32_t Pid = N.parent;
			BVHNode& P = bvhNode[Pid];
			if (P.parent == 0) continue;
			const uint32_t X1 = P.parent, X2 = (P.left == Nid ? P.right : P.left);
			// compute SAH before change
			float sahBefore = SAHCostUp( Nid );
			// execute change
			bckp[0] = bvhNode[X1];
			if (bvhNode[X1].left == Pid) bvhNode[X1].left = X2;
			else /* verbose[X1].right == Pid */ bvhNode[X1].right = X2;
			const uint32_t p2 = bvhNode[X2].parent;
			bvhNode[X2].parent = X1;
			const uint32_t Lid = N.left, Rid = N.right;
			RefitUp( X2 );
			// ReinsertNode( L, Nid ); ReinsertNode( R, Pid );
			const uint32_t Xbest1 = FindBestNewPosition( Lid ), XA = bvhNode[Xbest1].parent;
			sahBefore += SAHCostUp( Xbest1 );
			bckp[1] = bvhNode[Nid];
			N.left = Xbest1, N.right = Lid, N.parent = XA;
			bckp[2] = bvhNode[XA];
			if (bvhNode[XA].left == Xbest1) bvhNode[XA].left = Nid; else bvhNode[XA].right = Nid;
			const uint32_t p3 = bvhNode[Xbest1].parent, p4 = bvhNode[Lid].parent;
			bvhNode[Xbest1].parent = Nid, bvhNode[Lid].parent = Nid;
			RefitUp( Nid );
			const uint32_t Xbest2 = FindBestNewPosition( Rid ), XB = bvhNode[Xbest2].parent;
			sahBefore += SAHCostUp( Xbest2 );
			bckp[3] = bvhNode[Pid];
			P.left = Xbest2, P.right = Rid, P.parent = XB;
			bckp[4] = bvhNode[XB];
			if (bvhNode[XB].left == Xbest2) bvhNode[XB].left = Pid; else bvhNode[XB].right = Pid;
			const uint32_t p1 = bvhNode[Xbest2].parent, p0 = bvhNode[Rid].parent;
			bvhNode[Xbest2].parent = Pid, bvhNode[Rid].parent = Pid;
			RefitUp( Pid );
			// compute SAH after change
			float sahAfter = SAHCostUp( X1 ) + SAHCostUp( Nid ) + SAHCostUp( Pid );
			if (finishingTouch && (sahBefore / sahAfter > 1.01f)) break;
			if (sahAfter < sahBefore) continue;
			// undo change, mind the order.
			bvhNode[Rid].parent = p0, bvhNode[Xbest2].parent = p1, bvhNode[XB] = bckp[4];
			bvhNode[Pid] = bckp[3], bvhNode[Lid].parent = p4, bvhNode[Xbest1].parent = p3;
			bvhNode[XA] = bckp[2], bvhNode[Nid] = bckp[1], bvhNode[X2].parent = p2, bvhNode[X1] = bckp[0];
			RefitUp( XB );
			RefitUp( XA );
			RefitUp( Nid );
		}
		Refit( 0, true );
	}
	AlignedFree( sortList );
}

// Single-primitive leafs: Prepare the BVH for optimization. While it is not strictly
// necessary to have a single primitive per leaf, it will yield a slightly better
// optimized BVH. The leafs of the optimized BVH should be collapsed ('MergeLeafs')
// to obtain the final tree.
void BVH_Verbose::SplitLeafs( const uint32_t maxPrims )
{
	uint32_t nodeIdx = 0, stack[64], stackPtr = 0;
	while (1)
	{
		BVHNode& node = bvhNode[nodeIdx];
		if (!node.isLeaf()) nodeIdx = node.left, stack[stackPtr++] = node.right; else
		{
			// split this leaf
			if (node.triCount > maxPrims)
			{
				const uint32_t newIdx1 = usedNodes++, newIdx2 = usedNodes++;
				BVHNode& new1 = bvhNode[newIdx1], & new2 = bvhNode[newIdx2];
				new1.firstTri = node.firstTri, new1.triCount = node.triCount / 2;
				new1.parent = new2.parent = nodeIdx, new1.left = new1.right = 0;
				new2.firstTri = node.firstTri + new1.triCount;
				new2.triCount = node.triCount - new1.triCount, new2.left = new2.right = 0;
				node.left = newIdx1, node.right = newIdx2, node.triCount = 0;
				new1.aabbMin = new2.aabbMin = bvhvec3( BVH_FAR ), new1.aabbMax = new2.aabbMax = bvhvec3( -BVH_FAR );
				for (uint32_t fi, i = 0; i < new1.triCount; i++)
					fi = primIdx[new1.firstTri + i],
					new1.aabbMin = tinybvh_min( new1.aabbMin, fragment[fi].bmin ),
					new1.aabbMax = tinybvh_max( new1.aabbMax, fragment[fi].bmax );
				for (uint32_t fi, i = 0; i < new2.triCount; i++)
					fi = primIdx[new2.firstTri + i],
					new2.aabbMin = tinybvh_min( new2.aabbMin, fragment[fi].bmin ),
					new2.aabbMax = tinybvh_max( new2.aabbMax, fragment[fi].bmax );
				// recurse
				if (new1.triCount > 1) stack[stackPtr++] = newIdx1;
				if (new2.triCount > 1) stack[stackPtr++] = newIdx2;
			}
			if (stackPtr == 0) break; else nodeIdx = stack[--stackPtr];
		}
	}
}

// MergeLeafs: After optimizing a BVH, single-primitive leafs should be merged whenever
// SAH indicates this is an improvement.
void BVH_Verbose::MergeLeafs()
{
	// allocate some working space
	uint32_t* subtreeTriCount = (uint32_t*)AlignedAlloc( usedNodes * 4 );
	uint32_t* newIdx = (uint32_t*)AlignedAlloc( idxCount * 4 );
	memset( subtreeTriCount, 0, usedNodes * 4 );
	CountSubtreeTris( 0, subtreeTriCount );
	uint32_t stack[64], stackPtr = 0, nodeIdx = 0, newIdxPtr = 0;
	while (1)
	{
		BVHNode& node = bvhNode[nodeIdx];
		if (node.isLeaf())
		{
			uint32_t start = newIdxPtr;
			MergeSubtree( nodeIdx, newIdx, newIdxPtr );
			node.firstTri = start;
			// pop new task
			if (stackPtr == 0) break;
			nodeIdx = stack[--stackPtr];
		}
		else
		{
			const uint32_t leftCount = subtreeTriCount[node.left];
			const uint32_t rightCount = subtreeTriCount[node.right];
			const uint32_t mergedCount = leftCount + rightCount;
			// cost of unsplit
			float Cunsplit = SA( node.aabbMin, node.aabbMax ) * mergedCount * c_int;
			// cost of leaving things as they are
			BVHNode& left = bvhNode[node.left];
			BVHNode& right = bvhNode[node.right];
			float Ckeepsplit = c_trav + c_int * (left.SA() * leftCount + right.SA() * rightCount);
			if (Cunsplit <= Ckeepsplit)
			{
				// collapse the subtree
				uint32_t start = newIdxPtr;
				MergeSubtree( nodeIdx, newIdx, newIdxPtr );
				node.firstTri = start, node.triCount = mergedCount;
				node.left = node.right = 0;
				// pop new task
				if (stackPtr == 0) break;
				nodeIdx = stack[--stackPtr];
			}
			else /* recurse */ nodeIdx = node.left, stack[stackPtr++] = node.right;
		}
	}
	// cleanup
	AlignedFree( subtreeTriCount );
	AlignedFree( primIdx );
	primIdx = newIdx, may_have_holes = true; // all over the place, in fact
}

// BVH_GPU implementation
// ----------------------------------------------------------------------------

BVH_GPU::BVH_GPU( BVH_GPU&& other )
{
	*this = other;
	other.bvhNode = 0;
}

BVH_GPU::~BVH_GPU()
{
	if (!ownBVH) bvh = BVH(); // clear out pointers we don't own.
	AlignedFree( bvhNode );
}

void BVH_GPU::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH_GPU::Build( const bvhvec4slice& vertices )
{
	bvh.context = context;
	bvh.BuildDefault( vertices );
	ConvertFrom( bvh, false );
}

void BVH_GPU::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH_GPU::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh.context = context;
	bvh.BuildDefault( vertices, indices, prims );
	ConvertFrom( bvh, false );
}

void BVH_GPU::Build( BLASInstance* instances, const uint32_t instCount, BVHBase** blasses, const uint32_t blasCount )
{
	// build a TLAS based on the array of BLASInstance records.
	bvh.context = context;
	bvh.Build( instances, instCount, blasses, blasCount );
	ConvertFrom( bvh, false );
}

void BVH_GPU::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH_GPU::BuildHQ( const bvhvec4slice& vertices )
{
	bvh.BuildHQ( vertices );
	ConvertFrom( bvh, false );
}

void BVH_GPU::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	BuildHQ( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH_GPU::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh.context = context;
	bvh.BuildHQ( vertices, indices, prims );
	ConvertFrom( bvh, false );
}

void BVH_GPU::Optimize( const uint32_t iterations, bool extreme )
{
	bvh.Optimize( iterations, extreme );
	ConvertFrom( bvh, false );
}

void BVH_GPU::ConvertFrom( const BVH& original, bool compact )
{
	// get a copy of the original bvh
	if (&original != &bvh) ownBVH = false; // bvh isn't ours; don't delete in destructor.
	bvh = original;
	// allocate space
	const uint32_t spaceNeeded = compact ? original.usedNodes : original.allocatedNodes;
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		bvhNode = (BVHNode*)AlignedAlloc( sizeof( BVHNode ) * spaceNeeded );
		allocatedNodes = spaceNeeded;
	}
	memset( bvhNode, 0, sizeof( BVHNode ) * spaceNeeded );
	CopyBasePropertiesFrom( original );
	// recursively convert nodes
	uint32_t newNodePtr = 0, nodeIdx = 0, stack[128], stackPtr = 0;
	while (1)
	{
		const BVH::BVHNode& orig = original.bvhNode[nodeIdx];
		const uint32_t idx = newNodePtr++;
		if (orig.isLeaf())
		{
			this->bvhNode[idx].triCount = orig.triCount;
			this->bvhNode[idx].firstTri = orig.leftFirst;
			if (!stackPtr) break;
			nodeIdx = stack[--stackPtr];
			uint32_t newNodeParent = stack[--stackPtr];
			this->bvhNode[newNodeParent].right = newNodePtr;
		}
		else
		{
			const BVH::BVHNode& left = original.bvhNode[orig.leftFirst];
			const BVH::BVHNode& right = original.bvhNode[orig.leftFirst + 1];
			this->bvhNode[idx].lmin = left.aabbMin, this->bvhNode[idx].rmin = right.aabbMin;
			this->bvhNode[idx].lmax = left.aabbMax, this->bvhNode[idx].rmax = right.aabbMax;
			this->bvhNode[idx].left = newNodePtr; // right will be filled when popped
			stack[stackPtr++] = idx;
			stack[stackPtr++] = orig.leftFirst + 1;
			nodeIdx = orig.leftFirst;
		}
	}
	usedNodes = newNodePtr;
}

int32_t BVH_GPU::Intersect( Ray& ray ) const
{
	VALIDATE_RAY( ray );
	BVHNode* node = &bvhNode[0], * stack[64];
	const bvhvec4slice& verts = bvh.verts;
	const uint32_t* primIdx = bvh.primIdx;
	uint32_t stackPtr = 0;
	float cost = 0;
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			if (indexedEnabled && bvh.vertIdx != 0) for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t pi = primIdx[node->firstTri + i];
				const uint32_t i0 = bvh.vertIdx[pi * 3], i1 = bvh.vertIdx[pi * 3 + 1], i2 = bvh.vertIdx[pi * 3 + 2];
				IntersectTri( ray, pi, verts, i0, i1, i2 );
			}
			else for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t pi = primIdx[node->firstTri + i];
				IntersectTri( ray, pi, verts, pi * 3, pi * 3 + 1, pi * 3 + 2 );
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		const bvhvec3 lmin = node->lmin - ray.O, lmax = node->lmax - ray.O;
		const bvhvec3 rmin = node->rmin - ray.O, rmax = node->rmax - ray.O;
		float dist1 = BVH_FAR, dist2 = BVH_FAR;
		const bvhvec3 t1a = lmin * ray.rD, t2a = lmax * ray.rD;
		const bvhvec3 t1b = rmin * ray.rD, t2b = rmax * ray.rD;
		const float tmina = tinybvh_max( tinybvh_max( tinybvh_min( t1a.x, t2a.x ), tinybvh_min( t1a.y, t2a.y ) ), tinybvh_min( t1a.z, t2a.z ) );
		const float tmaxa = tinybvh_min( tinybvh_min( tinybvh_max( t1a.x, t2a.x ), tinybvh_max( t1a.y, t2a.y ) ), tinybvh_max( t1a.z, t2a.z ) );
		const float tminb = tinybvh_max( tinybvh_max( tinybvh_min( t1b.x, t2b.x ), tinybvh_min( t1b.y, t2b.y ) ), tinybvh_min( t1b.z, t2b.z ) );
		const float tmaxb = tinybvh_min( tinybvh_min( tinybvh_max( t1b.x, t2b.x ), tinybvh_max( t1b.y, t2b.y ) ), tinybvh_max( t1b.z, t2b.z ) );
		if (tmaxa >= tmina && tmina < ray.hit.t && tmaxa >= 0) dist1 = tmina;
		if (tmaxb >= tminb && tminb < ray.hit.t && tmaxb >= 0) dist2 = tminb;
		uint32_t lidx = node->left, ridx = node->right;
		if (dist1 > dist2)
		{
			float t = dist1; dist1 = dist2; dist2 = t;
			uint32_t i = lidx; lidx = ridx; ridx = i;
		}
		if (dist1 == BVH_FAR)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else
		{
			node = bvhNode + lidx;
			if (dist2 != BVH_FAR) stack[stackPtr++] = bvhNode + ridx;
		}
	}
	return (int32_t)cost; // cast to not break interface.
}

// BVH_SoA implementation
// ----------------------------------------------------------------------------

BVH_SoA::BVH_SoA( BVH_SoA&& other )
{
	*this = other;
	other.bvhNode = 0;
}

BVH_SoA::~BVH_SoA()
{
	if (!ownBVH) bvh = BVH(); // clear out pointers we don't own.
	AlignedFree( bvhNode );
}

void BVH_SoA::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH_SoA::Build( const bvhvec4slice& vertices )
{
	bvh.context = context; // properly propagate context to fix issue #66.
	bvh.BuildDefault( vertices );
	ConvertFrom( bvh, false );
}

void BVH_SoA::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH_SoA::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh.context = context;
	bvh.BuildDefault( vertices, indices, prims );
	ConvertFrom( bvh, false );
}

void BVH_SoA::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH_SoA::BuildHQ( const bvhvec4slice& vertices )
{
	bvh.context = context; // properly propagate context to fix issue #66.
	bvh.BuildHQ( vertices );
	ConvertFrom( bvh, false );
}

void BVH_SoA::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	BuildHQ( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH_SoA::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh.context = context;
	bvh.BuildHQ( vertices, indices, prims );
	ConvertFrom( bvh, false );
}

void BVH_SoA::Optimize( const uint32_t iterations, bool extreme )
{
	bvh.Optimize( iterations, extreme );
	ConvertFrom( bvh, false );
}

void BVH_SoA::Save( const char* fileName )
{
	bvh.Save( fileName );
}

bool BVH_SoA::Load( const char* fileName, const bvhvec4* vertices, const uint32_t primCount )
{
	return Load( fileName, bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}

bool BVH_SoA::Load( const char* fileName, const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount )
{
	return Load( fileName, bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) }, indices, primCount );
}

bool BVH_SoA::Load( const char* fileName, const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount )
{
	if (!bvh.Load( fileName, vertices, indices, primCount )) return false;
	ConvertFrom( bvh, false );
	return true;
}

void BVH_SoA::ConvertFrom( const BVH& original, bool compact )
{
	// get a copy of the original bvh
	if (&original != &bvh) ownBVH = false; // bvh isn't ours; don't delete in destructor.
	bvh = original;
	// allocate space
	const uint32_t spaceNeeded = compact ? bvh.usedNodes : bvh.allocatedNodes;
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		bvhNode = (BVHNode*)AlignedAlloc( sizeof( BVHNode ) * spaceNeeded );
		allocatedNodes = spaceNeeded;
	}
	memset( bvhNode, 0, sizeof( BVHNode ) * spaceNeeded );
	CopyBasePropertiesFrom( bvh );
	// recursively convert nodes
	uint32_t newAlt2Node = 0, nodeIdx = 0, stack[128], stackPtr = 0;
	while (1)
	{
		const BVH::BVHNode& node = bvh.bvhNode[nodeIdx];
		const uint32_t idx = newAlt2Node++;
		if (node.isLeaf())
		{
			bvhNode[idx].triCount = node.triCount;
			bvhNode[idx].firstTri = node.leftFirst;
			if (!stackPtr) break;
			nodeIdx = stack[--stackPtr];
			uint32_t newNodeParent = stack[--stackPtr];
			bvhNode[newNodeParent].right = newAlt2Node;
		}
		else
		{
			const BVH::BVHNode& left = bvh.bvhNode[node.leftFirst];
			const BVH::BVHNode& right = bvh.bvhNode[node.leftFirst + 1];
			// This BVH layout requires BVH_USEAVX/BVH_USENEON for traversal, but at least we
			// can convert to it without SSE/AVX/NEON support.
			bvhNode[idx].xxxx = SIMD_SETRVEC( left.aabbMin.x, left.aabbMax.x, right.aabbMin.x, right.aabbMax.x );
			bvhNode[idx].yyyy = SIMD_SETRVEC( left.aabbMin.y, left.aabbMax.y, right.aabbMin.y, right.aabbMax.y );
			bvhNode[idx].zzzz = SIMD_SETRVEC( left.aabbMin.z, left.aabbMax.z, right.aabbMin.z, right.aabbMax.z );
			bvhNode[idx].left = newAlt2Node; // right will be filled when popped
			stack[stackPtr++] = idx;
			stack[stackPtr++] = node.leftFirst + 1;
			nodeIdx = node.leftFirst;
		}
	}
	usedNodes = newAlt2Node;
}

// BVH_SoA::Intersect can be found in the BVH_USEAVX section later in this file.

// Generic (templated) MBVH implementation
// ----------------------------------------------------------------------------

template<int M> MBVH<M>::MBVH( MBVH<M>&& other )
{
	*this = other;
	other.mbvhNode = 0;
}

template<int M> MBVH<M>::~MBVH()
{
	if (!ownBVH) bvh = BVH(); // clear out pointers we don't own.
	AlignedFree( mbvhNode );
}

template<int M> void MBVH<M>::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

template<int M> void MBVH<M>::Build( const bvhvec4slice& vertices )
{
	bvh.context = context; // properly propagate context to fix issue #66.
	bvh.BuildDefault( vertices );
	ConvertFrom( bvh, false );
}

template<int M> void MBVH<M>::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

template<int M> void MBVH<M>::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh.context = context;
	bvh.BuildDefault( vertices, indices, prims );
	ConvertFrom( bvh, true );
}

template<int M> void MBVH<M>::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

template<int M> void MBVH<M>::BuildHQ( const bvhvec4slice& vertices )
{
	bvh.context = context;
	bvh.BuildHQ( vertices );
	ConvertFrom( bvh, true );
}

template<int M> void MBVH<M>::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

template<int M> void MBVH<M>::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh.context = context;
	bvh.BuildHQ( vertices, indices, prims );
	ConvertFrom( bvh, true );
}

template<int M> void MBVH<M>::Optimize( const uint32_t iterations, bool extreme )
{
	bvh.Optimize( iterations, extreme );
	ConvertFrom( bvh, true );
}

template<int M> uint32_t MBVH<M>::LeafCount( const uint32_t nodeIdx ) const
{
	MBVHNode& node = mbvhNode[nodeIdx];
	if (node.isLeaf()) return 1;
	uint32_t count = 0;
	for (uint32_t i = 0; i < node.childCount; i++) count += LeafCount( node.child[i] );
	return count;
}

template<int M> void MBVH<M>::Refit( const uint32_t nodeIdx )
{
	MBVHNode& node = mbvhNode[nodeIdx];
	if (node.isLeaf())
	{
		bvhvec3 bmin( BVH_FAR ), bmax( -BVH_FAR );
		if (bvh.vertIdx) for (uint32_t first = node.firstTri, j = 0; j < node.triCount; j++)
		{
			const uint32_t vidx = bvh.primIdx[first + j] * 3;
			const uint32_t i0 = bvh.vertIdx[vidx], i1 = bvh.vertIdx[vidx + 1], i2 = bvh.vertIdx[vidx + 2];
			const bvhvec3 v0 = bvh.verts[i0], v1 = bvh.verts[i1], v2 = bvh.verts[i2];
			bmin = tinybvh_min( bmin, tinybvh_min( tinybvh_min( v0, v1 ), v2 ) );
			bmax = tinybvh_max( bmax, tinybvh_max( tinybvh_max( v0, v1 ), v2 ) );
		}
		else for (uint32_t first = node.firstTri, j = 0; j < node.triCount; j++)
		{
			const uint32_t vidx = bvh.primIdx[first + j] * 3;
			const bvhvec3 v0 = bvh.verts[vidx], v1 = bvh.verts[vidx + 1], v2 = bvh.verts[vidx + 2];
			bmin = tinybvh_min( bmin, tinybvh_min( tinybvh_min( v0, v1 ), v2 ) );
			bmax = tinybvh_max( bmax, tinybvh_max( tinybvh_max( v0, v1 ), v2 ) );
		}
		node.aabbMin = bmin, node.aabbMax = bmax;
	}
	else
	{
		for (unsigned i = 0; i < node.childCount; i++) Refit( node.child[i] );
		MBVHNode& firstChild = mbvhNode[node.child[0]];
		bvhvec3 bmin = firstChild.aabbMin, bmax = firstChild.aabbMax;
		for (unsigned i = 1; i < node.childCount; i++)
		{
			MBVHNode& child = mbvhNode[node.child[i]];
			bmin = tinybvh_min( bmin, child.aabbMin );
			bmax = tinybvh_max( bmax, child.aabbMax );
		}
		node.aabbMin = bmin, node.aabbMax = bmax;
	}
	if (nodeIdx == 0) aabbMin = node.aabbMin, aabbMax = node.aabbMax;
}

template<int M> float MBVH<M>::SAHCost( const uint32_t nodeIdx ) const
{
	// Determine the SAH cost of the tree. This provides an indication
	// of the quality of the BVH: Lower is better.
	const MBVHNode& n = mbvhNode[nodeIdx];
	const float sa = BVH::SA( n.aabbMin, n.aabbMax );
	if (n.isLeaf()) return c_int * sa * n.triCount;
	float cost = c_trav * sa;
	for (unsigned i = 0; i < M; i++) if (n.child[i] != 0) cost += SAHCost( n.child[i] );
	return nodeIdx == 0 ? (cost / sa) : cost;
}

template<int M> void MBVH<M>::ConvertFrom( const BVH& original, bool compact )
{
	// get a copy of the original bvh
	if (&original != &bvh) ownBVH = false; // bvh isn't ours; don't delete in destructor.
	bvh = original;
	// allocate space
	uint32_t spaceNeeded = compact ? original.usedNodes : original.allocatedNodes;
	constexpr bool M8 = M == 8;
	if (M8) spaceNeeded += original.usedNodes >> 1; // cwbvh / SplitLeafs
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( mbvhNode );
		mbvhNode = (MBVHNode*)AlignedAlloc( spaceNeeded * sizeof( MBVHNode ) );
		allocatedNodes = spaceNeeded;
	}
	memset( mbvhNode, 0, sizeof( MBVHNode ) * spaceNeeded );
	CopyBasePropertiesFrom( original );
	// create an mbvh node for each bvh2 node
	for (uint32_t i = 0; i < original.usedNodes; i++) if (i != 1)
	{
		BVH::BVHNode& orig = original.bvhNode[i];
		MBVHNode& node = this->mbvhNode[i];
		node.aabbMin = orig.aabbMin, node.aabbMax = orig.aabbMax;
		if (orig.isLeaf()) node.triCount = orig.triCount, node.firstTri = orig.leftFirst;
		else node.child[0] = orig.leftFirst, node.child[1] = orig.leftFirst + 1, node.childCount = 2;
	}
	// collapse
	uint32_t stack[128], stackPtr = 0, nodeIdx = 0; // i.e., root node
	while (1)
	{
		MBVHNode& node = this->mbvhNode[nodeIdx];
		while (node.childCount < M)
		{
			int32_t bestChild = -1;
			float bestChildSA = 0;
			for (uint32_t i = 0; i < node.childCount; i++)
			{
				// see if we can adopt child i
				const MBVHNode& child = this->mbvhNode[node.child[i]];
				if (!child.isLeaf() && node.childCount - 1 + child.childCount <= M)
				{
					const float childSA = SA( child.aabbMin, child.aabbMax );
					if (childSA > bestChildSA) bestChild = i, bestChildSA = childSA;
				}
			}
			if (bestChild == -1) break; // could not adopt
			const MBVHNode& child = this->mbvhNode[node.child[bestChild]];
			node.child[bestChild] = child.child[0];
			for (uint32_t i = 1; i < child.childCount; i++)
				node.child[node.childCount++] = child.child[i];
		}
		// we're done with the node; proceed with the children.
		for (uint32_t i = 0; i < node.childCount; i++)
		{
			const uint32_t childIdx = node.child[i];
			const MBVHNode& child = this->mbvhNode[childIdx];
			if (!child.isLeaf()) stack[stackPtr++] = childIdx;
		}
		if (stackPtr == 0) break;
		nodeIdx = stack[--stackPtr];
	}
	// special case where root is leaf: add extra level - cwbvh needs this.
	MBVHNode& root = this->mbvhNode[0];
	if (root.isLeaf())
	{
		mbvhNode[1] = root;
		root.childCount = 1;
		root.child[0] = 1;
		root.triCount = 0;
	}
	// finalize
	usedNodes = original.usedNodes;
	this->may_have_holes = true;
}

// BVH4_GPU implementation
// ----------------------------------------------------------------------------

BVH4_GPU::BVH4_GPU( BVH4_GPU&& other )
{
	*this = other;
	other.bvh4Data = 0;
}

BVH4_GPU::~BVH4_GPU()
{
	if (!ownBVH4) bvh4 = MBVH<4>(); // clear out pointers we don't own.
	AlignedFree( bvh4Data );
}

void BVH4_GPU::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH4_GPU::Build( const bvhvec4slice& vertices )
{
	bvh4.context = context;
	bvh4.Build( vertices );
	ConvertFrom( bvh4, true );
}

void BVH4_GPU::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH4_GPU::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh4.context = context;
	bvh4.Build( vertices, indices, prims );
	ConvertFrom( bvh4, true );
}

void BVH4_GPU::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH4_GPU::BuildHQ( const bvhvec4slice& vertices )
{
	bvh4.context = context;
	bvh4.BuildHQ( vertices );
	ConvertFrom( bvh4, true );
}

void BVH4_GPU::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH4_GPU::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh4.context = context;
	bvh4.BuildHQ( vertices, indices, prims );
	ConvertFrom( bvh4, true );
}

void BVH4_GPU::Optimize( const uint32_t iterations, bool extreme )
{
	bvh4.Optimize( iterations, extreme );
	ConvertFrom( bvh4, true );
}

void BVH4_GPU::ConvertFrom( const MBVH<4>& original, bool compact )
{
	// get a copy of the original bvh4
	if (&original != &bvh4) ownBVH4 = false; // bvh isn't ours; don't delete in destructor.
	bvh4 = original;
	// Convert a 4-wide BVH to a format suitable for GPU traversal. Layout:
	// offs 0:   aabbMin (12 bytes), 4x quantized child xmin (4 bytes)
	// offs 16:  aabbMax (12 bytes), 4x quantized child xmax (4 bytes)
	// offs 32:  4x child ymin, then ymax, zmax, zmax (total 16 bytes)
	// offs 48:  4x child node info: leaf if MSB set.
	//           Leaf: 15 bits for tri count, 16 for offset
	//           Interior: 32 bits for position of child node.
	// Triangle data ('by value') immediately follows each leaf node.
	uint32_t blocksNeeded = compact ? (bvh4.usedNodes * 4) : (bvh4.allocatedNodes * 4); // here, 'block' is 16 bytes.
	blocksNeeded += 6 * bvh4.triCount; // this layout stores tris in the same buffer.
	if (allocatedBlocks < blocksNeeded)
	{
		AlignedFree( bvh4Data );
		bvh4Data = (bvhvec4*)AlignedAlloc( blocksNeeded * 16 );
		allocatedBlocks = blocksNeeded;
	}
	memset( bvh4Data, 0, 16 * blocksNeeded );
	CopyBasePropertiesFrom( bvh4 );
	// start conversion
	uint32_t nodeIdx = 0, newAlt4Ptr = 0, stack[128], stackPtr = 0, retValPos = 0;
	while (1)
	{
		const MBVH<4>::MBVHNode& orig = bvh4.mbvhNode[nodeIdx];
		// convert BVH4 node - must be an interior node.
		assert( !orig.isLeaf() );
		bvhvec4* nodeBase = bvh4Data + newAlt4Ptr;
		uint32_t baseAlt4Ptr = newAlt4Ptr;
		newAlt4Ptr += 4;
		nodeBase[0] = bvhvec4( orig.aabbMin, 0 );
		nodeBase[1] = bvhvec4( (orig.aabbMax - orig.aabbMin) * (1.0f / 255.0f), 0 );
		MBVH<4>::MBVHNode* childNode[4] = {
			&bvh4.mbvhNode[orig.child[0]], &bvh4.mbvhNode[orig.child[1]],
			&bvh4.mbvhNode[orig.child[2]], &bvh4.mbvhNode[orig.child[3]]
		};
		// start with leaf child node conversion
		uint32_t childInfo[4] = { 0, 0, 0, 0 }; // will store in final fields later
		for (int32_t i = 0; i < 4; i++) if (childNode[i]->isLeaf())
		{
			childInfo[i] = newAlt4Ptr - baseAlt4Ptr;
			childInfo[i] |= childNode[i]->triCount << 16;
			childInfo[i] |= 0x80000000;
			for (uint32_t j = 0; j < childNode[i]->triCount; j++)
			{
				uint32_t t = bvh4.bvh.primIdx[childNode[i]->firstTri + j];
				uint32_t ti0, ti1, ti2;
				if (bvh4.bvh.vertIdx)
					ti0 = bvh4.bvh.vertIdx[t * 3],
					ti1 = bvh4.bvh.vertIdx[t * 3 + 1],
					ti2 = bvh4.bvh.vertIdx[t * 3 + 2];
				else
					ti0 = t * 3, ti1 = t * 3 + 1, ti2 = t * 3 + 2;
			#ifdef BVH4_GPU_COMPRESSED_TRIS
				PrecomputeTriangle( verts, ti0, ti1, ti2, (float*)&bvh4Alt[newAlt4Ptr] );
				bvh4Alt[newAlt4Ptr + 3] = bvhvec4( 0, 0, 0, *(float*)&t );
				newAlt4Ptr += 4;
			#else
				bvhvec4 v0 = bvh4.bvh.verts[ti0];
				bvh4Data[newAlt4Ptr + 1] = bvh4.bvh.verts[ti1] - v0;
				bvh4Data[newAlt4Ptr + 2] = bvh4.bvh.verts[ti2] - v0;
				v0.w = *(float*)&t; // as_float
				bvh4Data[newAlt4Ptr + 0] = v0;
				newAlt4Ptr += 3;
			#endif
			}
		}
		// process interior nodes
		for (int32_t i = 0; i < 4; i++) if (!childNode[i]->isLeaf())
		{
			// childInfo[i] = node.child[i] == 0 ? 0 : GPUFormatBVH4( node.child[i] );
			if (orig.child[i] == 0) childInfo[i] = 0; else
			{
				stack[stackPtr++] = (uint32_t)(((float*)&nodeBase[3] + i) - (float*)bvh4Data);
				stack[stackPtr++] = orig.child[i];
			}
		}
		// store child node bounds, quantized
		const bvhvec3 extent = orig.aabbMax - orig.aabbMin;
		bvhvec3 scale;
		scale.x = extent.x > 1e-10f ? (254.999f / extent.x) : 0;
		scale.y = extent.y > 1e-10f ? (254.999f / extent.y) : 0;
		scale.z = extent.z > 1e-10f ? (254.999f / extent.z) : 0;
		uint8_t* slot0 = (uint8_t*)&nodeBase[0] + 12;	// 4 chars
		uint8_t* slot1 = (uint8_t*)&nodeBase[1] + 12;	// 4 chars
		uint8_t* slot2 = (uint8_t*)&nodeBase[2];		// 16 chars
		if (orig.child[0])
		{
			const bvhvec3 relBMin = childNode[0]->aabbMin - orig.aabbMin, relBMax = childNode[0]->aabbMax - orig.aabbMin;
			slot0[0] = (uint8_t)floorf( relBMin.x * scale.x ), slot1[0] = (uint8_t)ceilf( relBMax.x * scale.x );
			slot2[0] = (uint8_t)floorf( relBMin.y * scale.y ), slot2[4] = (uint8_t)ceilf( relBMax.y * scale.y );
			slot2[8] = (uint8_t)floorf( relBMin.z * scale.z ), slot2[12] = (uint8_t)ceilf( relBMax.z * scale.z );
		}
		if (orig.child[1])
		{
			const bvhvec3 relBMin = childNode[1]->aabbMin - orig.aabbMin, relBMax = childNode[1]->aabbMax - orig.aabbMin;
			slot0[1] = (uint8_t)floorf( relBMin.x * scale.x ), slot1[1] = (uint8_t)ceilf( relBMax.x * scale.x );
			slot2[1] = (uint8_t)floorf( relBMin.y * scale.y ), slot2[5] = (uint8_t)ceilf( relBMax.y * scale.y );
			slot2[9] = (uint8_t)floorf( relBMin.z * scale.z ), slot2[13] = (uint8_t)ceilf( relBMax.z * scale.z );
		}
		if (orig.child[2])
		{
			const bvhvec3 relBMin = childNode[2]->aabbMin - orig.aabbMin, relBMax = childNode[2]->aabbMax - orig.aabbMin;
			slot0[2] = (uint8_t)floorf( relBMin.x * scale.x ), slot1[2] = (uint8_t)ceilf( relBMax.x * scale.x );
			slot2[2] = (uint8_t)floorf( relBMin.y * scale.y ), slot2[6] = (uint8_t)ceilf( relBMax.y * scale.y );
			slot2[10] = (uint8_t)floorf( relBMin.z * scale.z ), slot2[14] = (uint8_t)ceilf( relBMax.z * scale.z );
		}
		if (orig.child[3])
		{
			const bvhvec3 relBMin = childNode[3]->aabbMin - orig.aabbMin, relBMax = childNode[3]->aabbMax - orig.aabbMin;
			slot0[3] = (uint8_t)floorf( relBMin.x * scale.x ), slot1[3] = (uint8_t)ceilf( relBMax.x * scale.x );
			slot2[3] = (uint8_t)floorf( relBMin.y * scale.y ), slot2[7] = (uint8_t)ceilf( relBMax.y * scale.y );
			slot2[11] = (uint8_t)floorf( relBMin.z * scale.z ), slot2[15] = (uint8_t)ceilf( relBMax.z * scale.z );
		}
		// finalize node
		nodeBase[3] = bvhvec4(
			*(float*)&childInfo[0], *(float*)&childInfo[1],
			*(float*)&childInfo[2], *(float*)&childInfo[3]
		);
		// pop new work from the stack
		if (retValPos > 0) ((uint32_t*)bvh4Data)[retValPos] = baseAlt4Ptr;
		if (stackPtr == 0) break;
		nodeIdx = stack[--stackPtr];
		retValPos = stack[--stackPtr];
	}
	usedBlocks = newAlt4Ptr;
}

// IntersectAlt4Nodes. For testing the converted data only; not efficient.
// This code replicates how traversal on GPU happens.
#define SWAP(A,B,C,D) tmp=A,A=B,B=tmp,tmp2=C,C=D,D=tmp2;
struct uchar4 { uint8_t x, y, z, w; };
static uchar4 as_uchar4( const float v ) { union { float t; uchar4 t4; }; t = v; return t4; }
static uint32_t as_uint( const float v ) { return *(uint32_t*)&v; }
int32_t BVH4_GPU::Intersect( Ray& ray ) const
{
	// traverse a blas
	VALIDATE_RAY( ray );
	uint32_t offset = 0, stack[128], stackPtr = 0, tmp2 /* for SWAP macro */;
	float cost = 0;
	while (1)
	{
		cost += c_trav;
		// fetch the node
		const bvhvec4 data0 = bvh4Data[offset + 0], data1 = bvh4Data[offset + 1];
		const bvhvec4 data2 = bvh4Data[offset + 2], data3 = bvh4Data[offset + 3];
		// extract aabb
		const bvhvec3 bmin = data0, extent = data1; // pre-scaled by 1/255
		// reconstruct conservative child aabbs
		const uchar4 d0 = as_uchar4( data0.w ), d1 = as_uchar4( data1.w ), d2 = as_uchar4( data2.x );
		const uchar4 d3 = as_uchar4( data2.y ), d4 = as_uchar4( data2.z ), d5 = as_uchar4( data2.w );
		const bvhvec3 c0min = bmin + extent * bvhvec3( d0.x, d2.x, d4.x ), c0max = bmin + extent * bvhvec3( d1.x, d3.x, d5.x );
		const bvhvec3 c1min = bmin + extent * bvhvec3( d0.y, d2.y, d4.y ), c1max = bmin + extent * bvhvec3( d1.y, d3.y, d5.y );
		const bvhvec3 c2min = bmin + extent * bvhvec3( d0.z, d2.z, d4.z ), c2max = bmin + extent * bvhvec3( d1.z, d3.z, d5.z );
		const bvhvec3 c3min = bmin + extent * bvhvec3( d0.w, d2.w, d4.w ), c3max = bmin + extent * bvhvec3( d1.w, d3.w, d5.w );
		// intersect child aabbs
		const bvhvec3 t1a = (c0min - ray.O) * ray.rD, t2a = (c0max - ray.O) * ray.rD;
		const bvhvec3 t1b = (c1min - ray.O) * ray.rD, t2b = (c1max - ray.O) * ray.rD;
		const bvhvec3 t1c = (c2min - ray.O) * ray.rD, t2c = (c2max - ray.O) * ray.rD;
		const bvhvec3 t1d = (c3min - ray.O) * ray.rD, t2d = (c3max - ray.O) * ray.rD;
		const bvhvec3 minta = tinybvh_min( t1a, t2a ), maxta = tinybvh_max( t1a, t2a );
		const bvhvec3 mintb = tinybvh_min( t1b, t2b ), maxtb = tinybvh_max( t1b, t2b );
		const bvhvec3 mintc = tinybvh_min( t1c, t2c ), maxtc = tinybvh_max( t1c, t2c );
		const bvhvec3 mintd = tinybvh_min( t1d, t2d ), maxtd = tinybvh_max( t1d, t2d );
		const float tmina = tinybvh_max( tinybvh_max( tinybvh_max( minta.x, minta.y ), minta.z ), 0.0f );
		const float tminb = tinybvh_max( tinybvh_max( tinybvh_max( mintb.x, mintb.y ), mintb.z ), 0.0f );
		const float tminc = tinybvh_max( tinybvh_max( tinybvh_max( mintc.x, mintc.y ), mintc.z ), 0.0f );
		const float tmind = tinybvh_max( tinybvh_max( tinybvh_max( mintd.x, mintd.y ), mintd.z ), 0.0f );
		const float tmaxa = tinybvh_min( tinybvh_min( tinybvh_min( maxta.x, maxta.y ), maxta.z ), ray.hit.t );
		const float tmaxb = tinybvh_min( tinybvh_min( tinybvh_min( maxtb.x, maxtb.y ), maxtb.z ), ray.hit.t );
		const float tmaxc = tinybvh_min( tinybvh_min( tinybvh_min( maxtc.x, maxtc.y ), maxtc.z ), ray.hit.t );
		const float tmaxd = tinybvh_min( tinybvh_min( tinybvh_min( maxtd.x, maxtd.y ), maxtd.z ), ray.hit.t );
		float dist0 = tmina > tmaxa ? BVH_FAR : tmina, dist1 = tminb > tmaxb ? BVH_FAR : tminb;
		float dist2 = tminc > tmaxc ? BVH_FAR : tminc, dist3 = tmind > tmaxd ? BVH_FAR : tmind, tmp;
		// get child node info fields
		uint32_t c0info = as_uint( data3.x ), c1info = as_uint( data3.y );
		uint32_t c2info = as_uint( data3.z ), c3info = as_uint( data3.w );
		if (dist0 < dist2) SWAP( dist0, dist2, c0info, c2info );
		if (dist1 < dist3) SWAP( dist1, dist3, c1info, c3info );
		if (dist0 < dist1) SWAP( dist0, dist1, c0info, c1info );
		if (dist2 < dist3) SWAP( dist2, dist3, c2info, c3info );
		if (dist1 < dist2) SWAP( dist1, dist2, c1info, c2info );
		// process results, starting with farthest child, so nearest ends on top of stack
		uint32_t nextNode = 0;
		uint32_t leaf[4] = { 0, 0, 0, 0 }, leafs = 0;
		if (dist0 < BVH_FAR)
		{
			if (c0info & 0x80000000) leaf[leafs++] = c0info; else if (c0info) stack[stackPtr++] = c0info;
		}
		if (dist1 < BVH_FAR)
		{
			if (c1info & 0x80000000) leaf[leafs++] = c1info; else if (c1info) stack[stackPtr++] = c1info;
		}
		if (dist2 < BVH_FAR)
		{
			if (c2info & 0x80000000) leaf[leafs++] = c2info; else if (c2info) stack[stackPtr++] = c2info;
		}
		if (dist3 < BVH_FAR)
		{
			if (c3info & 0x80000000) leaf[leafs++] = c3info; else if (c3info) stack[stackPtr++] = c3info;
		}
		// process encountered leafs, if any
		for (uint32_t i = 0; i < leafs; i++)
		{
			const uint32_t N = (leaf[i] >> 16) & 0x7fff;
			uint32_t triStart = offset + (leaf[i] & 0xffff);
			for (uint32_t j = 0; j < N; j++, triStart += 3)
			{
				cost += c_int;
				const bvhvec3 e2 = bvhvec3( bvh4Data[triStart + 2] );
				const bvhvec3 e1 = bvhvec3( bvh4Data[triStart + 1] );
				const bvhvec3 v0 = bvh4Data[triStart + 0];
				MOLLER_TRUMBORE_TEST( ray.hit.t, continue );
				ray.hit.t = t, ray.hit.u = u, ray.hit.v = v;
				ray.hit.prim = as_uint( bvh4Data[triStart + 0].w );
			}
		}
		// continue with nearest node or first node on the stack
		if (nextNode) offset = nextNode; else
		{
			if (!stackPtr) break;
			offset = stack[--stackPtr];
		}
	}
	return (int32_t)cost; // cast to not break interface.
}

// BVH4_CPU implementation
// ----------------------------------------------------------------------------

BVH4_CPU::BVH4_CPU( BVH4_CPU&& other )
{
	*this = other;
	other.bvh4Data = 0;
}

BVH4_CPU::~BVH4_CPU()
{
	if (!ownBVH4) bvh4 = MBVH<4>(); // clear out pointers we don't own.
	AlignedFree( bvh4Data );
}

void BVH4_CPU::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH4_CPU::Build( const bvhvec4slice& vertices )
{
	bvh4.bvh.context = bvh4.context = context;
	bvh4.bvh.BuildDefault( vertices );
	ConvertFrom( bvh4 );
}

void BVH4_CPU::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH4_CPU::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh4.bvh.context = bvh4.context = context;
	bvh4.bvh.BuildDefault( vertices, indices, prims );
	ConvertFrom( bvh4 );
}

void BVH4_CPU::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH4_CPU::BuildHQ( const bvhvec4slice& vertices )
{
	bvh4.bvh.context = bvh4.context = context;
	bvh4.bvh.BuildHQ( vertices );
	ConvertFrom( bvh4 );
}

void BVH4_CPU::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH4_CPU::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh4.bvh.context = bvh4.context = context;
	bvh4.bvh.BuildHQ( vertices, indices, prims );
	ConvertFrom( bvh4 );
}

void BVH4_CPU::Save( const char* fileName )
{
	std::fstream s{ fileName, s.binary | s.out };
	uint32_t header = TINY_BVH_VERSION_SUB + (TINY_BVH_VERSION_MINOR << 8) + (TINY_BVH_VERSION_MAJOR << 16) + (layout << 24);
	s.write( (char*)&header, sizeof( uint32_t ) );
	s.write( (char*)&triCount, sizeof( uint32_t ) );
	s.write( (char*)this, sizeof( BVH4_CPU ) );
	s.write( (char*)bvh4Data, usedBlocks * 64 );
}

bool BVH4_CPU::Load( const char* fileName, const uint32_t expectedTris )
{
	// open file and check contents
	std::fstream s{ fileName, s.binary | s.in };
	if (!s) return false;
	BVHContext tmp = context;
	uint32_t header, fileTriCount;
	s.read( (char*)&header, sizeof( uint32_t ) );
	if (((header >> 8) & 255) != TINY_BVH_VERSION_MINOR ||
		((header >> 16) & 255) != TINY_BVH_VERSION_MAJOR ||
		(header & 255) != TINY_BVH_VERSION_SUB || (header >> 24) != layout) return false;
	s.read( (char*)&fileTriCount, sizeof( uint32_t ) );
	if (fileTriCount != expectedTris) return false;
	// all checks passed; safe to overwrite *this
	s.read( (char*)this, sizeof( BVH4_CPU ) );
	context = tmp; // can't load context; function pointers will differ.
	bvh4Data = (CacheLine*)AlignedAlloc( usedBlocks * 64 );
	allocatedBlocks = usedBlocks;
	s.read( (char*)bvh4Data, usedBlocks * 64 );
	bvh4 = MBVH<4>();
	return true;
}

void BVH4_CPU::Optimize( const uint32_t iterations, bool extreme )
{
	bvh4.Optimize( iterations, extreme );
	ConvertFrom( bvh4 );
}

void BVH4_CPU::Refit()
{
	bvh4.Refit();
	ConvertFrom( bvh4 );
}

float BVH4_CPU::SAHCost( const uint32_t nodeIdx ) const
{
	return bvh4.SAHCost( nodeIdx );
}

#define SORT(a,b) { if (dist[a] < dist[b]) { float h = dist[a]; dist[a] = dist[b], dist[b] = h; } }

void BVH4_CPU::ConvertFrom( MBVH<4>& original )
{
	// Note: identical to BVH8_CPU version, just with fewer lanes.
	// get a copy of the input bvh4
	if (&original != &bvh4) ownBVH4 = false; // bvh isn't ours; don't delete in destructor.
	bvh4 = original;
	// prepare input bvh4
	uint32_t firstIdx = 0;
	bvh4.bvh.CombineLeafs( 4, firstIdx, 0 );
	bvh4.bvh.SplitLeafs( 4 );
	bvh4.ConvertFrom( bvh4.bvh, true );
	// allocate if needed
	uint32_t nodesNeeded = bvh4.usedNodes, leafsNeeded = bvh4.LeafCount();
	uint32_t blocksNeeded = nodesNeeded * (sizeof( BVHNode ) / 64); // here, block = cacheline.
	blocksNeeded += leafsNeeded * (sizeof( BVHTri4Leaf ) / 64);
	if (allocatedBlocks < blocksNeeded)
	{
		AlignedFree( bvh4Data );
		void* (*allocator)(size_t, void*) = malloc64;
		bvh4Data = (CacheLine*)allocator( blocksNeeded * 64, 0 );
		allocatedBlocks = blocksNeeded;
	}
	CopyBasePropertiesFrom( bvh4 );
	// start conversion
	uint32_t newBlockPtr = 0, nodeIdx = 0, stack[256], stackPtr = 0;
	while (1)
	{
		const MBVH<4>::MBVHNode& orig = bvh4.mbvhNode[nodeIdx];
		BVHNode* newNode = (BVHNode*)(bvh4Data + newBlockPtr);
		newBlockPtr += sizeof( BVHNode ) / 64;
		memset( newNode, 0, sizeof( BVHNode ) );
		// calculate the permutation offsets for the node
		for (uint32_t q = 0; q < 8; q++)
		{
			const bvhvec3 D( q & 1 ? 1.0f : -1.0f, q & 2 ? 1.0f : -1.0f, q & 4 ? 1.0f : -1.0f );
			union { float dist[4]; uint32_t idist[4]; };
			for (int i = 0; i < 4; i++) if (orig.child[i] == 0)
				dist[i] = 1e30f, idist[i] = (idist[i] & 0xfffffffc) + i;
			else
			{
				const MBVH<4>::MBVHNode& c = bvh4.mbvhNode[orig.child[i]];
				const bvhvec3 p( q & 1 ? c.aabbMin.x : c.aabbMax.x, q & 2 ? c.aabbMin.y : c.aabbMax.y, q & 4 ? c.aabbMin.z : c.aabbMax.z );
				dist[i] = tinybvh_dot( D, p ), idist[i] = (idist[i] & 0xfffffff8) + i;
			}
			// apply sorting network - https://bertdobbelaere.github.io/sorting_networks.html#N4L5D3
			SORT( 0, 2 ); SORT( 1, 3 ); SORT( 0, 1 ); SORT( 2, 3 ); SORT( 1, 2 );
			for (int i = 0; i < 4; i++) ((uint32_t*)&newNode->perm4)[i] += (idist[i] & 3) << (q * 2);
		}
		// fill remaining fields
		int32_t cidx = 0;
		for (int32_t i = 0; i < 4; i++) if (orig.child[i])
		{
			const MBVH<4>::MBVHNode& child = bvh4.mbvhNode[orig.child[i]];
			((float*)&newNode->xmin4)[cidx] = child.aabbMin.x, ((float*)&newNode->xmax4)[cidx] = child.aabbMax.x;
			((float*)&newNode->ymin4)[cidx] = child.aabbMin.y, ((float*)&newNode->ymax4)[cidx] = child.aabbMax.y;
			((float*)&newNode->zmin4)[cidx] = child.aabbMin.z, ((float*)&newNode->zmax4)[cidx] = child.aabbMax.z;
			if (child.isLeaf())
			{
				// emit leaf node: group of up to 4 triangles in AoS format.
				((uint32_t*)&newNode->child4)[cidx] = newBlockPtr + LEAF_BIT;
				BVHTri4Leaf* leaf = (BVHTri4Leaf*)(bvh4Data + newBlockPtr);
				newBlockPtr += sizeof( BVHTri4Leaf ) / 64;
				for (uint32_t i0, i1, i2, l = 0; l < 4; l++)
				{
					uint32_t primIdx = bvh4.bvh.primIdx[child.firstTri + tinybvh_min( l, child.triCount - 1u )];
					GET_PRIM_INDICES_I0_I1_I2( bvh4.bvh, primIdx );
					const bvhvec4 v0 = bvh4.bvh.verts[i0], e1 = bvh4.bvh.verts[i1] - v0, e2 = bvh4.bvh.verts[i2] - v0;
					leaf->SetData( v0, e1, e2, primIdx, l );
				}
			}
			else
			{
				uint32_t* slot = (uint32_t*)&newNode->child4 + cidx;
				stack[stackPtr++] = (uint32_t)(slot - (uint32_t*)bvh4Data);
				stack[stackPtr++] = orig.child[i];
			}
			cidx++;
		}
		for (; cidx < 4; cidx++)
			((float*)&newNode->xmin4)[cidx] = 1e30f, ((float*)&newNode->xmax4)[cidx] = 1.00001e30f,
			((float*)&newNode->ymin4)[cidx] = 1e30f, ((float*)&newNode->ymax4)[cidx] = 1.00001e30f,
			((float*)&newNode->zmin4)[cidx] = 1e30f, ((float*)&newNode->zmax4)[cidx] = 1.00001e30f,
			((uint32_t*)&newNode->child4)[cidx] |= EMPTY_BIT;
		// pop next task
		if (!stackPtr) break;
		nodeIdx = stack[--stackPtr];
		const uint32_t offset = stack[--stackPtr];
		((uint32_t*)bvh4Data)[offset] = newBlockPtr;
	}
	usedBlocks = newBlockPtr;
}

// BVH8_CPU implementation
// ----------------------------------------------------------------------------

BVH8_CPU::BVH8_CPU( BVH8_CPU&& other )
{
	*this = other;
	other.bvh8Data = 0;
}

BVH8_CPU::~BVH8_CPU()
{
	if (!ownBVH8) bvh8 = MBVH<8>(); // clear out pointers we don't own.
	AlignedFree( bvh8Data );
}

void BVH8_CPU::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH8_CPU::Build( const bvhvec4slice& vertices )
{
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildDefault( vertices );
	bvh8.bvh.Compact();
	ConvertFrom( bvh8 );
}

void BVH8_CPU::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH8_CPU::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildDefault( vertices, indices, prims );
	bvh8.bvh.Compact();
	ConvertFrom( bvh8 );
}

void BVH8_CPU::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH8_CPU::BuildHQ( const bvhvec4slice& vertices )
{
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildHQ( vertices );
	ConvertFrom( bvh8 );
}

void BVH8_CPU::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH8_CPU::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildHQ( vertices, indices, prims );
	ConvertFrom( bvh8 );
}

void BVH8_CPU::Save( const char* fileName )
{
	std::fstream s{ fileName, s.binary | s.out };
	uint32_t header = TINY_BVH_VERSION_SUB + (TINY_BVH_VERSION_MINOR << 8) + (TINY_BVH_VERSION_MAJOR << 16) + (layout << 24);
	s.write( (char*)&header, sizeof( uint32_t ) );
	s.write( (char*)&triCount, sizeof( uint32_t ) );
	s.write( (char*)this, sizeof( BVH8_CPU ) );
	s.write( (char*)bvh8Data, usedBlocks * 64 );
}

bool BVH8_CPU::Load( const char* fileName, const uint32_t expectedTris )
{
	// open file and check contents
	std::fstream s{ fileName, s.binary | s.in };
	if (!s) return false;
	BVHContext tmp = context;
	uint32_t header, fileTriCount;
	s.read( (char*)&header, sizeof( uint32_t ) );
	if (((header >> 8) & 255) != TINY_BVH_VERSION_MINOR ||
		((header >> 16) & 255) != TINY_BVH_VERSION_MAJOR ||
		(header & 255) != TINY_BVH_VERSION_SUB || (header >> 24) != layout) return false;
	s.read( (char*)&fileTriCount, sizeof( uint32_t ) );
	if (fileTriCount != expectedTris) return false;
	// all checks passed; safe to overwrite *this
	s.read( (char*)this, sizeof( BVH8_CPU ) );
	context = tmp; // can't load context; function pointers will differ.
	bvh8Data = (CacheLine*)AlignedAlloc( usedBlocks * 64 );
	allocatedBlocks = usedBlocks;
	s.read( (char*)bvh8Data, usedBlocks * 64 );
	bvh8 = MBVH<8>();
	return true;
}

void BVH8_CPU::Optimize( const uint32_t iterations, bool extreme )
{
	bvh8.Optimize( iterations, extreme );
	ConvertFrom( bvh8 );
}

void BVH8_CPU::Refit()
{
	bvh8.Refit();
	ConvertFrom( bvh8 );
}

float BVH8_CPU::SAHCost( const uint32_t nodeIdx ) const
{
	return bvh8.SAHCost( nodeIdx );
}

void BVH8_CPU::ConvertFrom( MBVH<8>& original )
{
	// get a copy of the input bvh8
	if (&original != &bvh8) ownBVH8 = false; // bvh isn't ours; don't delete in destructor.
	bvh8 = original;
	// prepare input bvh8
	uint32_t firstIdx = 0;
	bvh8.bvh.CombineLeafs( 4, firstIdx, 0 );
	bvh8.bvh.SplitLeafs( 4 );
	bvh8.ConvertFrom( bvh8.bvh, true );
	// allocate if needed
	uint32_t nodesNeeded = bvh8.usedNodes, leafsNeeded = bvh8.LeafCount();
	uint32_t blocksNeeded = nodesNeeded * (sizeof( BVHNode ) / 64); // here, block = cacheline.
	blocksNeeded += leafsNeeded * (sizeof( BVHTri4Leaf ) / 64);
	if (allocatedBlocks < blocksNeeded)
	{
		AlignedFree( bvh8Data );
		void* (*allocator)(size_t, void*) = malloc64;
		bvh8Data = (CacheLine*)allocator( blocksNeeded * 64, 0 );
		allocatedBlocks = blocksNeeded;
	}
	CopyBasePropertiesFrom( bvh8 );
	// start conversion
	uint32_t newBlockPtr = 0, nodeIdx = 0, stack[256], stackPtr = 0;
	while (1)
	{
		const MBVH<8>::MBVHNode& orig = bvh8.mbvhNode[nodeIdx];
		BVHNode* newNode = (BVHNode*)(bvh8Data + newBlockPtr);
		newBlockPtr += sizeof( BVHNode ) / 64;
		memset( newNode, 0, sizeof( BVHNode ) );
		// calculate the permutation offsets for the node
		for (uint32_t q = 0; q < 8; q++)
		{
			const bvhvec3 D( q & 1 ? 1.0f : -1.0f, q & 2 ? 1.0f : -1.0f, q & 4 ? 1.0f : -1.0f );
			union { float dist[8]; uint32_t idist[8]; };
			for (int i = 0; i < 8; i++) if (orig.child[i] == 0)
				dist[i] = 1e30f, idist[i] = (idist[i] & 0xfffffff8) + i;
			else
			{
				const MBVH<8>::MBVHNode& c = bvh8.mbvhNode[orig.child[i]];
				const bvhvec3 p( q & 1 ? c.aabbMin.x : c.aabbMax.x, q & 2 ? c.aabbMin.y : c.aabbMax.y, q & 4 ? c.aabbMin.z : c.aabbMax.z );
				dist[i] = tinybvh_dot( D, p ), idist[i] = (idist[i] & 0xfffffff8) + i;
			}
			// apply sorting network - https://bertdobbelaere.github.io/sorting_networks.html#N8L19D6
			SORT( 0, 2 ); SORT( 1, 3 ); SORT( 4, 6 ); SORT( 5, 7 ); SORT( 0, 4 );
			SORT( 1, 5 ); SORT( 2, 6 ); SORT( 3, 7 ); SORT( 0, 1 ); SORT( 2, 3 );
			SORT( 4, 5 ); SORT( 6, 7 ); SORT( 2, 4 ); SORT( 3, 5 ); SORT( 1, 4 );
			SORT( 3, 6 ); SORT( 1, 2 ); SORT( 3, 4 ); SORT( 5, 6 );
			for (int i = 0; i < 8; i++) ((uint32_t*)&newNode->perm8)[i] += (idist[i] & 7) << (q * 3);
		}
		// fill remaining fields
		int32_t cidx = 0;
		for (int32_t i = 0; i < 8; i++) if (orig.child[i])
		{
			const MBVH<8>::MBVHNode& child = bvh8.mbvhNode[orig.child[i]];
			((float*)&newNode->xmin8)[cidx] = child.aabbMin.x, ((float*)&newNode->xmax8)[cidx] = child.aabbMax.x;
			((float*)&newNode->ymin8)[cidx] = child.aabbMin.y, ((float*)&newNode->ymax8)[cidx] = child.aabbMax.y;
			((float*)&newNode->zmin8)[cidx] = child.aabbMin.z, ((float*)&newNode->zmax8)[cidx] = child.aabbMax.z;
			if (child.isLeaf())
			{
				// emit leaf node: group of up to 4 triangles in AoS format.
				((uint32_t*)&newNode->child8)[cidx] = newBlockPtr + LEAF_BIT;
				BVHTri4Leaf* leaf = (BVHTri4Leaf*)(bvh8Data + newBlockPtr);
				newBlockPtr += sizeof( BVHTri4Leaf ) / 64;
				for (uint32_t i0, i1, i2, l = 0; l < 4; l++)
				{
					uint32_t primIdx = bvh8.bvh.primIdx[child.firstTri + tinybvh_min( l, child.triCount - 1u )];
					GET_PRIM_INDICES_I0_I1_I2( bvh8.bvh, primIdx );
					const bvhvec4 v0 = bvh8.bvh.verts[i0], e1 = bvh8.bvh.verts[i1] - v0, e2 = bvh8.bvh.verts[i2] - v0;
					leaf->SetData( v0, e1, e2, primIdx, l );
				}
			}
			else
			{
				uint32_t* slot = (uint32_t*)&newNode->child8 + cidx;
				stack[stackPtr++] = (uint32_t)(slot - (uint32_t*)bvh8Data);
				stack[stackPtr++] = orig.child[i];
			}
			cidx++;
		}
		for (; cidx < 8; cidx++)
		{
			((float*)&newNode->xmin8)[cidx] = 1e30f, ((float*)&newNode->xmax8)[cidx] = 1.00001e30f;
			((float*)&newNode->ymin8)[cidx] = 1e30f, ((float*)&newNode->ymax8)[cidx] = 1.00001e30f;
			((float*)&newNode->zmin8)[cidx] = 1e30f, ((float*)&newNode->zmax8)[cidx] = 1.00001e30f;
			((uint32_t*)&newNode->child8)[cidx] |= EMPTY_BIT;
		}
		// pop next task
		if (!stackPtr) break;
		nodeIdx = stack[--stackPtr];
		const uint32_t offset = stack[--stackPtr];
		((uint32_t*)bvh8Data)[offset] = newBlockPtr;
	}
	usedBlocks = newBlockPtr;
}

// BVH8_CWBVH implementation
// ----------------------------------------------------------------------------

BVH8_CWBVH::BVH8_CWBVH( BVH8_CWBVH&& other )
{
	*this = other;
	other.bvh8Data = 0;
	other.bvh8Tris = 0;
}

BVH8_CWBVH::~BVH8_CWBVH()
{
	if (!ownBVH8) bvh8 = MBVH<8>(); // clear out pointers we don't own.
	AlignedFree( bvh8Data );
	AlignedFree( bvh8Tris );
}

void BVH8_CWBVH::Optimize( const uint32_t iterations, bool extreme )
{
	bvh8.Optimize( iterations, extreme );
	ConvertFrom( bvh8, true );
}

float BVH8_CWBVH::SAHCost( const uint32_t nodeIdx ) const
{
	return bvh8.SAHCost( nodeIdx );
}

void BVH8_CWBVH::Save( const char* fileName )
{
	std::fstream s{ fileName, s.binary | s.out };
	uint32_t header = TINY_BVH_VERSION_SUB + (TINY_BVH_VERSION_MINOR << 8) + (TINY_BVH_VERSION_MAJOR << 16) + (layout << 24);
	s.write( (char*)&header, sizeof( uint32_t ) );
	s.write( (char*)&triCount, sizeof( uint32_t ) );
	s.write( (char*)this, sizeof( BVH8_CWBVH ) );
	s.write( (char*)bvh8Data, usedBlocks * 16 );
	s.write( (char*)bvh8Tris, bvh8.idxCount * 4 * 16 );
}

bool BVH8_CWBVH::Load( const char* fileName, const uint32_t expectedTris )
{
	// open file and check contents
	std::fstream s{ fileName, s.binary | s.in };
	if (!s) return false;
	BVHContext tmp = context;
	uint32_t header, fileTriCount;
	s.read( (char*)&header, sizeof( uint32_t ) );
	if (((header >> 8) & 255) != TINY_BVH_VERSION_MINOR ||
		((header >> 16) & 255) != TINY_BVH_VERSION_MAJOR ||
		(header & 255) != TINY_BVH_VERSION_SUB || (header >> 24) != layout) return false;
	s.read( (char*)&fileTriCount, sizeof( uint32_t ) );
	if (fileTriCount != expectedTris) return false;
	// all checks passed; safe to overwrite *this
	s.read( (char*)this, sizeof( BVH8_CWBVH ) );
	context = tmp; // can't load context; function pointers will differ.
	bvh8Data = (bvhvec4*)AlignedAlloc( usedBlocks * 16 );
	bvh8Tris = (bvhvec4*)AlignedAlloc( bvh8.idxCount * 4 * 16 );
	allocatedBlocks = usedBlocks;
	s.read( (char*)bvh8Data, usedBlocks * 16 );
	s.read( (char*)bvh8Tris, bvh8.idxCount * 4 * 16 );
	bvh8 = MBVH<8>();
	return true;
}

void BVH8_CWBVH::Build( const bvhvec4* vertices, const uint32_t primCount )
{
	Build( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH8_CWBVH::Build( const bvhvec4slice& vertices )
{
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildDefault( vertices );
	bvh8.bvh.Compact();
	bvh8.bvh.SplitLeafs( 3 );
	bvh8.ConvertFrom( bvh8.bvh, false );
	ConvertFrom( bvh8, true );
}

void BVH8_CWBVH::Build( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	// build the BVH with a continuous array of bvhvec4 vertices, indexed by 'indices'.
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH8_CWBVH::Build( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	// build the BVH from vertices stored in a slice, indexed by 'indices'.
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildDefault( vertices, indices, prims );
	bvh8.bvh.Compact();
	bvh8.bvh.SplitLeafs( 3 );
	bvh8.ConvertFrom( bvh8.bvh, true );
	ConvertFrom( bvh8, true );
}

void BVH8_CWBVH::BuildHQ( const bvhvec4* vertices, const uint32_t primCount )
{
	BuildHQ( bvhvec4slice( vertices, primCount * 3, sizeof( bvhvec4 ) ) );
}

void BVH8_CWBVH::BuildHQ( const bvhvec4slice& vertices )
{
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildHQ( vertices );
	bvh8.bvh.SplitLeafs( 3 );
	bvh8.ConvertFrom( bvh8.bvh, true );
	ConvertFrom( bvh8, true );
}

void BVH8_CWBVH::BuildHQ( const bvhvec4* vertices, const uint32_t* indices, const uint32_t prims )
{
	Build( bvhvec4slice{ vertices, prims * 3, sizeof( bvhvec4 ) }, indices, prims );
}

void BVH8_CWBVH::BuildHQ( const bvhvec4slice& vertices, const uint32_t* indices, uint32_t prims )
{
	bvh8.bvh.context = bvh8.context = context;
	bvh8.bvh.BuildHQ( vertices, indices, prims );
	bvh8.bvh.SplitLeafs( 3 );
	bvh8.ConvertFrom( bvh8.bvh, true );
	ConvertFrom( bvh8, true );
}

// Convert a BVH8 to the format specified in: "Efficient Incoherent Ray Traversal on GPUs Through
// Compressed Wide BVHs", Ylitie et al. 2017. Adapted from code by "AlanWBFT".
void BVH8_CWBVH::ConvertFrom( MBVH<8>& original, bool )
{
	// get a copy of the original bvh8
	if (&original != &bvh8) ownBVH8 = false; // bvh isn't ours; don't delete in destructor.
	bvh8 = original;
	BVH_FATAL_ERROR_IF( bvh8.mbvhNode[0].isLeaf(), "BVH8_CWBVH::ConvertFrom( .. ), converting a single-node bvh." );
	// allocate memory
	uint32_t spaceNeeded = bvh8.triCount * 5; // CWBVH nodes use 80 bytes each.
	if (spaceNeeded > allocatedBlocks)
	{
		bvh8Data = (bvhvec4*)AlignedAlloc( spaceNeeded * 16 );
		bvh8Tris = (bvhvec4*)AlignedAlloc( bvh8.idxCount * 4 * 16 );
		allocatedBlocks = spaceNeeded;
	}
	memset( bvh8Data, 0, spaceNeeded * 16 );
	memset( bvh8Tris, 0, bvh8.idxCount * 3 * 16 );
	CopyBasePropertiesFrom( bvh8 );
	MBVH<8>::MBVHNode* stackNodePtr[256];
	uint32_t stackNodeAddr[256], stackPtr = 1, nodeDataPtr = 5, triDataPtr = 0;
	stackNodePtr[0] = &bvh8.mbvhNode[0], stackNodeAddr[0] = 0;
	// start conversion
	while (stackPtr > 0)
	{
		MBVH<8>::MBVHNode* orig = stackNodePtr[--stackPtr];
		const int32_t currentNodeAddr = stackNodeAddr[stackPtr];
		bvhvec3 nodeLo = orig->aabbMin, nodeHi = orig->aabbMax;
		// greedy child node ordering
		const bvhvec3 nodeCentroid = (nodeLo + nodeHi) * 0.5f;
		float cost[8][8];
		int32_t assignment[8];
		bool isSlotEmpty[8];
		for (int32_t s = 0; s < 8; s++)
		{
			isSlotEmpty[s] = true, assignment[s] = -1;
			bvhvec3 ds(
				(((s >> 2) & 1) == 1) ? -1.0f : 1.0f,
				(((s >> 1) & 1) == 1) ? -1.0f : 1.0f,
				(((s >> 0) & 1) == 1) ? -1.0f : 1.0f
			);
			for (int32_t i = 0; i < 8; i++) if (orig->child[i] == 0) cost[s][i] = BVH_FAR; else
			{
				MBVH<8>::MBVHNode* const child = &bvh8.mbvhNode[orig->child[i]];
				bvhvec3 childCentroid = (child->aabbMin + child->aabbMax) * 0.5f;
				cost[s][i] = tinybvh_dot( childCentroid - nodeCentroid, ds );
			}
		}
		while (1)
		{
			float minCost = BVH_FAR;
			int32_t minEntryx = -1, minEntryy = -1;
			for (int32_t s = 0; s < 8; s++) for (int32_t i = 0; i < 8; i++)
				if (assignment[i] == -1 && isSlotEmpty[s] && cost[s][i] < minCost)
					minCost = cost[s][i], minEntryx = s, minEntryy = i;
			if (minEntryx == -1 && minEntryy == -1) break;
			isSlotEmpty[minEntryx] = false, assignment[minEntryy] = minEntryx;
		}
		for (int32_t i = 0; i < 8; i++) if (assignment[i] == -1) for (int32_t s = 0; s < 8; s++) if (isSlotEmpty[s])
		{
			isSlotEmpty[s] = false, assignment[i] = s;
			break;
		}
		const MBVH<8>::MBVHNode oldNode = *orig;
		for (int32_t i = 0; i < 8; i++) orig->child[assignment[i]] = oldNode.child[i];
		// calculate quantization parameters for each axis
		const int32_t ex = (int32_t)((int8_t)ceilf( log2f( (nodeHi.x - nodeLo.x) / 255.0f ) ));
		const int32_t ey = (int32_t)((int8_t)ceilf( log2f( (nodeHi.y - nodeLo.y) / 255.0f ) ));
		const int32_t ez = (int32_t)((int8_t)ceilf( log2f( (nodeHi.z - nodeLo.z) / 255.0f ) ));
		// encode output
		int32_t internalChildCount = 0, leafChildTriCount = 0, childBaseIndex = 0, triangleBaseIndex = 0;
		uint8_t imask = 0;
		for (int32_t i = 0; i < 8; i++)
		{
			if (orig->child[i] == 0) continue;
			MBVH<8>::MBVHNode* const child = &bvh8.mbvhNode[orig->child[i]];
			const int32_t qlox = (int32_t)floorf( (child->aabbMin.x - nodeLo.x) / powf( 2, (float)ex ) );
			const int32_t qloy = (int32_t)floorf( (child->aabbMin.y - nodeLo.y) / powf( 2, (float)ey ) );
			const int32_t qloz = (int32_t)floorf( (child->aabbMin.z - nodeLo.z) / powf( 2, (float)ez ) );
			const int32_t qhix = (int32_t)ceilf( (child->aabbMax.x - nodeLo.x) / powf( 2, (float)ex ) );
			const int32_t qhiy = (int32_t)ceilf( (child->aabbMax.y - nodeLo.y) / powf( 2, (float)ey ) );
			const int32_t qhiz = (int32_t)ceilf( (child->aabbMax.z - nodeLo.z) / powf( 2, (float)ez ) );
			uint8_t* const baseAddr = (uint8_t*)&bvh8Data[currentNodeAddr + 2];
			baseAddr[i + 0] = (uint8_t)qlox, baseAddr[i + 24] = (uint8_t)qhix;
			baseAddr[i + 8] = (uint8_t)qloy, baseAddr[i + 32] = (uint8_t)qhiy;
			baseAddr[i + 16] = (uint8_t)qloz, baseAddr[i + 40] = (uint8_t)qhiz;
			if (!child->isLeaf())
			{
				// interior node, set params and push onto stack
				const int32_t childNodeAddr = nodeDataPtr;
				if (internalChildCount++ == 0) childBaseIndex = childNodeAddr / 5;
				nodeDataPtr += 5, imask |= 1 << i;
				// set the meta field - This calculation assumes children are stored contiguously.
				uint8_t* const childMetaField = ((uint8_t*)&bvh8Data[currentNodeAddr + 1]) + 8;
				childMetaField[i] = (1 << 5) | (24 + (uint8_t)i); // I don't see how this accounts for empty children?
				stackNodePtr[stackPtr] = child, stackNodeAddr[stackPtr++] = childNodeAddr; // counted in float4s
				continue;
			}
			// leaf node
			const uint32_t tcount = child->triCount; // will not exceed 3.
			if (leafChildTriCount == 0) triangleBaseIndex = triDataPtr;
			int32_t unaryEncodedTriCount = tcount == 1 ? 0b001 : tcount == 2 ? 0b011 : 0b111;
			// set the meta field - This calculation assumes children are stored contiguously.
			uint8_t* const childMetaField = ((uint8_t*)&bvh8Data[currentNodeAddr + 1]) + 8;
			childMetaField[i] = (uint8_t)((unaryEncodedTriCount << 5) | leafChildTriCount);
			leafChildTriCount += tcount;
			for (uint32_t j = 0; j < tcount; j++)
			{
				int32_t triIdx = bvh8.bvh.primIdx[child->firstTri + j];
				uint32_t ti0, ti1, ti2;
				if (bvh8.bvh.vertIdx)
					ti0 = bvh8.bvh.vertIdx[triIdx * 3],
					ti1 = bvh8.bvh.vertIdx[triIdx * 3 + 1],
					ti2 = bvh8.bvh.vertIdx[triIdx * 3 + 2];
				else
					ti0 = triIdx * 3, ti1 = triIdx * 3 + 1, ti2 = triIdx * 3 + 2;
			#ifdef CWBVH_COMPRESSED_TRIS
				PrecomputeTriangle( verts, ti0, ti1, ti2, (float*)&bvh8Tris[triDataPtr] );
				bvh8Tris[triDataPtr + 3] = bvhvec4( 0, 0, 0, *(float*)&triIdx );
				triDataPtr += 4;
			#else
				bvhvec4 t = bvh8.bvh.verts[ti0];
				bvh8Tris[triDataPtr + 0] = bvh8.bvh.verts[ti2] - t;
				bvh8Tris[triDataPtr + 1] = bvh8.bvh.verts[ti1] - t;
				t.w = *(float*)&triIdx;
				bvh8Tris[triDataPtr + 2] = t, triDataPtr += 3;
			#endif
			}
		}
		uint8_t exyzAndimask[4] = { *(uint8_t*)&ex, *(uint8_t*)&ey, *(uint8_t*)&ez, imask };
		bvh8Data[currentNodeAddr + 0] = bvhvec4( nodeLo, *(float*)&exyzAndimask );
		bvh8Data[currentNodeAddr + 1].x = *(float*)&childBaseIndex;
		bvh8Data[currentNodeAddr + 1].y = *(float*)&triangleBaseIndex;
	}
	usedBlocks = nodeDataPtr;
}

// ============================================================================
//
//        I M P L E M E N T A T I O N  -  A V X / S S E  C O D E
//
// ============================================================================

#ifdef BVH_USESSE

inline __m128 fastrcp4( const __m128 a )
{
	__m128 res = _mm_rcp_ps( a );
	__m128 muls = _mm_mul_ps( a, _mm_mul_ps( res, res ) );
	return _mm_sub_ps( _mm_add_ps( res, res ), muls );
}

static uint32_t __popc( uint32_t x )
{
#if defined _MSC_VER && !defined __clang__
	return __popcnt( x );
#elif defined __GNUC__ || defined __clang__
	return __builtin_popcount( x );
#endif
}

static const unsigned __A = 0x03020100, __B = 0x07060504, __C = 0x0B0A0908, __D = 0x0F0E0D0C;
ALIGNED( 64 ) static __m128i idxLUT4_[16] = {
	_mm_set_epi32( 0, 0, 0, 0 ),		// 0000
	_mm_set_epi32( 0, 0, 0, __A ),		// 0001
	_mm_set_epi32( 0, 0, 0, __B ),		// 0010
	_mm_set_epi32( 0, 0, __B, __A ),	// 0011
	_mm_set_epi32( 0, 0, 0, __C ),		// 0100
	_mm_set_epi32( 0, 0, __C, __A ),	// 0101
	_mm_set_epi32( 0, 0, __C, __B ),	// 0110
	_mm_set_epi32( 0, __C, __B, __A ),	// 0111
	_mm_set_epi32( 0, 0, 0, __D ),		// 1000
	_mm_set_epi32( 0, 0, __D, __A ),	// 1001
	_mm_set_epi32( 0, 0, __D, __B ),	// 1010
	_mm_set_epi32( 0, __D, __B, __A ),	// 1011
	_mm_set_epi32( 0, 0, __D, __C ),	// 1100
	_mm_set_epi32( 0, __D, __C, __A ),	// 1101
	_mm_set_epi32( 0, __D, __C, __B ),	// 1110
	_mm_set_epi32( __D, __C, __B, __A ) // 1111
};

int32_t BVH4_CPU::Intersect( Ray& ray ) const
{
	VALIDATE_RAY( ray );
	const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
	if (!posX) goto negx;
	if (posY) { if (posZ) return Intersect<true, true, true>( ray ); else return Intersect<true, true, false>( ray ); }
	if (posZ) return Intersect<true, false, true>( ray ); else return Intersect<true, false, false>( ray );
negx:
	if (posY) { if (posZ) return Intersect<false, true, true>( ray ); else return Intersect<false, true, false>( ray ); }
	if (posZ) return Intersect<false, false, true>( ray ); else return Intersect<false, false, false>( ray );
}

template <bool posX, bool posY, bool posZ> int32_t BVH4_CPU::Intersect( Ray& ray ) const
{
	ALIGNED( 64 ) int32_t nodeStack[256];
	ALIGNED( 64 ) float distStack[256];
	const __m128 zero4 = _mm_setzero_ps();
	__m128 t4 = _mm_set1_ps( ray.hit.t );
	ALIGNED( 64 ) int32_t stackPtr = 0, nodeIdx = 0;
	union ALIGNED( 32 ) { __m256i c8s; uint32_t cs[8]; };
	constexpr int signShift = (posX ? 2 : 0) + (posY ? 4 : 0) + (posZ ? 8 : 0);
	const __m128 rx4 = _mm_set1_ps( ray.O.x * ray.rD.x ), rdx4 = _mm_set1_ps( ray.rD.x );
	const __m128 ry4 = _mm_set1_ps( ray.O.y * ray.rD.y ), rdy4 = _mm_set1_ps( ray.rD.y );
	const __m128 rz4 = _mm_set1_ps( ray.O.z * ray.rD.z ), rdz4 = _mm_set1_ps( ray.rD.z );
	const __m128 ox4 = _mm_set1_ps( ray.O.x ), oy4 = _mm_set1_ps( ray.O.y ), oz4 = _mm_set1_ps( ray.O.z );
	const __m128 dx4 = _mm_set1_ps( ray.D.x ), dy4 = _mm_set1_ps( ray.D.y ), dz4 = _mm_set1_ps( ray.D.z );
	const __m128 one4 = _mm_set1_ps( 1 ), inf4 = _mm_set1_ps( 1e34f );
#ifndef __AVX__
	const __m128i shftmsk4 = _mm_set1_epi32( 3 ), mul4 = _mm_set1_epi32( 0x04040404 ), add4 = _mm_set1_epi32( 0x03020100 );
#endif
#ifdef _DEBUG
	// sorry, not even this can be tolerated in this function. Only in debug.
	uint32_t steps = 0;
#endif
	while (1)
	{
	#ifdef _DEBUG
		steps++;
	#endif
		while (!(nodeIdx & LEAF_BIT))
		{
			const BVHNode* n = (BVHNode*)(bvh4Data + nodeIdx);
			const __m128 tx1 = _mm_sub_ps( _mm_mul_ps( posX ? n->xmin4 : n->xmax4, rdx4 ), rx4 );
			const __m128 ty1 = _mm_sub_ps( _mm_mul_ps( posY ? n->ymin4 : n->ymax4, rdy4 ), ry4 );
			const __m128 tz1 = _mm_sub_ps( _mm_mul_ps( posZ ? n->zmin4 : n->zmax4, rdz4 ), rz4 );
			const __m128 tx2 = _mm_sub_ps( _mm_mul_ps( posX ? n->xmax4 : n->xmin4, rdx4 ), rx4 );
			const __m128 ty2 = _mm_sub_ps( _mm_mul_ps( posY ? n->ymax4 : n->ymin4, rdy4 ), ry4 );
			const __m128 tz2 = _mm_sub_ps( _mm_mul_ps( posZ ? n->zmax4 : n->zmin4, rdz4 ), rz4 );
			__m128 tmin = _mm_max_ps( _mm_max_ps( _mm_max_ps( zero4, tx1 ), ty1 ), tz1 );
			const __m128 tmax = _mm_min_ps( _mm_min_ps( _mm_min_ps( tx2, t4 ), ty2 ), tz2 );
			const __m128 mask4 = _mm_cmple_ps( tmin, tmax );
			const uint32_t mask = _mm_movemask_ps( mask4 );
			const uint32_t validNodes = __popc( mask );
			if (validNodes == 1)
			{
				const uint32_t lane = __bfind( mask );
				nodeIdx = ((uint32_t*)&n->child4)[lane];
			}
			else if (validNodes)
			{
			#ifdef __AVX__
				// avx1 path
				const __m128i index = _mm_srli_epi32( n->perm4, signShift );
				const uint32_t m = _mm_movemask_ps( _mm_permutevar_ps( mask4, index ) );
				tmin = _mm_permutevar_ps( tmin, index );
				const __m128i c4 = _mm_castps_si128( _mm_permutevar_ps( _mm_castsi128_ps( n->child4 ), index ) );
			#else
				// sse4.2 path, 3 extra ops to emulate _mm_permutevar_ps via _mm_shuffle_epi8
				const __m128i raw4 = _mm_and_si128( _mm_srli_epi32( n->perm4, signShift ), shftmsk4 );
				const __m128i shfl16 = _mm_add_epi32( _mm_mullo_epi32( raw4, mul4 ), add4 );
				const uint32_t m = _mm_movemask_ps( _mm_castsi128_ps( _mm_shuffle_epi8( _mm_castps_si128( mask4 ), shfl16 ) ) );
				tmin = _mm_castsi128_ps( _mm_shuffle_epi8( _mm_castps_si128( tmin ), shfl16 ) );
				const __m128i c4 = _mm_shuffle_epi8( n->child4, shfl16 );
			#endif
				const __m128i cpi = idxLUT4_[m];
				const __m128 dist4 = _mm_castsi128_ps( _mm_shuffle_epi8( _mm_castps_si128( tmin ), cpi ) );
				const __m128i child4 = _mm_shuffle_epi8( c4, cpi );
				_mm_storeu_si128( (__m128i*)(nodeStack + stackPtr), child4 );
				_mm_storeu_ps( (float*)(distStack + stackPtr), dist4 );
				stackPtr += validNodes - 1;
				nodeIdx = nodeStack[stackPtr];
			}
			else
			{
				if (!stackPtr) goto the_end;
				nodeIdx = nodeStack[--stackPtr];
			}
		}
		// Moeller-Trumbore ray/triangle intersection algorithm for four triangles
		uint32_t n;
		memcpy( &n, &nodeIdx, 4 );
		const BVHTri4Leaf* leaf = (BVHTri4Leaf*)(bvh4Data + (n & 0x1fffffff));
		const __m128 hx4 = _mm_sub_ps( _mm_mul_ps( dy4, leaf->e2z4 ), _mm_mul_ps( dz4, leaf->e2y4 ) );
		const __m128 hy4 = _mm_sub_ps( _mm_mul_ps( dz4, leaf->e2x4 ), _mm_mul_ps( dx4, leaf->e2z4 ) );
		const __m128 hz4 = _mm_sub_ps( _mm_mul_ps( dx4, leaf->e2y4 ), _mm_mul_ps( dy4, leaf->e2x4 ) );
		const __m128 sx4 = _mm_sub_ps( ox4, leaf->v0x4 ), sy4 = _mm_sub_ps( oy4, leaf->v0y4 ), sz4 = _mm_sub_ps( oz4, leaf->v0z4 );
		const __m128 det4 = _mm_add_ps( _mm_mul_ps( leaf->e1z4, hz4 ), _mm_add_ps( _mm_mul_ps( leaf->e1x4, hx4 ), _mm_mul_ps( leaf->e1y4, hy4 ) ) );
		const __m128 qz4 = _mm_sub_ps( _mm_mul_ps( sx4, leaf->e1y4 ), _mm_mul_ps( sy4, leaf->e1x4 ) );
		const __m128 qx4 = _mm_sub_ps( _mm_mul_ps( sy4, leaf->e1z4 ), _mm_mul_ps( sz4, leaf->e1y4 ) );
		const __m128 qy4 = _mm_sub_ps( _mm_mul_ps( sz4, leaf->e1x4 ), _mm_mul_ps( sx4, leaf->e1z4 ) );
		const __m128 inv_det4 = fastrcp4( det4 );
		const __m128 u4 = _mm_mul_ps( _mm_add_ps( _mm_mul_ps( sz4, hz4 ), _mm_add_ps( _mm_mul_ps( sx4, hx4 ), _mm_mul_ps( sy4, hy4 ) ) ), inv_det4 );
		const __m128 v4 = _mm_mul_ps( _mm_add_ps( _mm_mul_ps( dz4, qz4 ), _mm_add_ps( _mm_mul_ps( dx4, qx4 ), _mm_mul_ps( dy4, qy4 ) ) ), inv_det4 );
		const __m128 ta4 = _mm_mul_ps( _mm_add_ps( _mm_mul_ps( leaf->e2z4, qz4 ), _mm_add_ps( _mm_mul_ps( leaf->e2x4, qx4 ), _mm_mul_ps( leaf->e2y4, qy4 ) ) ), inv_det4 );
		const __m128 mask1 = _mm_and_ps( _mm_cmpge_ps( u4, zero4 ), _mm_cmpge_ps( v4, zero4 ) );
		const __m128 mask2 = _mm_cmple_ps( _mm_add_ps( u4, v4 ), one4 );
		const __m128 mask3 = _mm_and_ps( _mm_cmplt_ps( ta4, t4 ), _mm_cmpgt_ps( ta4, zero4 ) );
		__m128 combined = _mm_and_ps( _mm_and_ps( mask1, mask2 ), mask3 );
		uint32_t imask = _mm_movemask_ps( combined );
		// evaluate opacity map, if present (SSE version).
		if (opmap) if (imask)
		{
			const __m128 fN4 = _mm_set1_ps( (float)opmapN );
			const __m128i row4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_add_ps( u4, v4 ), fN4 ) );
			const __m128i dia4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_sub_ps( one4, u4 ), fN4 ) );
			const __m128i v0 = _mm_mullo_epi32( row4, row4 );
			const __m128i v1 = _mm_cvttps_epi32( _mm_mul_ps( v4, fN4 ) );
			const __m128i v2 = _mm_sub_epi32( dia4, _mm_sub_epi32( _mm_set1_epi32( opmapN - 1 ), row4 ) );
			union { uint32_t idx[4]; __m128i idx4; };
			union { uint32_t omask[4]; __m128 omask4; };
			idx4 = _mm_add_epi32( _mm_add_epi32( v0, v1 ), v2 );
			// proceed with scalar code for gather operation - TODO: better approach?
			omask[0] = omask[1] = omask[2] = omask[3] = 0;
			for (int i = 0; i < 4; i++) if (imask & (1 << i))
			{
				uint32_t* om = opmap + leaf->primIdx[i] * ((opmapN * opmapN + 31) >> 5);
				if (om[idx[i] >> 5] & (1 << (idx[i] & 31))) omask[i] = 0xffffffff;
			}
			// combine
			combined = _mm_and_ps( combined, omask4 );
			imask = _mm_movemask_ps( combined );
		}
		if (imask)
		{
			const __m128 dist4 = _mm_blendv_ps( inf4, ta4, combined );
			// compute broadcasted horizontal minimum of dist4
			const __m128 a = _mm_min_ps( dist4, _mm_shuffle_ps( dist4, dist4, _MM_SHUFFLE( 2, 1, 0, 3 ) ) );
			const __m128 c = _mm_min_ps( a, _mm_shuffle_ps( a, a, _MM_SHUFFLE( 1, 0, 3, 2 ) ) );
			const uint32_t lane = __bfind( _mm_movemask_ps( _mm_cmpeq_ps( c, dist4 ) ) );
			// update hit record
			const __m128 _d4 = dist4;
			const float t = ((float*)&_d4)[lane];
			const __m128 _u4 = u4, _v4 = v4;
			ray.hit.t = t, ray.hit.u = ((float*)&_u4)[lane], ray.hit.v = ((float*)&_v4)[lane];
		#if INST_IDX_BITS == 32
			ray.hit.prim = leaf->primIdx[lane], ray.hit.inst = ray.instIdx;
		#else
			ray.hit.prim = leaf->primIdx[lane] + ray.instIdx;
		#endif
			t4 = _mm_set1_ps( t );
			// compress stack
			uint32_t outStackPtr = 0;
			for (int32_t i = 0; i < stackPtr; i += 4)
			{
				__m128i node4 = _mm_load_si128( (__m128i*)(nodeStack + i) );
				__m128 d4 = _mm_load_ps( (float*)(distStack + i) );
				const uint32_t mask = _mm_movemask_ps( _mm_cmple_ps( d4, t4 ) );
				const __m128i shfl16 = idxLUT4_[mask];
				const __m128i dst4 = _mm_shuffle_epi8( _mm_castps_si128( d4 ), shfl16 );
				node4 = _mm_shuffle_epi8( node4, shfl16 );
				_mm_storeu_si128( (__m128i*)(distStack + outStackPtr), dst4 );
				_mm_storeu_si128( (__m128i*)(nodeStack + outStackPtr), node4 );
				const int32_t numItems = tinybvh_min( 4, stackPtr - i ), validMask = (1 << numItems) - 1;
				outStackPtr += __popc( mask & validMask );
			}
			stackPtr = outStackPtr;
		}
		if (!stackPtr) break;
		nodeIdx = nodeStack[--stackPtr];
	}
the_end:
#ifdef _DEBUG
	return steps;
#else
	return 0;
#endif
}

bool BVH4_CPU::IsOccluded( const Ray& ray ) const
{
	VALIDATE_RAY( ray );
	const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
	if (!posX) goto negx;
	if (posY) { if (posZ) return IsOccluded<true, true, true>( ray ); else return IsOccluded<true, true, false>( ray ); }
	if (posZ) return IsOccluded<true, false, true>( ray ); else return IsOccluded<true, false, false>( ray );
negx:
	if (posY) { if (posZ) return IsOccluded<false, true, true>( ray ); else return IsOccluded<false, true, false>( ray ); }
	if (posZ) return IsOccluded<false, false, true>( ray ); else return IsOccluded<false, false, false>( ray );
}

template <bool posX, bool posY, bool posZ> bool BVH4_CPU::IsOccluded( const Ray& ray ) const
{
	ALIGNED( 64 ) uint32_t nodeStack[256];
	ALIGNED( 64 ) int32_t stackPtr = 0, nodeIdx = 0;
	const __m128 t4 = _mm_set1_ps( ray.hit.t );
	const __m128 rx4 = _mm_set1_ps( ray.O.x * ray.rD.x ), rdx4 = _mm_set1_ps( ray.rD.x );
	const __m128 ry4 = _mm_set1_ps( ray.O.y * ray.rD.y ), rdy4 = _mm_set1_ps( ray.rD.y );
	const __m128 rz4 = _mm_set1_ps( ray.O.z * ray.rD.z ), rdz4 = _mm_set1_ps( ray.rD.z );
	const __m128 ox4 = _mm_set1_ps( ray.O.x ), oy4 = _mm_set1_ps( ray.O.y ), oz4 = _mm_set1_ps( ray.O.z );
	const __m128 dx4 = _mm_set1_ps( ray.D.x ), dy4 = _mm_set1_ps( ray.D.y ), dz4 = _mm_set1_ps( ray.D.z );
	const __m128 one4 = _mm_set1_ps( 1.0f ), zero4 = _mm_setzero_ps();
	while (1)
	{
		while (!(nodeIdx & LEAF_BIT))
		{
			const BVHNode* n = (BVHNode*)(bvh4Data + nodeIdx);
			const __m128 tx1 = _mm_sub_ps( _mm_mul_ps( posX ? n->xmin4 : n->xmax4, rdx4 ), rx4 );
			const __m128 ty1 = _mm_sub_ps( _mm_mul_ps( posY ? n->ymin4 : n->ymax4, rdy4 ), ry4 );
			const __m128 tz1 = _mm_sub_ps( _mm_mul_ps( posZ ? n->zmin4 : n->zmax4, rdz4 ), rz4 );
			const __m128 tx2 = _mm_sub_ps( _mm_mul_ps( posX ? n->xmax4 : n->xmin4, rdx4 ), rx4 );
			const __m128 ty2 = _mm_sub_ps( _mm_mul_ps( posY ? n->ymax4 : n->ymin4, rdy4 ), ry4 );
			const __m128 tz2 = _mm_sub_ps( _mm_mul_ps( posZ ? n->zmax4 : n->zmin4, rdz4 ), rz4 );
			const __m128 tmin = _mm_max_ps( _mm_max_ps( _mm_max_ps( _mm_setzero_ps(), tx1 ), ty1 ), tz1 );
			const __m128 tmax = _mm_min_ps( _mm_min_ps( _mm_min_ps( tx2, t4 ), ty2 ), tz2 );
			const __m128 mask4 = _mm_cmple_ps( tmin, tmax );
			const uint32_t mask = _mm_movemask_ps( mask4 );
			const uint32_t validNodes = __popc( mask );
			if (validNodes == 1)
			{
				const uint32_t lane = __bfind( mask );
				nodeIdx = ((uint32_t*)&n->child4)[lane];
			}
			else if (validNodes)
			{
				const __m128i cpi = idxLUT4_[mask];
				const __m128i child4 = _mm_shuffle_epi8( n->child4, cpi );
				_mm_storeu_si128( (__m128i*)(nodeStack + stackPtr), child4 );
				stackPtr += validNodes - 1;
				nodeIdx = nodeStack[stackPtr];
			}
			else
			{
				if (!stackPtr) return false;
				nodeIdx = nodeStack[--stackPtr];
			}
		}
		uint32_t n;
		memcpy( &n, &nodeIdx, 4 );
		// Moeller-Trumbore ray/triangle intersection algorithm for four triangles
		const BVHTri4Leaf* leaf = (BVHTri4Leaf*)(bvh4Data + (n & 0x1fffffff));
		const __m128 hx4 = _mm_sub_ps( _mm_mul_ps( dy4, leaf->e2z4 ), _mm_mul_ps( dz4, leaf->e2y4 ) );
		const __m128 hy4 = _mm_sub_ps( _mm_mul_ps( dz4, leaf->e2x4 ), _mm_mul_ps( dx4, leaf->e2z4 ) );
		const __m128 hz4 = _mm_sub_ps( _mm_mul_ps( dx4, leaf->e2y4 ), _mm_mul_ps( dy4, leaf->e2x4 ) );
		const __m128 sx4 = _mm_sub_ps( ox4, leaf->v0x4 ), sy4 = _mm_sub_ps( oy4, leaf->v0y4 ), sz4 = _mm_sub_ps( oz4, leaf->v0z4 );
		const __m128 det4 = _mm_add_ps( _mm_mul_ps( leaf->e1z4, hz4 ), _mm_add_ps( _mm_mul_ps( leaf->e1x4, hx4 ), _mm_mul_ps( leaf->e1y4, hy4 ) ) );
		const __m128 qz4 = _mm_sub_ps( _mm_mul_ps( sx4, leaf->e1y4 ), _mm_mul_ps( sy4, leaf->e1x4 ) );
		const __m128 qx4 = _mm_sub_ps( _mm_mul_ps( sy4, leaf->e1z4 ), _mm_mul_ps( sz4, leaf->e1y4 ) );
		const __m128 qy4 = _mm_sub_ps( _mm_mul_ps( sz4, leaf->e1x4 ), _mm_mul_ps( sx4, leaf->e1z4 ) );
		const __m128 inv_det4 = fastrcp4( det4 );
		const __m128 u4 = _mm_mul_ps( _mm_add_ps( _mm_mul_ps( sz4, hz4 ), _mm_add_ps( _mm_mul_ps( sx4, hx4 ), _mm_mul_ps( sy4, hy4 ) ) ), inv_det4 );
		const __m128 v4 = _mm_mul_ps( _mm_add_ps( _mm_mul_ps( dz4, qz4 ), _mm_add_ps( _mm_mul_ps( dx4, qx4 ), _mm_mul_ps( dy4, qy4 ) ) ), inv_det4 );
		const __m128 ta4 = _mm_mul_ps( _mm_add_ps( _mm_mul_ps( leaf->e2z4, qz4 ), _mm_add_ps( _mm_mul_ps( leaf->e2x4, qx4 ), _mm_mul_ps( leaf->e2y4, qy4 ) ) ), inv_det4 );
		const __m128 mask1 = _mm_and_ps( _mm_cmpge_ps( u4, zero4 ), _mm_cmpge_ps( v4, zero4 ) );
		const __m128 mask2 = _mm_cmple_ps( _mm_add_ps( u4, v4 ), one4 );
		const __m128 mask3 = _mm_and_ps( _mm_cmplt_ps( ta4, t4 ), _mm_cmpgt_ps( ta4, zero4 ) );
		__m128 combined = _mm_and_ps( _mm_and_ps( mask1, mask2 ), mask3 );
		// evaluate opacity map, if present (SSE version).
		if (_mm_movemask_ps( combined ))
		{
			if (!opmap) return true;
			// evaluate opacity map, SSE version.
			const __m128 fN4 = _mm_set1_ps( (float)opmapN );
			const __m128i row4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_add_ps( u4, v4 ), fN4 ) );
			const __m128i dia4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_sub_ps( one4, u4 ), fN4 ) );
			const __m128i v0 = _mm_mullo_epi32( row4, row4 );
			const __m128i v1 = _mm_cvttps_epi32( _mm_mul_ps( v4, fN4 ) );
			const __m128i v2 = _mm_sub_epi32( dia4, _mm_sub_epi32( _mm_set1_epi32( opmapN - 1 ), row4 ) );
			union { uint32_t idx[4]; __m128i idx4; };
			idx4 = _mm_add_epi32( _mm_add_epi32( v0, v1 ), v2 );
			// proceed with scalar code for gather operation - TODO: better approach?
			const uint32_t imask = _mm_movemask_ps( combined );
			for (int i = 0; i < 4; i++) if (imask & (1 << i))
			{
				uint32_t* om = opmap + leaf->primIdx[i] * ((opmapN * opmapN + 31) >> 5);
				if (om[idx[i] >> 5] & (1 << (idx[i] & 31))) return true;
			}
		}
		// we continue.
		if (!stackPtr) return false;
		nodeIdx = nodeStack[--stackPtr];
	}
}

#endif // BVH_USESSE

#ifdef BVH_USEAVX

// Ultra-fast single-threaded AVX binned-SAH-builder.
// This code produces BVHs nearly identical to reference, but much faster.
// On a 12th gen laptop i7 CPU, Sponza Crytek (~260k tris) is processed in 51ms.
// The code relies on the availability of AVX instructions. AVX2 is not needed.
#if defined _MSC_VER && !defined __clang__
#define LANE(a,b) a.m128_f32[b]
#define LANE8(a,b) a.m256_f32[b]
// Not using clang/g++ method under MSCC; compiler may benefit from .m128_i32.
#define ILANE(a,b) a.m128i_i32[b]
#else
#define LANE(a,b) a[b]
#define LANE8(a,b) a[b]
// Below method reduces to a single instruction.
#define ILANE(a,b) _mm_cvtsi128_si32(_mm_castps_si128( _mm_shuffle_ps(_mm_castsi128_ps( a ), _mm_castsi128_ps( a ), b)))
#endif
inline __m256 fastrcp8( const __m256 a )
{
	__m256 res = _mm256_rcp_ps( a );
	__m256 muls = _mm256_mul_ps( a, _mm256_mul_ps( res, res ) );
	return _mm256_sub_ps( _mm256_add_ps( res, res ), muls );
}
inline float halfArea( const __m128 a /* a contains extent of aabb */ )
{
	return LANE( a, 0 ) * LANE( a, 1 ) + LANE( a, 1 ) * LANE( a, 2 ) + LANE( a, 2 ) * LANE( a, 3 );
}
inline float halfArea( const __m256& a /* a contains aabb itself, with min.xyz negated */ )
{
#ifndef _MSC_VER
	// g++ doesn't seem to like the faster construct
	float* c = (float*)&a;
	float ex = c[4] + c[0], ey = c[5] + c[1], ez = c[6] + c[2];
	return ex * ey + ey * ez + ez * ex;
#else
	const __m128 q = _mm256_castps256_ps128( _mm256_add_ps( _mm256_permute2f128_ps( a, a, 5 ), a ) );
	const __m128 v = _mm_mul_ps( q, _mm_shuffle_ps( q, q, 9 ) );
	return LANE( v, 0 ) + LANE( v, 1 ) + LANE( v, 2 );
#endif
}
#define PROCESS_PLANE( a, pos, ANLR, lN, rN, lb, rb ) if (lN * rN != 0) { \
	ANLR = halfArea( lb ) * (float)lN + halfArea( rb ) * (float)rN; if (ANLR < splitCost) \
	splitCost = ANLR, bestAxis = a, bestPos = pos, bestLBox = lb, bestRBox = rb; }
#if defined _MSC_VER
#pragma warning ( push )
#pragma warning( disable:4701 ) // "potentially uninitialized local variable 'bestLBox' used"
#pragma warning (disable:4324) // "lambda structure was padded due to alignment specifier"
#elif defined __GNUC__ && !defined __clang__
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wmaybe-uninitialized"
#endif
void BVH::BuildAVX( const bvhvec4* vertices, const uint32_t primCount )
{
	// build the BVH with a continuous array of bvhvec4 vertices:
	// in this case, the stride for the slice is 16 bytes.
	BuildAVX( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}
void BVH::BuildAVX( const bvhvec4slice& vertices )
{
	PrepareAVXBuild( vertices, 0, 0 );
	BuildAVXSubtree( 0u, 0u );
}
void BVH::BuildAVX( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount )
{
	// build the BVH with an indexed array of bvhvec4 vertices.
	bvhvec4slice slice( vertices, primCount * 3, sizeof( bvhvec4 ) );
	BuildAVX( slice, indices, primCount );
}
void BVH::BuildAVX( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount )
{
	PrepareAVXBuild( vertices, indices, primCount );
	BuildAVXSubtree( 0u, 0u );
}
void BVH::PrepareAVXBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t prims )
{
	BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareAVXBuild( .. ), primCount == 0." );
	BVH_FATAL_ERROR_IF( vertices.stride & 15, "BVH::PrepareAVXBuild( .. ), stride must be multiple of 16." );
	// some constants
	static const __m128 min4 = _mm_set1_ps( BVH_FAR ), max4 = _mm_set1_ps( -BVH_FAR );
	// reset node pool
	uint32_t primCount = prims > 0 ? prims : vertices.count / 3;
	const uint32_t spaceNeeded = primCount * 2;
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		primIdx = (uint32_t*)AlignedAlloc( primCount * sizeof( uint32_t ) );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 ); // avoid crash in refit.
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	else BVH_FATAL_ERROR_IF( !rebuildable, "BVH::BuildAVX( .. ), bvh not rebuildable." );
	verts = vertices; // note: we're not copying this data; don't delete.
	vertIdx = (uint32_t*)indices;
	triCount = idxCount = primCount;
	newNodePtr = 2;
	struct FragSSE { __m128 bmin4, bmax4; };
	FragSSE* frag4 = (FragSSE*)fragment;
	const __m128* verts4 = (__m128*)verts.data; // that's why it must be 16-byte aligned.
	// assign all triangles to the root node
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = triCount;
	// initialize fragments and update root bounds
	__m128 rootMin = min4, rootMax = max4;
	uint32_t stride4 = verts.stride / 16;
	if (indices)
	{
		BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareAVXBuild( .. ), empty vertex slice." );
		BVH_FATAL_ERROR_IF( prims == 0, "BVH::PrepareAVXBuild( .. ), prims == 0." );
		// build the BVH over indexed triangles
		for (uint32_t i = 0; i < triCount; i++)
		{
			const uint32_t i0 = indices[i * 3], i1 = indices[i * 3 + 1], i2 = indices[i * 3 + 2];
			const __m128 v0 = verts4[i0 * stride4], v1 = verts4[i1 * stride4], v2 = verts4[i2 * stride4];
			const __m128 t1 = _mm_min_ps( _mm_min_ps( v0, v1 ), v2 );
			const __m128 t2 = _mm_max_ps( _mm_max_ps( v0, v1 ), v2 );
			frag4[i].bmin4 = t1, frag4[i].bmax4 = t2, rootMin = _mm_min_ps( rootMin, t1 ), rootMax = _mm_max_ps( rootMax, t2 );
			primIdx[i] = i;
		}
	}
	else
	{
		BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareAVXBuild( .. ), empty vertex slice." );
		BVH_FATAL_ERROR_IF( prims != 0, "BVH::PrepareAVXBuild( .. ), indices == 0." );
		// build the BVH over a list of vertices: three per triangle
		for (uint32_t i = 0; i < triCount; i++)
		{
			const __m128 v0 = verts4[(i * 3) * stride4], v1 = verts4[(i * 3 + 1) * stride4], v2 = verts4[(i * 3 + 2) * stride4];
			const __m128 t1 = _mm_min_ps( _mm_min_ps( v0, v1 ), v2 );
			const __m128 t2 = _mm_max_ps( _mm_max_ps( v0, v1 ), v2 );
			frag4[i].bmin4 = t1, frag4[i].bmax4 = t2, rootMin = _mm_min_ps( rootMin, t1 ), rootMax = _mm_max_ps( rootMax, t2 );
			primIdx[i] = i;
		}
	}
	root.aabbMin = *(bvhvec3*)&rootMin, root.aabbMax = *(bvhvec3*)&rootMax;
	bvh_over_indices = indices != nullptr;
}

void BVH::BuildAVXBinTask( const uint32_t first, const uint32_t last, __m256* binbox, __m256* orig,
	uint32_t* count, const __m128& nmin4, const __m128& rpd4 )
{
	struct FragSSE { __m128 bmin4, bmax4; };
	FragSSE* frag4 = (FragSSE*)fragment;
	__m256* frag8 = (__m256*)fragment;
	uint32_t fi = primIdx[first];
	memset( count, 0, 3 * AVXBINS * 4 );
	memcpy( binbox, orig, 3 * AVXBINS * 32 );
	__m256 r0, r1, r2, f = _mm256_xor_ps( frag8[fi], signFlip8 );
	const __m128i zero4i = _mm_setzero_si128();
	union { __m128i bc4; uint32_t bc[4]; };
	bc4 = _mm_max_epi32( _mm_cvtps_epi32( _mm_sub_ps( _mm_mul_ps( _mm_sub_ps( _mm_add_ps( frag4[fi].bmax4, frag4[fi].bmin4 ), nmin4 ), rpd4 ), half4 ) ), zero4i );
#ifdef _DEBUG
	// this should never trigger. leaving it in for _DEBUG for now.
	for (int i = 0; i < 4; i++) if (bc4.m128i_i32[i] >= 8) printf( "CLAMPED TO 7\n" );
#endif
	uint32_t i0 = bc[0], i1 = bc[1], i2 = bc[2], * ti = primIdx + first + 1;
	for (uint32_t i = first; i < last - 1; i++)
	{
		uint32_t fid = *ti++;
	#if defined __GNUC__ || _MSC_VER < 1920
		if (fid > triCount) fid = triCount - 1; // never happens but g++ *and* vs2017 need this to not crash...
	#endif
		const __m256 b0 = binbox[i0], b1 = binbox[AVXBINS + i1], b2 = binbox[2 * AVXBINS + i2];
		const __m128 frmin = frag4[fid].bmin4, frmax = frag4[fid].bmax4;
		r0 = _mm256_max_ps( b0, f ), r1 = _mm256_max_ps( b1, f ), r2 = _mm256_max_ps( b2, f );
		bc4 = _mm_max_epi32( _mm_cvtps_epi32( _mm_sub_ps( _mm_mul_ps( _mm_sub_ps( _mm_add_ps( frmax, frmin ), nmin4 ), rpd4 ), half4 ) ), zero4i );
	#ifdef _DEBUG
		// this should never trigger. leaving it in for _DEBUG for now.
		for (int i = 0; i < 4; i++) if (bc4.m128i_i32[i] >= 8) printf( "CLAMPED TO 7\n" );
	#endif
		f = _mm256_xor_ps( frag8[fid], signFlip8 );
		count[i0]++, count[AVXBINS + i1]++, count[AVXBINS * 2 + i2]++;
		binbox[i0] = r0, i0 = bc[0];
		binbox[AVXBINS + i1] = r1, i1 = bc[1];
		binbox[2 * AVXBINS + i2] = r2, i2 = bc[2];
	}
	// final business for final fragment
	const __m256 b0 = binbox[i0], b1 = binbox[AVXBINS + i1], b2 = binbox[2 * AVXBINS + i2];
	count[i0]++, count[AVXBINS + i1]++, count[AVXBINS * 2 + i2]++;
	r0 = _mm256_max_ps( b0, f ), r1 = _mm256_max_ps( b1, f ), r2 = _mm256_max_ps( b2, f );
	binbox[i0] = r0, binbox[AVXBINS + i1] = r1, binbox[2 * AVXBINS + i2] = r2;
}

#ifndef MT_USE_JOBSYSTEM
void BuildAVXSubtree_( uint32_t n, uint32_t d, BVH* bvh ) { bvh->BuildAVXSubtree( n, d ); }
#endif
void BVH::BuildAVXSubtree( uint32_t nodeIdx, uint32_t depth )
{
	// aligned data
	constexpr uint32_t slices = 8;
	ALIGNED( 64 ) __m256 slicebinbox[slices][3 * AVXBINS];
	ALIGNED( 64 ) uint32_t slicecount[slices][3 * AVXBINS];
	ALIGNED( 64 ) __m256 binboxOrig[3 * AVXBINS];		// 768 bytes
	ALIGNED( 64 ) __m256 bestLBox, bestRBox;			// 64 bytes
	__m256* binbox = slicebinbox[0];
	uint32_t* count = slicecount[0];
	for (uint32_t i = 0; i < 3 * AVXBINS; i++) binboxOrig[i] = max8; // binbox initialization template
	if (depth == 0)
	{
		// avoid threaded building for small meshes: not efficient; build multiple in parallel instead.
	#ifdef NO_THREADED_BUILDS
		threadedBuild = false;
	#else
		if (triCount < MT_BUILD_THRESHOLD) threadedBuild = false; else
		{
		#ifdef MT_USE_JOBSYSTEM
			if (!globalSubtreeJobs) globalSubtreeJobs = new JobSystem();
			if (!globalBinningJobs) globalBinningJobs = new JobSystem();
			subtreeJobs = globalSubtreeJobs;
			binningJobs = globalBinningJobs;
		#endif
			atomicNewNodePtr = new std::atomic<uint32_t>( newNodePtr );
		}
	#endif
	}
	// subdivide recursively
	ALIGNED( 64 ) uint32_t task[256], taskCount = 0;
	BVHNode& root = bvhNode[0];
	const bvhvec3 minDim = (root.aabbMax - root.aabbMin) * 1e-7f;
	while (1)
	{
		while (1)
		{
			BVHNode& node = bvhNode[nodeIdx];
			__m128* node4 = (__m128*) & bvhNode[nodeIdx];
			// find optimal object split
			const __m128 d4 = _mm_blendv_ps( min1, _mm_sub_ps( node4[1], node4[0] ), mask3 );
			const __m128 nmin4 = _mm_add_ps( node4[0], node4[0] );
			const __m128 rpd4 = _mm_and_ps( _mm_div_ps( binmul3, d4 ), _mm_cmpneq_ps( d4, _mm_setzero_ps() ) );
			// implementation of Section 4.1 of "Parallel Spatial Splits in Bounding Volume Hierarchies":
			// main loop operates on two fragments to minimize dependencies and maximize ILP.
		#ifdef MT_USE_JOBSYSTEM
			if (threadedBuild && node.triCount > MT_BUILD_THRESHOLD)
			{
				// run binning in parallel slices
				const uint32_t sliceSize = node.triCount / slices;
				for (int slice = 0; slice < (int)slices; slice++)
				{
					// calculate task start and end
					const uint32_t first = node.leftFirst + sliceSize * slice;
					const uint32_t last = slice == (slices - 1) ? (node.leftFirst + node.triCount) : (first + sliceSize);
					__m256* sbb = slicebinbox[slice], * bo = binboxOrig;
					uint32_t* sc = slicecount[slice];
					BVH* thisBVH = this; // avoid warnings / complexities of capturing this
					binningJobs->Execute( [=]() { thisBVH->BuildAVXBinTask( first, last, sbb, bo, sc, nmin4, rpd4 ); } );
				}
				binningJobs->Wait();
				// combine results from threads
				for (int a = 0; a < 3; a++) for (int i = 0; i < AVXBINS; i++)
					for (int ai = a * AVXBINS + i, slice = 1; slice < (int)slices; slice++) count[ai] += slicecount[slice][ai],
						binbox[ai] = _mm256_max_ps( binbox[ai], slicebinbox[slice][ai] );
			}
			else
			#else
				// threaded binning requires use of the jobsystem.
		#endif
			BuildAVXBinTask( node.leftFirst, node.leftFirst + node.triCount, binbox, binboxOrig, count, nmin4, rpd4 );
			// calculate per-split totals
			float splitCost = BVH_FAR;
			const float rSAV = 1.0f / node.SurfaceArea();
			uint32_t bestAxis = 0, bestPos = 0;
			const __m256* bb = binbox;
			for (int32_t a = 0; a < 3; a++, bb += AVXBINS) if ((node.aabbMax[a] - node.aabbMin[a]) > minDim[a])
			{
				// hardcoded bin processing for AVXBINS == 8
				assert( AVXBINS == 8 );
				const uint32_t* cnt = count + a * AVXBINS;
				const uint32_t lN0 = cnt[0], rN0 = cnt[7];
				const __m256 lb0 = bb[0], rb0 = bb[7];
				const uint32_t lN1 = lN0 + cnt[1], rN1 = rN0 + cnt[6], lN2 = lN1 + cnt[2];
				const uint32_t rN2 = rN1 + cnt[5], lN3 = lN2 + cnt[3], rN3 = rN2 + cnt[4];
				const __m256 lb1 = _mm256_max_ps( lb0, bb[1] ), rb1 = _mm256_max_ps( rb0, bb[6] );
				const __m256 lb2 = _mm256_max_ps( lb1, bb[2] ), rb2 = _mm256_max_ps( rb1, bb[5] );
				const __m256 lb3 = _mm256_max_ps( lb2, bb[3] ), rb3 = _mm256_max_ps( rb2, bb[4] );
				const uint32_t lN4 = lN3 + cnt[4], rN4 = rN3 + cnt[3], lN5 = lN4 + cnt[5];
				const uint32_t rN5 = rN4 + cnt[2], lN6 = lN5 + cnt[6], rN6 = rN5 + cnt[1];
				const __m256 lb4 = _mm256_max_ps( lb3, bb[4] ), rb4 = _mm256_max_ps( rb3, bb[3] );
				const __m256 lb5 = _mm256_max_ps( lb4, bb[5] ), rb5 = _mm256_max_ps( rb4, bb[2] );
				const __m256 lb6 = _mm256_max_ps( lb5, bb[6] ), rb6 = _mm256_max_ps( rb5, bb[1] );
				float ANLR3 = BVH_FAR; PROCESS_PLANE( a, 3, ANLR3, lN3, rN3, lb3, rb3 ); // most likely split
				float ANLR2 = BVH_FAR; PROCESS_PLANE( a, 2, ANLR2, lN2, rN4, lb2, rb4 );
				float ANLR4 = BVH_FAR; PROCESS_PLANE( a, 4, ANLR4, lN4, rN2, lb4, rb2 );
				float ANLR5 = BVH_FAR; PROCESS_PLANE( a, 5, ANLR5, lN5, rN1, lb5, rb1 );
				float ANLR1 = BVH_FAR; PROCESS_PLANE( a, 1, ANLR1, lN1, rN5, lb1, rb5 );
				float ANLR0 = BVH_FAR; PROCESS_PLANE( a, 0, ANLR0, lN0, rN6, lb0, rb6 );
				float ANLR6 = BVH_FAR; PROCESS_PLANE( a, 6, ANLR6, lN6, rN0, lb6, rb0 ); // least likely split
			}
			splitCost = c_trav + c_int * rSAV * splitCost;
			const float noSplitCost = (float)node.triCount * c_int;
			if (splitCost >= noSplitCost) break; // not splitting is better.
			// in-place partition
			const float rpd = (*(bvhvec3*)&rpd4)[bestAxis], nmin = (*(bvhvec3*)&nmin4)[bestAxis];
			uint32_t i = node.leftFirst, j = node.leftFirst + node.triCount, t, fr = primIdx[i];
			for (uint32_t k = 0; k < node.triCount; k++)
			{
				const uint32_t bi = (uint32_t)((fragment[fr].bmax[bestAxis] + fragment[fr].bmin[bestAxis] - nmin) * rpd);
				if (bi <= bestPos) fr = primIdx[++i]; else t = fr, fr = primIdx[i] = primIdx[--j], primIdx[j] = t;
			}
			// create child nodes and recurse
			uint32_t n;
			if (threadedBuild) n = atomicNewNodePtr->fetch_add( 2 ); else n = newNodePtr, newNodePtr += 2;
			const uint32_t leftCount = i - node.leftFirst, rightCount = node.triCount - leftCount;
			if (leftCount == 0 || rightCount == 0 || taskCount == BVH_NUM_ELEMS( task )) break; // should not happen.
			*(__m256*)& bvhNode[n] = _mm256_xor_ps( bestLBox, signFlip8 );
			bvhNode[n].leftFirst = node.leftFirst, bvhNode[n].triCount = leftCount;
			node.leftFirst = n, node.triCount = 0;
			*(__m256*)& bvhNode[n + 1] = _mm256_xor_ps( bestRBox, signFlip8 );
			bvhNode[n + 1].leftFirst = i, bvhNode[n + 1].triCount = rightCount;
		#ifdef MT_USE_JOBSYSTEM
			const bool spawnThreads = leftCount + rightCount > 5000 && depth < MT_JOBSYSTEM_DEPTH && threadedBuild;
			if (!spawnThreads) task[taskCount++] = n + 1, nodeIdx = n; else
			{
				BVH* thisBVH = this; // avoid warnings / complexities of capturing this
				subtreeJobs->Execute( [=]() { thisBVH->BuildAVXSubtree( n + 1, depth + 1 ); } );
				nodeIdx = n;
			}
		#else
			if (leftCount + rightCount > 2000 && depth < 5 && threadedBuild)
			{
				std::thread t1( &BuildAVXSubtree_, n, depth + 1, this );
				BuildAVXSubtree( n + 1, depth + 1 );
				t1.join();
				break;
			}
			task[taskCount++] = n + 1, nodeIdx = n;
		#endif
		}
		// fetch subdivision task from stack
		if (taskCount == 0) break;
		nodeIdx = task[--taskCount];
	}
	// all done.
	if (depth == 0)
	{
		// wait for all threads to complete.
		if (threadedBuild)
		{
		#ifdef MT_USE_JOBSYSTEM
			subtreeJobs->Wait();
		#endif
			newNodePtr = atomicNewNodePtr->load();
			delete atomicNewNodePtr;
			atomicNewNodePtr = 0;
		}
		// tree has been built.
		aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
		refittable = true; // not using spatial splits: can refit this BVH
		may_have_holes = false; // there are no holes in the list of nodes.
		usedNodes = newNodePtr;
	}
}

#if defined _MSC_VER
#pragma warning ( pop ) // restore 4701
#elif defined __GNUC__ && !defined __clang__
#pragma GCC diagnostic pop // restore -Wmaybe-uninitialized
#endif

// Intersect a BVH with a ray packet, basic SSE-optimized version.
// Note: This yields +10% on 10th gen Intel CPUs, but a small loss on
// more recent hardware. This function needs a full conversion to work
// with groups of 8 rays at a time - TODO.
void BVH::Intersect256RaysSSE( Ray* packet ) const
{
	// Corner rays are: 0, 51, 204 and 255
	// Construct the bounding planes, with normals pointing outwards
	bvhvec3 O = packet[0].O; // same for all rays in this case
	__m128 O4 = *(__m128*) & packet[0].O;
	__m128 mask4 = _mm_cmpeq_ps( _mm_setzero_ps(), _mm_set_ps( 1, 0, 0, 0 ) );
	bvhvec3 p0 = packet[0].O + packet[0].D; // top-left
	bvhvec3 p1 = packet[51].O + packet[51].D; // top-right
	bvhvec3 p2 = packet[204].O + packet[204].D; // bottom-left
	bvhvec3 p3 = packet[255].O + packet[255].D; // bottom-right
	bvhvec3 plane0 = tinybvh_normalize( tinybvh_cross( p0 - O, p0 - p2 ) ); // left plane
	bvhvec3 plane1 = tinybvh_normalize( tinybvh_cross( p3 - O, p3 - p1 ) ); // right plane
	bvhvec3 plane2 = tinybvh_normalize( tinybvh_cross( p1 - O, p1 - p0 ) ); // top plane
	bvhvec3 plane3 = tinybvh_normalize( tinybvh_cross( p2 - O, p2 - p3 ) ); // bottom plane
	int32_t sign0x = plane0.x < 0 ? 4 : 0, sign0y = plane0.y < 0 ? 5 : 1, sign0z = plane0.z < 0 ? 6 : 2;
	int32_t sign1x = plane1.x < 0 ? 4 : 0, sign1y = plane1.y < 0 ? 5 : 1, sign1z = plane1.z < 0 ? 6 : 2;
	int32_t sign2x = plane2.x < 0 ? 4 : 0, sign2y = plane2.y < 0 ? 5 : 1, sign2z = plane2.z < 0 ? 6 : 2;
	int32_t sign3x = plane3.x < 0 ? 4 : 0, sign3y = plane3.y < 0 ? 5 : 1, sign3z = plane3.z < 0 ? 6 : 2;
	float t0 = tinybvh_dot( O, plane0 ), t1 = tinybvh_dot( O, plane1 );
	float t2 = tinybvh_dot( O, plane2 ), t3 = tinybvh_dot( O, plane3 );
	// Traverse the tree with the packet
	int32_t first = 0, last = 255; // first and last active ray in the packet
	BVHNode* node = &bvhNode[0];
	ALIGNED( 64 ) uint32_t stack[64], stackPtr = 0;
	while (1)
	{
		if (node->isLeaf())
		{
			// handle leaf node
			for (uint32_t j = 0; j < node->triCount; j++)
			{
				const uint32_t idx = primIdx[node->leftFirst + j], vid = idx * 3;
				const bvhvec3 e1 = verts[vid + 1] - verts[vid], e2 = verts[vid + 2] - verts[vid];
				const bvhvec3 s = O - bvhvec3( verts[vid] );
				for (int32_t i = first; i <= last; i++)
				{
					Ray& ray = packet[i];
					const bvhvec3 h = tinybvh_cross( ray.D, e2 );
					const float a = tinybvh_dot( e1, h );
					if (fabs( a ) < 0.0000001f) continue; // ray parallel to triangle
					const float f = 1 / a, u = f * tinybvh_dot( s, h );
					const bvhvec3 q = tinybvh_cross( s, e1 );
					const float v = f * tinybvh_dot( ray.D, q );
					if (u < 0 || v < 0 || u + v > 1) continue;
					const float t = f * tinybvh_dot( e2, q );
					if (t <= 0 || t >= ray.hit.t) continue;
					ray.hit.t = t, ray.hit.u = u, ray.hit.v = v, ray.hit.prim = idx;
				}
			}
			if (stackPtr == 0) break; else // pop
				last = stack[--stackPtr], node = bvhNode + stack[--stackPtr],
				first = last >> 8, last &= 255;
		}
		else
		{
			// fetch pointers to child nodes
			BVHNode* left = bvhNode + node->leftFirst;
			BVHNode* right = bvhNode + node->leftFirst + 1;
			bool visitLeft = true, visitRight = true;
			int32_t leftFirst = first, leftLast = last, rightFirst = first, rightLast = last;
			float distLeft, distRight;
			{
				// see if we want to intersect the left child
				const __m128 minO4 = _mm_sub_ps( *(__m128*) & left->aabbMin, O4 );
				const __m128 maxO4 = _mm_sub_ps( *(__m128*) & left->aabbMax, O4 );
				// 1. Early-in test: if first ray hits the node, the packet visits the node
				bool earlyHit;
				{
					const __m128 rD4 = *(__m128*) & packet[first].rD;
					const __m128 st1 = _mm_mul_ps( _mm_and_ps( minO4, mask4 ), rD4 );
					const __m128 st2 = _mm_mul_ps( _mm_and_ps( maxO4, mask4 ), rD4 );
					const __m128 vmax4 = _mm_max_ps( st1, st2 ), vmin4 = _mm_min_ps( st1, st2 );
					const float tmax = tinybvh_min( LANE( vmax4, 0 ), tinybvh_min( LANE( vmax4, 1 ), LANE( vmax4, 2 ) ) );
					const float tmin = tinybvh_max( LANE( vmin4, 0 ), tinybvh_max( LANE( vmin4, 1 ), LANE( vmin4, 2 ) ) );
					earlyHit = (tmax >= tmin && tmin < packet[first].hit.t && tmax >= 0);
					distLeft = tmin;
				}
				// 2. Early-out test: if the node aabb is outside the four planes, we skip the node
				if (!earlyHit)
				{
					float* minmax = (float*)left;
					bvhvec3 c0( minmax[sign0x], minmax[sign0y], minmax[sign0z] );
					bvhvec3 c1( minmax[sign1x], minmax[sign1y], minmax[sign1z] );
					bvhvec3 c2( minmax[sign2x], minmax[sign2y], minmax[sign2z] );
					bvhvec3 c3( minmax[sign3x], minmax[sign3y], minmax[sign3z] );
					if (tinybvh_dot( c0, plane0 ) > t0 || tinybvh_dot( c1, plane1 ) > t1 ||
						tinybvh_dot( c2, plane2 ) > t2 || tinybvh_dot( c3, plane3 ) > t3)
						visitLeft = false;
					else
					{
						// 3. Last resort: update first and last, stay in node if first > last
						for (; leftFirst <= leftLast; leftFirst++)
						{
							const __m128 rD4 = *(__m128*) & packet[leftFirst].rD;
							const __m128 st1 = _mm_mul_ps( _mm_and_ps( minO4, mask4 ), rD4 );
							const __m128 st2 = _mm_mul_ps( _mm_and_ps( maxO4, mask4 ), rD4 );
							const __m128 vmax4 = _mm_max_ps( st1, st2 ), vmin4 = _mm_min_ps( st1, st2 );
							const float tmax = tinybvh_min( LANE( vmax4, 0 ), tinybvh_min( LANE( vmax4, 1 ), LANE( vmax4, 2 ) ) );
							const float tmin = tinybvh_max( LANE( vmin4, 0 ), tinybvh_max( LANE( vmin4, 1 ), LANE( vmin4, 2 ) ) );
							if (tmax >= tmin && tmin < packet[leftFirst].hit.t && tmax >= 0) { distLeft = tmin; break; }
						}
						for (; leftLast >= leftFirst; leftLast--)
						{
							const __m128 rD4 = *(__m128*) & packet[leftLast].rD;
							const __m128 st1 = _mm_mul_ps( _mm_and_ps( minO4, mask4 ), rD4 );
							const __m128 st2 = _mm_mul_ps( _mm_and_ps( maxO4, mask4 ), rD4 );
							const __m128 vmax4 = _mm_max_ps( st1, st2 ), vmin4 = _mm_min_ps( st1, st2 );
							const float tmax = tinybvh_min( LANE( vmax4, 0 ), tinybvh_min( LANE( vmax4, 1 ), LANE( vmax4, 2 ) ) );
							const float tmin = tinybvh_max( LANE( vmin4, 0 ), tinybvh_max( LANE( vmin4, 1 ), LANE( vmin4, 2 ) ) );
							if (tmax >= tmin && tmin < packet[leftLast].hit.t && tmax >= 0) break;
						}
						visitLeft = leftLast >= leftFirst;
					}
				}
			}
			{
				// see if we want to intersect the right child
				const __m128 minO4 = _mm_sub_ps( *(__m128*) & right->aabbMin, O4 );
				const __m128 maxO4 = _mm_sub_ps( *(__m128*) & right->aabbMax, O4 );
				// 1. Early-in test: if first ray hits the node, the packet visits the node
				bool earlyHit;
				{
					const __m128 rD4 = *(__m128*) & packet[first].rD;
					const __m128 st1 = _mm_mul_ps( minO4, rD4 ), st2 = _mm_mul_ps( maxO4, rD4 );
					const __m128 vmax4 = _mm_max_ps( st1, st2 ), vmin4 = _mm_min_ps( st1, st2 );
					const float tmax = tinybvh_min( LANE( vmax4, 0 ), tinybvh_min( LANE( vmax4, 1 ), LANE( vmax4, 2 ) ) );
					const float tmin = tinybvh_max( LANE( vmin4, 0 ), tinybvh_max( LANE( vmin4, 1 ), LANE( vmin4, 2 ) ) );
					earlyHit = (tmax >= tmin && tmin < packet[first].hit.t && tmax >= 0);
					distRight = tmin;
				}
				// 2. Early-out test: if the node aabb is outside the four planes, we skip the node
				if (!earlyHit)
				{
					float* minmax = (float*)right;
					bvhvec3 c0( minmax[sign0x], minmax[sign0y], minmax[sign0z] );
					bvhvec3 c1( minmax[sign1x], minmax[sign1y], minmax[sign1z] );
					bvhvec3 c2( minmax[sign2x], minmax[sign2y], minmax[sign2z] );
					bvhvec3 c3( minmax[sign3x], minmax[sign3y], minmax[sign3z] );
					if (tinybvh_dot( c0, plane0 ) > t0 || tinybvh_dot( c1, plane1 ) > t1 ||
						tinybvh_dot( c2, plane2 ) > t2 || tinybvh_dot( c3, plane3 ) > t3)
						visitRight = false;
					else
					{
						// 3. Last resort: update first and last, stay in node if first > last
						for (; rightFirst <= rightLast; rightFirst++)
						{
							const __m128 rD4 = *(__m128*) & packet[rightFirst].rD;
							const __m128 st1 = _mm_mul_ps( _mm_and_ps( minO4, mask4 ), rD4 );
							const __m128 st2 = _mm_mul_ps( _mm_and_ps( maxO4, mask4 ), rD4 );
							const __m128 vmax4 = _mm_max_ps( st1, st2 ), vmin4 = _mm_min_ps( st1, st2 );
							const float tmax1 = tinybvh_min( LANE( vmax4, 0 ), tinybvh_min( LANE( vmax4, 1 ), LANE( vmax4, 2 ) ) );
							const float tmin1 = tinybvh_max( LANE( vmin4, 0 ), tinybvh_max( LANE( vmin4, 1 ), LANE( vmin4, 2 ) ) );
							if (tmax1 >= tmin1 && tmin1 < packet[rightFirst].hit.t && tmax1 >= 0) { distRight = tmin1; break; }
						}
						for (; rightLast >= first; rightLast--)
						{
							const __m128 rD4 = *(__m128*) & packet[rightLast].rD;
							const __m128 st1 = _mm_mul_ps( _mm_and_ps( minO4, mask4 ), rD4 );
							const __m128 st2 = _mm_mul_ps( _mm_and_ps( maxO4, mask4 ), rD4 );
							const __m128 vmax4 = _mm_max_ps( st1, st2 ), vmin4 = _mm_min_ps( st1, st2 );
							const float tmax1 = tinybvh_min( LANE( vmax4, 0 ), tinybvh_min( LANE( vmax4, 1 ), LANE( vmax4, 2 ) ) );
							const float tmin1 = tinybvh_max( LANE( vmin4, 0 ), tinybvh_max( LANE( vmin4, 1 ), LANE( vmin4, 2 ) ) );
							if (tmax1 >= tmin1 && tmin1 < packet[rightLast].hit.t && tmax1 >= 0) break;
						}
						visitRight = rightLast >= rightFirst;
					}
				}
			}
			// process intersection result
			if (visitLeft && visitRight)
			{
				if (distLeft < distRight)
				{
					// push right, continue with left
					stack[stackPtr++] = node->leftFirst + 1;
					stack[stackPtr++] = (rightFirst << 8) + rightLast;
					node = left, first = leftFirst, last = leftLast;
				}
				else
				{
					// push left, continue with right
					stack[stackPtr++] = node->leftFirst;
					stack[stackPtr++] = (leftFirst << 8) + leftLast;
					node = right, first = rightFirst, last = rightLast;
				}
			}
			else if (visitLeft) // continue with left
				node = left, first = leftFirst, last = leftLast;
			else if (visitRight) // continue with right
				node = right, first = rightFirst, last = rightLast;
			else if (stackPtr == 0) break; else // pop
				last = stack[--stackPtr], node = bvhNode + stack[--stackPtr],
				first = last >> 8, last &= 255;
		}
	}
}

// Traverse the 'structure of arrays' BVH layout.
int32_t BVH_SoA::Intersect( Ray& ray ) const
{
	VALIDATE_RAY( ray );
	BVHNode* node = &bvhNode[0], * stack[64];
	const bvhvec4slice& verts = bvh.verts;
	const uint32_t* primIdx = bvh.primIdx;
	uint32_t stackPtr = 0;
	float cost = 0;
	const __m128 Ox4 = _mm_set1_ps( ray.O.x ), rDx4 = _mm_set1_ps( ray.rD.x );
	const __m128 Oy4 = _mm_set1_ps( ray.O.y ), rDy4 = _mm_set1_ps( ray.rD.y );
	const __m128 Oz4 = _mm_set1_ps( ray.O.z ), rDz4 = _mm_set1_ps( ray.rD.z );
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			if (indexedEnabled && bvh.vertIdx != 0) for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t pi = primIdx[node->firstTri + i];
				const uint32_t i0 = bvh.vertIdx[pi * 3], i1 = bvh.vertIdx[pi * 3 + 1], i2 = bvh.vertIdx[pi * 3 + 2];
				IntersectTri( ray, pi, verts, i0, i1, i2 );
			}
			else for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t pi = primIdx[node->firstTri + i];
				IntersectTri( ray, pi, verts, pi * 3, pi * 3 + 1, pi * 3 + 2 );
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		__m128 x4 = _mm_mul_ps( _mm_sub_ps( node->xxxx, Ox4 ), rDx4 );
		__m128 y4 = _mm_mul_ps( _mm_sub_ps( node->yyyy, Oy4 ), rDy4 );
		__m128 z4 = _mm_mul_ps( _mm_sub_ps( node->zzzz, Oz4 ), rDz4 );
		// transpose
		__m128 t0 = _mm_unpacklo_ps( x4, y4 ), t2 = _mm_unpacklo_ps( z4, z4 );
		__m128 t1 = _mm_unpackhi_ps( x4, y4 ), t3 = _mm_unpackhi_ps( z4, z4 );
		const __m128 xyzw1a = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		const __m128 xyzw2a = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 3, 2, 3, 2 ) );
		const __m128 xyzw1b = _mm_shuffle_ps( t1, t3, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		const __m128 xyzw2b = _mm_shuffle_ps( t1, t3, _MM_SHUFFLE( 3, 2, 3, 2 ) );
		// process
		const __m128 tmina4 = _mm_min_ps( xyzw1a, xyzw2a ), tmaxa4 = _mm_max_ps( xyzw1a, xyzw2a );
		const __m128 tminb4 = _mm_min_ps( xyzw1b, xyzw2b ), tmaxb4 = _mm_max_ps( xyzw1b, xyzw2b );
		// transpose back
		t0 = _mm_unpacklo_ps( tmina4, tmaxa4 ), t2 = _mm_unpacklo_ps( tminb4, tmaxb4 );
		t1 = _mm_unpackhi_ps( tmina4, tmaxa4 ), t3 = _mm_unpackhi_ps( tminb4, tmaxb4 );
		x4 = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		y4 = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 3, 2, 3, 2 ) );
		z4 = _mm_shuffle_ps( t1, t3, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		uint32_t lidx = node->left, ridx = node->right;
		const __m128 min4 = _mm_max_ps( _mm_max_ps( _mm_max_ps( x4, y4 ), z4 ), _mm_setzero_ps() );
		const __m128 max4 = _mm_min_ps( _mm_min_ps( _mm_min_ps( x4, y4 ), z4 ), _mm_set1_ps( ray.hit.t ) );
		const float tmina_0 = LANE( min4, 0 ), tmaxa_1 = LANE( max4, 1 );
		const float tminb_2 = LANE( min4, 2 ), tmaxb_3 = LANE( max4, 3 );
		float dist1 = tmaxa_1 >= tmina_0 ? tmina_0 : BVH_FAR;
		float dist2 = tmaxb_3 >= tminb_2 ? tminb_2 : BVH_FAR;
		if (dist1 > dist2)
		{
			const float t = dist1; dist1 = dist2; dist2 = t;
			const uint32_t i = lidx; lidx = ridx; ridx = i;
		}
		if (dist1 == BVH_FAR)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else
		{
			node = bvhNode + lidx;
			if (dist2 != BVH_FAR) stack[stackPtr++] = bvhNode + ridx;
		}
	}
	return (int32_t)cost;
}

// Find occlusions in the second alternative BVH layout (ALT_SOA).
bool BVH_SoA::IsOccluded( const Ray& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	const bvhvec4slice& verts = bvh.verts;
	const uint32_t* primIdx = bvh.primIdx;
	uint32_t stackPtr = 0;
	const __m128 Ox4 = _mm_set1_ps( ray.O.x ), rDx4 = _mm_set1_ps( ray.rD.x );
	const __m128 Oy4 = _mm_set1_ps( ray.O.y ), rDy4 = _mm_set1_ps( ray.rD.y );
	const __m128 Oz4 = _mm_set1_ps( ray.O.z ), rDz4 = _mm_set1_ps( ray.rD.z );
	while (1)
	{
		if (node->isLeaf())
		{
			if (indexedEnabled && bvh.vertIdx != 0) for (uint32_t i = 0; i < node->triCount; i++)
			{
				const uint32_t pi = primIdx[node->firstTri + i], vi0 = pi * 3;
				const uint32_t i0 = bvh.vertIdx[vi0], i1 = bvh.vertIdx[vi0 + 1], i2 = bvh.vertIdx[vi0 + 2];
				if (TriOccludes( ray, verts, pi, i0, i1, i2 )) return true;
			}
			else for (uint32_t i = 0; i < node->triCount; i++)
			{
				const uint32_t pi = primIdx[node->firstTri + i], vi0 = pi * 3;
				if (TriOccludes( ray, verts, pi, vi0, vi0 + 1, vi0 + 2 )) return true;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		__m128 x4 = _mm_mul_ps( _mm_sub_ps( node->xxxx, Ox4 ), rDx4 );
		__m128 y4 = _mm_mul_ps( _mm_sub_ps( node->yyyy, Oy4 ), rDy4 );
		__m128 z4 = _mm_mul_ps( _mm_sub_ps( node->zzzz, Oz4 ), rDz4 );
		// transpose
		__m128 t0 = _mm_unpacklo_ps( x4, y4 ), t2 = _mm_unpacklo_ps( z4, z4 );
		__m128 t1 = _mm_unpackhi_ps( x4, y4 ), t3 = _mm_unpackhi_ps( z4, z4 );
		__m128 xyzw1a = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		__m128 xyzw2a = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 3, 2, 3, 2 ) );
		__m128 xyzw1b = _mm_shuffle_ps( t1, t3, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		__m128 xyzw2b = _mm_shuffle_ps( t1, t3, _MM_SHUFFLE( 3, 2, 3, 2 ) );
		// process
		__m128 tmina4 = _mm_min_ps( xyzw1a, xyzw2a ), tmaxa4 = _mm_max_ps( xyzw1a, xyzw2a );
		__m128 tminb4 = _mm_min_ps( xyzw1b, xyzw2b ), tmaxb4 = _mm_max_ps( xyzw1b, xyzw2b );
		// transpose back
		t0 = _mm_unpacklo_ps( tmina4, tmaxa4 ), t2 = _mm_unpacklo_ps( tminb4, tmaxb4 );
		t1 = _mm_unpackhi_ps( tmina4, tmaxa4 ), t3 = _mm_unpackhi_ps( tminb4, tmaxb4 );
		x4 = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		y4 = _mm_shuffle_ps( t0, t2, _MM_SHUFFLE( 3, 2, 3, 2 ) );
		z4 = _mm_shuffle_ps( t1, t3, _MM_SHUFFLE( 1, 0, 1, 0 ) );
		uint32_t lidx = node->left, ridx = node->right;
		const __m128 min4 = _mm_max_ps( _mm_max_ps( _mm_max_ps( x4, y4 ), z4 ), _mm_setzero_ps() );
		const __m128 max4 = _mm_min_ps( _mm_min_ps( _mm_min_ps( x4, y4 ), z4 ), _mm_set1_ps( ray.hit.t ) );
		const float tmina_0 = LANE( min4, 0 ), tmaxa_1 = LANE( max4, 1 );
		const float tminb_2 = LANE( min4, 2 ), tmaxb_3 = LANE( max4, 3 );
		float dist1 = tmaxa_1 >= tmina_0 ? tmina_0 : BVH_FAR;
		float dist2 = tmaxb_3 >= tminb_2 ? tminb_2 : BVH_FAR;
		if (dist1 > dist2)
		{
			float t = dist1; dist1 = dist2; dist2 = t;
			uint32_t i = lidx; lidx = ridx; ridx = i;
		}
		if (dist1 == BVH_FAR)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else
		{
			node = bvhNode + lidx;
			if (dist2 != BVH_FAR) stack[stackPtr++] = bvhNode + ridx;
		}
	}
	return false;
}

// Intersect_CWBVH:
// Intersect a compressed 8-wide BVH with a ray. For debugging only, not efficient.
// Not technically limited to BVH_USEAVX, but __lzcnt and __popcnt will require
// exotic compiler flags (in combination with __builtin_ia32_lzcnt_u32), so... Since
// this is just here to test data before it goes to the GPU: MSVC-only for now.
#define STACK_POP() { ngroup = traversalStack[--stackPtr]; }
#define STACK_PUSH() { traversalStack[stackPtr++] = ngroup; }
inline uint32_t extract_byte( const uint32_t i, const uint32_t n ) { return (i >> (n * 8)) & 0xFF; }
inline uint32_t sign_extend_s8x4( const uint32_t i )
{
	// asm("prmt.b32 %0, %1, 0x0, 0x0000BA98;" : "=r"(v) : "r"(i)); // BA98: 1011`1010`1001`1000
	// with the given parameters, prmt will extend the sign to all bits in a byte.
	uint32_t b0 = (i & 0b10000000000000000000000000000000) ? 0xff000000 : 0;
	uint32_t b1 = (i & 0b00000000100000000000000000000000) ? 0x00ff0000 : 0;
	uint32_t b2 = (i & 0b00000000000000001000000000000000) ? 0x0000ff00 : 0;
	uint32_t b3 = (i & 0b00000000000000000000000010000000) ? 0x000000ff : 0;
	return b0 + b1 + b2 + b3; // probably can do better than this.
}
int32_t BVH8_CWBVH::Intersect( Ray& ray ) const
{
	bvhuint2 traversalStack[128];
	uint32_t hitAddr = 0, stackPtr = 0;
	bvhvec2 triangleuv( 0, 0 );
	const bvhvec4* blasNodes = bvh8Data, * blasTris = bvh8Tris;
	float tmin = 0, tmax = ray.hit.t;
	const uint32_t octinv = (7 - ((ray.D.x < 0 ? 4 : 0) | (ray.D.y < 0 ? 2 : 0) | (ray.D.z < 0 ? 1 : 0))) * 0x1010101;
	bvhuint2 ngroup = bvhuint2( 0, 0b10000000000000000000000000000000 ), tgroup = bvhuint2( 0 );
	do
	{
		if (ngroup.y > 0x00FFFFFF)
		{
			const uint32_t hits = ngroup.y, imask = ngroup.y;
			const uint32_t child_bit_index = __bfind( hits ), child_node_base_index = ngroup.x;
			ngroup.y &= ~(1 << child_bit_index);
			if (ngroup.y > 0x00FFFFFF) { STACK_PUSH( /* nodeGroup */ ); }
			{
				const uint32_t slot_index = (child_bit_index - 24) ^ (octinv & 255);
				const uint32_t relative_index = __popc( imask & ~(0xFFFFFFFF << slot_index) );
				const uint32_t child_node_index = child_node_base_index + relative_index;
				const bvhvec4 n0 = blasNodes[child_node_index * 5 + 0], n1 = blasNodes[child_node_index * 5 + 1];
				const bvhvec4 n2 = blasNodes[child_node_index * 5 + 2], n3 = blasNodes[child_node_index * 5 + 3];
				const bvhvec4 n4 = blasNodes[child_node_index * 5 + 4], p = n0;
				bvhint3 e;
				e.x = (int32_t) * ((int8_t*)&n0.w + 0), e.y = (int32_t) * ((int8_t*)&n0.w + 1), e.z = (int32_t) * ((int8_t*)&n0.w + 2);
				ngroup.x = as_uint( n1.x ), tgroup.x = as_uint( n1.y ), tgroup.y = 0;
				uint32_t hitmask = 0;
				const uint32_t vx = (e.x + 127) << 23u; const float adjusted_idirx = *(float*)&vx * ray.rD.x;
				const uint32_t vy = (e.y + 127) << 23u; const float adjusted_idiry = *(float*)&vy * ray.rD.y;
				const uint32_t vz = (e.z + 127) << 23u; const float adjusted_idirz = *(float*)&vz * ray.rD.z;
				const float origx = -(ray.O.x - p.x) * ray.rD.x;
				const float origy = -(ray.O.y - p.y) * ray.rD.y;
				const float origz = -(ray.O.z - p.z) * ray.rD.z;
				{	// First 4
					const uint32_t meta4 = *(uint32_t*)&n1.z, is_inner4 = (meta4 & (meta4 << 1)) & 0x10101010;
					const uint32_t inner_mask4 = sign_extend_s8x4( is_inner4 << 3 );
					const uint32_t bit_index4 = (meta4 ^ (octinv & inner_mask4)) & 0x1F1F1F1F;
					const uint32_t child_bits4 = (meta4 >> 5) & 0x07070707;
					uint32_t swizzledLox = (ray.rD.x < 0) ? *(uint32_t*)&n3.z : *(uint32_t*)&n2.x, swizzledHix = (ray.rD.x < 0) ? *(uint32_t*)&n2.x : *(uint32_t*)&n3.z;
					uint32_t swizzledLoy = (ray.rD.y < 0) ? *(uint32_t*)&n4.x : *(uint32_t*)&n2.z, swizzledHiy = (ray.rD.y < 0) ? *(uint32_t*)&n2.z : *(uint32_t*)&n4.x;
					uint32_t swizzledLoz = (ray.rD.z < 0) ? *(uint32_t*)&n4.z : *(uint32_t*)&n3.x, swizzledHiz = (ray.rD.z < 0) ? *(uint32_t*)&n3.x : *(uint32_t*)&n4.z;
					float tminx[4], tminy[4], tminz[4], tmaxx[4], tmaxy[4], tmaxz[4];
					tminx[0] = ((swizzledLox >> 0) & 0xFF) * adjusted_idirx + origx, tminx[1] = ((swizzledLox >> 8) & 0xFF) * adjusted_idirx + origx, tminx[2] = ((swizzledLox >> 16) & 0xFF) * adjusted_idirx + origx;
					tminx[3] = ((swizzledLox >> 24) & 0xFF) * adjusted_idirx + origx, tminy[0] = ((swizzledLoy >> 0) & 0xFF) * adjusted_idiry + origy, tminy[1] = ((swizzledLoy >> 8) & 0xFF) * adjusted_idiry + origy;
					tminy[2] = ((swizzledLoy >> 16) & 0xFF) * adjusted_idiry + origy, tminy[3] = ((swizzledLoy >> 24) & 0xFF) * adjusted_idiry + origy, tminz[0] = ((swizzledLoz >> 0) & 0xFF) * adjusted_idirz + origz;
					tminz[1] = ((swizzledLoz >> 8) & 0xFF) * adjusted_idirz + origz, tminz[2] = ((swizzledLoz >> 16) & 0xFF) * adjusted_idirz + origz, tminz[3] = ((swizzledLoz >> 24) & 0xFF) * adjusted_idirz + origz;
					tmaxx[0] = ((swizzledHix >> 0) & 0xFF) * adjusted_idirx + origx, tmaxx[1] = ((swizzledHix >> 8) & 0xFF) * adjusted_idirx + origx, tmaxx[2] = ((swizzledHix >> 16) & 0xFF) * adjusted_idirx + origx;
					tmaxx[3] = ((swizzledHix >> 24) & 0xFF) * adjusted_idirx + origx, tmaxy[0] = ((swizzledHiy >> 0) & 0xFF) * adjusted_idiry + origy, tmaxy[1] = ((swizzledHiy >> 8) & 0xFF) * adjusted_idiry + origy;
					tmaxy[2] = ((swizzledHiy >> 16) & 0xFF) * adjusted_idiry + origy, tmaxy[3] = ((swizzledHiy >> 24) & 0xFF) * adjusted_idiry + origy, tmaxz[0] = ((swizzledHiz >> 0) & 0xFF) * adjusted_idirz + origz;
					tmaxz[1] = ((swizzledHiz >> 8) & 0xFF) * adjusted_idirz + origz, tmaxz[2] = ((swizzledHiz >> 16) & 0xFF) * adjusted_idirz + origz, tmaxz[3] = ((swizzledHiz >> 24) & 0xFF) * adjusted_idirz + origz;
					for (int32_t i = 0; i < 4; i++)
					{
						// Use VMIN, VMAX to compute the slabs
						const float cmin = tinybvh_max( tinybvh_max( tinybvh_max( tminx[i], tminy[i] ), tminz[i] ), tmin );
						const float cmax = tinybvh_min( tinybvh_min( tinybvh_min( tmaxx[i], tmaxy[i] ), tmaxz[i] ), tmax );
						if (cmin <= cmax) hitmask |= extract_byte( child_bits4, i ) << extract_byte( bit_index4, i );
					}
				}
				{	// Second 4
					const uint32_t meta4 = *(uint32_t*)&n1.w, is_inner4 = (meta4 & (meta4 << 1)) & 0x10101010;
					const uint32_t inner_mask4 = sign_extend_s8x4( is_inner4 << 3 );
					const uint32_t bit_index4 = (meta4 ^ (octinv & inner_mask4)) & 0x1F1F1F1F;
					const uint32_t child_bits4 = (meta4 >> 5) & 0x07070707;
					uint32_t swizzledLox = (ray.rD.x < 0) ? *(uint32_t*)&n3.w : *(uint32_t*)&n2.y, swizzledHix = (ray.rD.x < 0) ? *(uint32_t*)&n2.y : *(uint32_t*)&n3.w;
					uint32_t swizzledLoy = (ray.rD.y < 0) ? *(uint32_t*)&n4.y : *(uint32_t*)&n2.w, swizzledHiy = (ray.rD.y < 0) ? *(uint32_t*)&n2.w : *(uint32_t*)&n4.y;
					uint32_t swizzledLoz = (ray.rD.z < 0) ? *(uint32_t*)&n4.w : *(uint32_t*)&n3.y, swizzledHiz = (ray.rD.z < 0) ? *(uint32_t*)&n3.y : *(uint32_t*)&n4.w;
					float tminx[4], tminy[4], tminz[4], tmaxx[4], tmaxy[4], tmaxz[4];
					tminx[0] = ((swizzledLox >> 0) & 0xFF) * adjusted_idirx + origx, tminx[1] = ((swizzledLox >> 8) & 0xFF) * adjusted_idirx + origx, tminx[2] = ((swizzledLox >> 16) & 0xFF) * adjusted_idirx + origx;
					tminx[3] = ((swizzledLox >> 24) & 0xFF) * adjusted_idirx + origx, tminy[0] = ((swizzledLoy >> 0) & 0xFF) * adjusted_idiry + origy, tminy[1] = ((swizzledLoy >> 8) & 0xFF) * adjusted_idiry + origy;
					tminy[2] = ((swizzledLoy >> 16) & 0xFF) * adjusted_idiry + origy, tminy[3] = ((swizzledLoy >> 24) & 0xFF) * adjusted_idiry + origy, tminz[0] = ((swizzledLoz >> 0) & 0xFF) * adjusted_idirz + origz;
					tminz[1] = ((swizzledLoz >> 8) & 0xFF) * adjusted_idirz + origz, tminz[2] = ((swizzledLoz >> 16) & 0xFF) * adjusted_idirz + origz, tminz[3] = ((swizzledLoz >> 24) & 0xFF) * adjusted_idirz + origz;
					tmaxx[0] = ((swizzledHix >> 0) & 0xFF) * adjusted_idirx + origx, tmaxx[1] = ((swizzledHix >> 8) & 0xFF) * adjusted_idirx + origx, tmaxx[2] = ((swizzledHix >> 16) & 0xFF) * adjusted_idirx + origx;
					tmaxx[3] = ((swizzledHix >> 24) & 0xFF) * adjusted_idirx + origx, tmaxy[0] = ((swizzledHiy >> 0) & 0xFF) * adjusted_idiry + origy, tmaxy[1] = ((swizzledHiy >> 8) & 0xFF) * adjusted_idiry + origy;
					tmaxy[2] = ((swizzledHiy >> 16) & 0xFF) * adjusted_idiry + origy, tmaxy[3] = ((swizzledHiy >> 24) & 0xFF) * adjusted_idiry + origy, tmaxz[0] = ((swizzledHiz >> 0) & 0xFF) * adjusted_idirz + origz;
					tmaxz[1] = ((swizzledHiz >> 8) & 0xFF) * adjusted_idirz + origz, tmaxz[2] = ((swizzledHiz >> 16) & 0xFF) * adjusted_idirz + origz, tmaxz[3] = ((swizzledHiz >> 24) & 0xFF) * adjusted_idirz + origz;
					for (int32_t i = 0; i < 4; i++)
					{
						const float cmin = tinybvh_max( tinybvh_max( tinybvh_max( tminx[i], tminy[i] ), tminz[i] ), tmin );
						const float cmax = tinybvh_min( tinybvh_min( tinybvh_min( tmaxx[i], tmaxy[i] ), tmaxz[i] ), tmax );
						if (cmin <= cmax) hitmask |= extract_byte( child_bits4, i ) << extract_byte( bit_index4, i );
					}
				}
				ngroup.y = (hitmask & 0xFF000000) | (as_uint( n0.w ) >> 24), tgroup.y = hitmask & 0x00FFFFFF;
			}
		}
		else tgroup = ngroup, ngroup = bvhuint2( 0 );
		while (tgroup.y != 0)
		{
			uint32_t triangleIndex = __bfind( tgroup.y );
			tgroup.y -= 1 << triangleIndex;
			int32_t triAddr = tgroup.x + triangleIndex * 3;
			const bvhvec3 e2 = bvhvec3( blasTris[triAddr + 0] ), e1 = bvhvec3( blasTris[triAddr + 1] );
			const bvhvec3 v0 = blasTris[triAddr + 2];
			MOLLER_TRUMBORE_TEST( tmax, continue );
			triangleuv = bvhvec2( u, v ), tmax = t;
			hitAddr = as_uint( blasTris[triAddr + 2].w );
		}
		if (ngroup.y > 0x00FFFFFF) continue;
		if (stackPtr > 0) { STACK_POP( /* nodeGroup */ ); }
		else
		{
			ray.hit.t = tmax;
			if (tmax < BVH_FAR) ray.hit.u = triangleuv.x, ray.hit.v = triangleuv.y, ray.hit.prim = hitAddr;
			break;
		}
	} while (true);
	return 0;
}

#ifdef BVH_USEAVX2

#define TO256(x) _mm256_cvtepu8_epi32( _mm_cvtsi64_si128( x ) )
ALIGNED( 64 ) static const __m256i idxLUT256[256] = {
	TO256( 506097522914230528 ), TO256( 1976943448883713 ), TO256( 1976943448883712 ), TO256( 7722435347202 ), TO256( 1976943448883456 ), TO256( 7722435347201 ), TO256(
	7722435347200 ), TO256( 30165763075 ), TO256( 1976943448817920 ), TO256( 7722435346945 ), TO256( 7722435346944 ), TO256( 30165763074 ), TO256( 7722435346688 ), TO256(
	30165763073 ), TO256( 30165763072 ), TO256( 117835012 ), TO256( 1976943432040704 ), TO256( 7722435281409 ), TO256( 7722435281408 ), TO256( 30165762818 ), TO256(
	7722435281152 ), TO256( 30165762817 ), TO256( 30165762816 ), TO256( 117835011 ), TO256( 7722435215616 ), TO256( 30165762561 ), TO256( 30165762560 ), TO256( 117835010 ), TO256(
	30165762304 ), TO256( 117835009 ), TO256( 117835008 ), TO256( 460293 ), TO256( 1976939137073408 ), TO256( 7722418504193 ), TO256( 7722418504192 ), TO256( 30165697282 ), TO256(
	7722418503936 ), TO256( 30165697281 ), TO256( 30165697280 ), TO256( 117834755 ), TO256( 7722418438400 ), TO256( 30165697025 ), TO256( 30165697024 ), TO256( 117834754 ), TO256(
	30165696768 ), TO256( 117834753 ), TO256( 117834752 ), TO256( 460292 ), TO256( 7722401661184 ), TO256( 30165631489 ), TO256( 30165631488 ), TO256( 117834498 ), TO256( 30165631232 ), TO256(
	117834497 ), TO256( 117834496 ), TO256( 460291 ), TO256( 30165565696 ), TO256( 117834241 ), TO256( 117834240 ), TO256( 460290 ), TO256( 117833984 ), TO256( 460289 ), TO256( 460288 ), TO256( 1798 ), TO256(
	1975839625445632 ), TO256( 7718123536897 ), TO256( 7718123536896 ), TO256( 30148920066 ), TO256( 7718123536640 ), TO256( 30148920065 ), TO256( 30148920064 ), TO256(
	117769219 ), TO256( 7718123471104 ), TO256( 30148919809 ), TO256( 30148919808 ), TO256( 117769218 ), TO256( 30148919552 ), TO256( 117769217 ), TO256( 117769216 ), TO256( 460036 ), TO256(
	7718106693888 ), TO256( 30148854273 ), TO256( 30148854272 ), TO256( 117768962 ), TO256( 30148854016 ), TO256( 117768961 ), TO256( 117768960 ), TO256( 460035 ), TO256( 30148788480 ), TO256(
	117768705 ), TO256( 117768704 ), TO256( 460034 ), TO256( 117768448 ), TO256( 460033 ), TO256( 460032 ), TO256( 1797 ), TO256( 7713811726592 ), TO256( 30132077057 ), TO256( 30132077056 ), TO256(
	117703426 ), TO256( 30132076800 ), TO256( 117703425 ), TO256( 117703424 ), TO256( 459779 ), TO256( 30132011264 ), TO256( 117703169 ), TO256( 117703168 ), TO256( 459778 ), TO256( 117702912 ), TO256(
	459777 ), TO256( 459776 ), TO256( 1796 ), TO256( 30115234048 ), TO256( 117637633 ), TO256( 117637632 ), TO256( 459522 ), TO256( 117637376 ), TO256( 459521 ), TO256( 459520 ), TO256( 1795 ), TO256( 117571840 ), TO256(
	459265 ), TO256( 459264 ), TO256( 1794 ), TO256( 459008 ), TO256( 1793 ), TO256( 1792 ), TO256( 7 ), TO256( 1694364648734976 ), TO256( 6618611909121 ), TO256( 6618611909120 ), TO256( 25853952770 ), TO256(
	6618611908864 ), TO256( 25853952769 ), TO256( 25853952768 ), TO256( 100992003 ), TO256( 6618611843328 ), TO256( 25853952513 ), TO256( 25853952512 ), TO256( 100992002 ), TO256(
	25853952256 ), TO256( 100992001 ), TO256( 100992000 ), TO256( 394500 ), TO256( 6618595066112 ), TO256( 25853886977 ), TO256( 25853886976 ), TO256( 100991746 ), TO256( 25853886720 ), TO256(
	100991745 ), TO256( 100991744 ), TO256( 394499 ), TO256( 25853821184 ), TO256( 100991489 ), TO256( 100991488 ), TO256( 394498 ), TO256( 100991232 ), TO256( 394497 ), TO256( 394496 ), TO256( 1541 ), TO256(
	6614300098816 ), TO256( 25837109761 ), TO256( 25837109760 ), TO256( 100926210 ), TO256( 25837109504 ), TO256( 100926209 ), TO256( 100926208 ), TO256( 394243 ), TO256( 25837043968 ), TO256(
	100925953 ), TO256( 100925952 ), TO256( 394242 ), TO256( 100925696 ), TO256( 394241 ), TO256( 394240 ), TO256( 1540 ), TO256( 25820266752 ), TO256( 100860417 ), TO256( 100860416 ), TO256( 393986 ), TO256(
	100860160 ), TO256( 393985 ), TO256( 393984 ), TO256( 1539 ), TO256( 100794624 ), TO256( 393729 ), TO256( 393728 ), TO256( 1538 ), TO256( 393472 ), TO256( 1537 ), TO256( 1536 ), TO256( 6 ), TO256( 5514788471040 ), TO256(
	21542142465 ), TO256( 21542142464 ), TO256( 84148994 ), TO256( 21542142208 ), TO256( 84148993 ), TO256( 84148992 ), TO256( 328707 ), TO256( 21542076672 ), TO256( 84148737 ), TO256(
	84148736 ), TO256( 328706 ), TO256( 84148480 ), TO256( 328705 ), TO256( 328704 ), TO256( 1284 ), TO256( 21525299456 ), TO256( 84083201 ), TO256( 84083200 ), TO256( 328450 ), TO256( 84082944 ), TO256( 328449 ), TO256(
	328448 ), TO256( 1283 ), TO256( 84017408 ), TO256( 328193 ), TO256( 328192 ), TO256( 1282 ), TO256( 327936 ), TO256( 1281 ), TO256( 1280 ), TO256( 5 ), TO256( 17230332160 ), TO256( 67305985 ), TO256( 67305984 ), TO256( 262914 ), TO256(
	67305728 ), TO256( 262913 ), TO256( 262912 ), TO256( 1027 ), TO256( 67240192 ), TO256( 262657 ), TO256( 262656 ), TO256( 1026 ), TO256( 262400 ), TO256( 1025 ), TO256( 1024 ), TO256( 4 ), TO256( 50462976 ), TO256( 197121 ), TO256(
	197120 ), TO256( 770 ), TO256( 196864 ), TO256( 769 ), TO256( 768 ), TO256( 3 ), TO256( 131328 ), TO256( 513 ), TO256( 512 ), TO256( 2 ), TO256( 256 ), TO256( 1 ), TO256( 0 ), TO256( 0 )
};

int32_t BVH8_CPU::Intersect( Ray& ray ) const
{
	VALIDATE_RAY( ray );
	const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
	if (!posX) goto negx;
	if (posY) { if (posZ) return Intersect<true, true, true>( ray ); else return Intersect<true, true, false>( ray ); }
	if (posZ) return Intersect<true, false, true>( ray ); else return Intersect<true, false, false>( ray );
negx:
	if (posY) { if (posZ) return Intersect<false, true, true>( ray ); else return Intersect<false, true, false>( ray ); }
	if (posZ) return Intersect<false, false, true>( ray ); else return Intersect<false, false, false>( ray );
}

template <bool posX, bool posY, bool posZ> int32_t BVH8_CPU::Intersect( Ray& ray ) const
{
	ALIGNED( 64 ) int32_t nodeStack[256];
	ALIGNED( 64 ) float distStack[256];
	const __m256 zero8 = _mm256_setzero_ps();
	__m256 t8 = _mm256_set1_ps( ray.hit.t );
	ALIGNED( 64 ) int32_t stackPtr = 0, nodeIdx = 0;
	constexpr int signShift = (posX ? 3 : 0) + (posY ? 6 : 0) + (posZ ? 12 : 0);
	const __m256 rx8 = _mm256_set1_ps( ray.O.x * ray.rD.x ), rdx8 = _mm256_set1_ps( ray.rD.x );
	const __m256 ry8 = _mm256_set1_ps( ray.O.y * ray.rD.y ), rdy8 = _mm256_set1_ps( ray.rD.y );
	const __m256 rz8 = _mm256_set1_ps( ray.O.z * ray.rD.z ), rdz8 = _mm256_set1_ps( ray.rD.z );
	const __m128 ox4 = _mm_set1_ps( ray.O.x ), oy4 = _mm_set1_ps( ray.O.y ), oz4 = _mm_set1_ps( ray.O.z );
	const __m128 dx4 = _mm_set1_ps( ray.D.x ), dy4 = _mm_set1_ps( ray.D.y ), dz4 = _mm_set1_ps( ray.D.z );
	const __m128 one4 = _mm_set1_ps( 1 ), inf4 = _mm_set1_ps( 1e34f );
#ifdef _DEBUG
	// sorry, not even this can be tolerated in this function. Only in debug.
	uint32_t steps = 0;
#endif
	while (1)
	{
	#ifdef _DEBUG
		steps++;
	#endif
		while (!(nodeIdx & LEAF_BIT))
		{
			const BVHNode* n = (BVHNode*)(bvh8Data + nodeIdx);
			const __m256 tx1 = _mm256_fmsub_ps( posX ? n->xmin8 : n->xmax8, rdx8, rx8 );
			const __m256 ty1 = _mm256_fmsub_ps( posY ? n->ymin8 : n->ymax8, rdy8, ry8 );
			const __m256 tz1 = _mm256_fmsub_ps( posZ ? n->zmin8 : n->zmax8, rdz8, rz8 );
			const __m256 tx2 = _mm256_fmsub_ps( posX ? n->xmax8 : n->xmin8, rdx8, rx8 );
			const __m256 ty2 = _mm256_fmsub_ps( posY ? n->ymax8 : n->ymin8, rdy8, ry8 );
			const __m256 tz2 = _mm256_fmsub_ps( posZ ? n->zmax8 : n->zmin8, rdz8, rz8 );
			__m256 tmin = _mm256_max_ps( _mm256_max_ps( _mm256_max_ps( zero8, tx1 ), ty1 ), tz1 );
			__m256 tmax = _mm256_min_ps( _mm256_min_ps( _mm256_min_ps( tx2, t8 ), ty2 ), tz2 );
			const __m256 mask8 = _mm256_cmp_ps( tmin, tmax, _CMP_LE_OQ );
			const uint32_t mask = _mm256_movemask_ps( mask8 );
			const uint32_t validNodes = __popc( mask );
			if (validNodes == 1)
			{
				const uint32_t lane = __bfind( mask );
				nodeIdx = ((uint32_t*)&n->child8)[lane];
			}
			else if (validNodes > 0)
			{
				const __m256i index = _mm256_srli_epi32( n->perm8, signShift );
				const uint32_t m = _mm256_movemask_ps( _mm256_permutevar8x32_ps( mask8, index ) );
				tmin = _mm256_permutevar8x32_ps( tmin, index );
				const __m256i cpi = idxLUT256[255 - m];
				const __m256i c8 = _mm256_permutevar8x32_epi32( n->child8, index );
				const __m256 dist8 = _mm256_permutevar8x32_ps( tmin, cpi );
				const __m256i child8 = _mm256_permutevar8x32_epi32( c8, cpi );
				_mm256_storeu_si256( (__m256i*)(nodeStack + stackPtr), child8 );
				_mm256_storeu_ps( (float*)(distStack + stackPtr), dist8 );
				stackPtr += validNodes - 1;
				nodeIdx = nodeStack[stackPtr];
			}
			else
			{
				if (!stackPtr) goto the_end;
				nodeIdx = nodeStack[--stackPtr];
			}
		}
		// Moeller-Trumbore ray/triangle intersection algorithm for four triangles
		uint32_t n;
		memcpy( &n, &nodeIdx, 4 );
		const BVHTri4Leaf* leaf = (BVHTri4Leaf*)(bvh8Data + (n & 0x1fffffff));
		const __m128 hx4 = _mm_fmsub_ps( dy4, leaf->e2z4, _mm_mul_ps( dz4, leaf->e2y4 ) );
		const __m128 hy4 = _mm_fmsub_ps( dz4, leaf->e2x4, _mm_mul_ps( dx4, leaf->e2z4 ) );
		const __m128 hz4 = _mm_fmsub_ps( dx4, leaf->e2y4, _mm_mul_ps( dy4, leaf->e2x4 ) );
		const __m128 sx4 = _mm_sub_ps( ox4, leaf->v0x4 ), sy4 = _mm_sub_ps( oy4, leaf->v0y4 );
		const __m128 sz4 = _mm_sub_ps( oz4, leaf->v0z4 );
		const __m128 det4 = _mm_fmadd_ps( leaf->e1z4, hz4, _mm_fmadd_ps( leaf->e1x4, hx4, _mm_mul_ps( leaf->e1y4, hy4 ) ) );
		const __m128 qz4 = _mm_fmsub_ps( sx4, leaf->e1y4, _mm_mul_ps( sy4, leaf->e1x4 ) );
		const __m128 qx4 = _mm_fmsub_ps( sy4, leaf->e1z4, _mm_mul_ps( sz4, leaf->e1y4 ) );
		const __m128 qy4 = _mm_fmsub_ps( sz4, leaf->e1x4, _mm_mul_ps( sx4, leaf->e1z4 ) );
		const __m128 inv_det4 = fastrcp4( det4 );
		const __m128 u4 = _mm_mul_ps( _mm_fmadd_ps( sz4, hz4, _mm_fmadd_ps( sx4, hx4, _mm_mul_ps( sy4, hy4 ) ) ), inv_det4 );
		const __m128 v4 = _mm_mul_ps( _mm_fmadd_ps( dz4, qz4, _mm_fmadd_ps( dx4, qx4, _mm_mul_ps( dy4, qy4 ) ) ), inv_det4 );
		const __m128 ta4 = _mm_mul_ps( _mm_fmadd_ps( leaf->e2z4, qz4, _mm_fmadd_ps( leaf->e2x4, qx4, _mm_mul_ps( leaf->e2y4, qy4 ) ) ), inv_det4 );
		const __m128 mask1 = _mm_cmpge_ps( u4, _mm_setzero_ps() ), mask2 = _mm_cmpge_ps( v4, _mm_setzero_ps() );
		const __m128 mask3 = _mm_cmple_ps( _mm_add_ps( u4, v4 ), one4 );
		const __m128 mask4 = _mm_cmpgt_ps( ta4, _mm_setzero_ps() );
		const __m128 mask5 = _mm_cmplt_ps( ta4, _mm256_extractf128_ps( t8, 0 ) );
		__m128 combined = _mm_and_ps( _mm_and_ps( _mm_and_ps( mask1, mask2 ), _mm_and_ps( mask3, mask4 ) ), mask5 );
		uint32_t imask = _mm_movemask_ps( combined );
		// evaluate opacity map, if present (SSE version).
		if (opmap) if (imask)
		{
			const __m128 fN4 = _mm_set1_ps( (float)opmapN );
			const __m128i row4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_add_ps( u4, v4 ), fN4 ) );
			const __m128i dia4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_sub_ps( one4, u4 ), fN4 ) );
			const __m128i v0 = _mm_mullo_epi32( row4, row4 );
			const __m128i v1 = _mm_cvttps_epi32( _mm_mul_ps( v4, fN4 ) );
			const __m128i v2 = _mm_sub_epi32( dia4, _mm_sub_epi32( _mm_set1_epi32( opmapN - 1 ), row4 ) );
			union { uint32_t idx[4]; __m128i idx4; };
			union { uint32_t omask[4]; __m128 omask4; };
			idx4 = _mm_add_epi32( _mm_add_epi32( v0, v1 ), v2 );
			// proceed with scalar code for gather operation - TODO: better approach?
			omask[0] = omask[1] = omask[2] = omask[3] = 0;
			for (int i = 0; i < 4; i++) if (imask & (1 << i))
			{
				uint32_t* om = opmap + leaf->primIdx[i] * ((opmapN * opmapN + 31) >> 5);
				if (om[idx[i] >> 5] & (1 << (idx[i] & 31))) omask[i] = 0xffffffff;
			}
			// combine
			combined = _mm_and_ps( combined, omask4 );
			imask = _mm_movemask_ps( combined );
		}
		if (imask)
		{
			// compute broadcasted horizontal minimum of dist4
			const __m128 dist4 = _mm_blendv_ps( inf4, ta4, combined );
			const __m128 a = _mm_min_ps( dist4, _mm_shuffle_ps( dist4, dist4, _MM_SHUFFLE( 2, 1, 0, 3 ) ) );
			const __m128 c = _mm_min_ps( a, _mm_shuffle_ps( a, a, _MM_SHUFFLE( 1, 0, 3, 2 ) ) );
			const uint32_t lane = __bfind( _mm_movemask_ps( _mm_cmpeq_ps( c, dist4 ) ) );
			// update hit record
			const __m128 _d4 = dist4;
			const float t = ((float*)&_d4)[lane];
			const __m128 _u4 = u4, _v4 = v4;
			ray.hit.t = t, ray.hit.u = ((float*)&_u4)[lane], ray.hit.v = ((float*)&_v4)[lane];
		#if INST_IDX_BITS == 32
			ray.hit.prim = leaf->primIdx[lane], ray.hit.inst = ray.instIdx;
		#else
			ray.hit.prim = leaf->primIdx[lane] + ray.instIdx;
		#endif
			t8 = _mm256_set1_ps( t );
			// compress stack
			uint32_t outStackPtr = 0;
			for (int32_t i = 0; i < stackPtr; i += 8)
			{
				__m256i node8 = _mm256_load_si256( (__m256i*)(nodeStack + i) );
				__m256 dist8 = _mm256_load_ps( (float*)(distStack + i) );
				const uint32_t mask = _mm256_movemask_ps( _mm256_cmp_ps( dist8, t8, _CMP_LE_OQ ) );
				const __m256i cpi = idxLUT256[255 - mask];
				dist8 = _mm256_permutevar8x32_ps( dist8, cpi ), node8 = _mm256_permutevar8x32_epi32( node8, cpi );
				_mm256_storeu_ps( (float*)(distStack + outStackPtr), dist8 );
				_mm256_storeu_si256( (__m256i*)(nodeStack + outStackPtr), node8 );
				const int32_t numItems = tinybvh_min( 8, stackPtr - i ), validMask = (1 << numItems) - 1;
				outStackPtr += __popc( mask & validMask );
			}
			stackPtr = outStackPtr;
		}
		if (!stackPtr) break;
		nodeIdx = nodeStack[--stackPtr];
	}
the_end:
#ifdef _DEBUG
	return steps;
#else
	return 0;
#endif
}

bool BVH8_CPU::IsOccluded( const Ray& ray ) const
{
	VALIDATE_RAY( ray );
	const bool posX = ray.D.x >= 0, posY = ray.D.y >= 0, posZ = ray.D.z >= 0;
	if (!posX) goto negx;
	if (posY) { if (posZ) return IsOccluded<true, true, true>( ray ); else return IsOccluded<true, true, false>( ray ); }
	if (posZ) return IsOccluded<true, false, true>( ray ); else return IsOccluded<true, false, false>( ray );
negx:
	if (posY) { if (posZ) return IsOccluded<false, true, true>( ray ); else return IsOccluded<false, true, false>( ray ); }
	if (posZ) return IsOccluded<false, false, true>( ray ); else return IsOccluded<false, false, false>( ray );
}

template <bool posX, bool posY, bool posZ> bool BVH8_CPU::IsOccluded( const Ray& ray ) const
{
	ALIGNED( 64 ) uint32_t nodeStack[256];
	ALIGNED( 64 ) int32_t stackPtr = 0, nodeIdx = 0;
	const __m256 t8 = _mm256_set1_ps( ray.hit.t );
	const __m256 rx8 = _mm256_set1_ps( ray.O.x * ray.rD.x ), rdx8 = _mm256_set1_ps( ray.rD.x );
	const __m256 ry8 = _mm256_set1_ps( ray.O.y * ray.rD.y ), rdy8 = _mm256_set1_ps( ray.rD.y );
	const __m256 rz8 = _mm256_set1_ps( ray.O.z * ray.rD.z ), rdz8 = _mm256_set1_ps( ray.rD.z );
	const __m128 ox4 = _mm_set1_ps( ray.O.x ), oy4 = _mm_set1_ps( ray.O.y ), oz4 = _mm_set1_ps( ray.O.z );
	const __m128 dx4 = _mm_set1_ps( ray.D.x ), dy4 = _mm_set1_ps( ray.D.y ), dz4 = _mm_set1_ps( ray.D.z );
	const __m128 t4 = _mm_set1_ps( ray.hit.t );
	const __m128 one4 = _mm_set1_ps( 1.0f ), zero4 = _mm_setzero_ps();
	while (1)
	{
		while (!(nodeIdx & LEAF_BIT))
		{
			const BVHNode* n = (BVHNode*)(bvh8Data + nodeIdx);
			const __m256i c8 = n->child8;
			const __m256 tx1 = _mm256_fmsub_ps( posX ? n->xmin8 : n->xmax8, rdx8, rx8 );
			const __m256 ty1 = _mm256_fmsub_ps( posY ? n->ymin8 : n->ymax8, rdy8, ry8 );
			const __m256 tz1 = _mm256_fmsub_ps( posZ ? n->zmin8 : n->zmax8, rdz8, rz8 );
			const __m256 tx2 = _mm256_fmsub_ps( posX ? n->xmax8 : n->xmin8, rdx8, rx8 );
			const __m256 ty2 = _mm256_fmsub_ps( posY ? n->ymax8 : n->ymin8, rdy8, ry8 );
			const __m256 tz2 = _mm256_fmsub_ps( posZ ? n->zmax8 : n->zmin8, rdz8, rz8 );
			const __m256 tmin = _mm256_max_ps( _mm256_max_ps( _mm256_max_ps( _mm256_setzero_ps(), tx1 ), ty1 ), tz1 );
			const __m256 tmax = _mm256_min_ps( _mm256_min_ps( _mm256_min_ps( tx2, t8 ), ty2 ), tz2 );
			const __m256 mask8 = _mm256_cmp_ps( tmin, tmax, _CMP_LE_OQ );
			const uint32_t mask = _mm256_movemask_ps( mask8 ); // _mm256_or_ps( _mm256_castsi256_ps( c8 ), mask8 ) );
			const uint32_t validNodes = __popc( mask );
			if (validNodes == 1)
			{
				const uint32_t lane = __bfind( mask );
				nodeIdx = ((uint32_t*)&n->child8)[lane];
			}
			else if (validNodes > 0)
			{
				const __m256i cpi = idxLUT256[255 - mask];
				const __m256i child8 = _mm256_permutevar8x32_epi32( c8, cpi );
				_mm256_storeu_si256( (__m256i*)(nodeStack + stackPtr), child8 );
				stackPtr += validNodes - 1;
				nodeIdx = nodeStack[stackPtr];
			}
			else
			{
				if (!stackPtr) return false;
				nodeIdx = nodeStack[--stackPtr];
			}
		}
		uint32_t n;
		memcpy( &n, &nodeIdx, 4 );
		// Moeller-Trumbore ray/triangle intersection algorithm for four triangles
		const BVHTri4Leaf* leaf = (BVHTri4Leaf*)(bvh8Data + (n & 0x1fffffff));
		const __m128 hx4 = _mm_fmsub_ps( dy4, leaf->e2z4, _mm_mul_ps( dz4, leaf->e2y4 ) );
		const __m128 hy4 = _mm_fmsub_ps( dz4, leaf->e2x4, _mm_mul_ps( dx4, leaf->e2z4 ) );
		const __m128 hz4 = _mm_fmsub_ps( dx4, leaf->e2y4, _mm_mul_ps( dy4, leaf->e2x4 ) );
		const __m128 sx4 = _mm_sub_ps( ox4, leaf->v0x4 );
		const __m128 sy4 = _mm_sub_ps( oy4, leaf->v0y4 );
		const __m128 sz4 = _mm_sub_ps( oz4, leaf->v0z4 );
		const __m128 det4 = _mm_fmadd_ps( leaf->e1z4, hz4, _mm_fmadd_ps( leaf->e1x4, hx4, _mm_mul_ps( leaf->e1y4, hy4 ) ) );
		const __m128 qz4 = _mm_fmsub_ps( sx4, leaf->e1y4, _mm_mul_ps( sy4, leaf->e1x4 ) );
		const __m128 qx4 = _mm_fmsub_ps( sy4, leaf->e1z4, _mm_mul_ps( sz4, leaf->e1y4 ) );
		const __m128 qy4 = _mm_fmsub_ps( sz4, leaf->e1x4, _mm_mul_ps( sx4, leaf->e1z4 ) );
		const __m128 inv_det4 = fastrcp4( det4 );
		const __m128 u4 = _mm_mul_ps( _mm_fmadd_ps( sz4, hz4, _mm_fmadd_ps( sx4, hx4, _mm_mul_ps( sy4, hy4 ) ) ), inv_det4 );
		const __m128 v4 = _mm_mul_ps( _mm_fmadd_ps( dz4, qz4, _mm_fmadd_ps( dx4, qx4, _mm_mul_ps( dy4, qy4 ) ) ), inv_det4 );
		const __m128 ta4 = _mm_mul_ps( _mm_fmadd_ps( leaf->e2z4, qz4, _mm_fmadd_ps( leaf->e2x4, qx4, _mm_mul_ps( leaf->e2y4, qy4 ) ) ), inv_det4 );
		const __m128 mask1 = _mm_cmpge_ps( u4, zero4 );
		const __m128 mask2 = _mm_cmpge_ps( v4, zero4 );
		const __m128 mask3 = _mm_cmple_ps( _mm_add_ps( u4, v4 ), one4 );
		const __m128 mask4 = _mm_cmplt_ps( ta4, t4 );
		const __m128 mask5 = _mm_cmpgt_ps( ta4, zero4 );
		__m128 combined = _mm_and_ps( _mm_and_ps( _mm_and_ps( mask1, mask2 ), _mm_and_ps( mask3, mask4 ) ), mask5 );
		if (_mm_movemask_ps( combined ))
		{
			if (!opmap) return true;
			// evaluate opacity map, SSE version.
			const __m128 fN4 = _mm_set1_ps( (float)opmapN );
			const __m128i row4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_add_ps( u4, v4 ), fN4 ) );
			const __m128i dia4 = _mm_cvttps_epi32( _mm_mul_ps( _mm_sub_ps( one4, u4 ), fN4 ) );
			const __m128i v0 = _mm_mullo_epi32( row4, row4 );
			const __m128i v1 = _mm_cvttps_epi32( _mm_mul_ps( v4, fN4 ) );
			const __m128i v2 = _mm_sub_epi32( dia4, _mm_sub_epi32( _mm_set1_epi32( opmapN - 1 ), row4 ) );
			union { uint32_t idx[4]; __m128i idx4; };
			idx4 = _mm_add_epi32( _mm_add_epi32( v0, v1 ), v2 );
			// proceed with scalar code for gather operation - TODO: better approach?
			const uint32_t imask = _mm_movemask_ps( combined );
			for (int i = 0; i < 4; i++) if (imask & (1 << i))
			{
				uint32_t* om = opmap + leaf->primIdx[i] * ((opmapN * opmapN + 31) >> 5);
				if (om[idx[i] >> 5] & (1 << (idx[i] & 31))) return true;
			}
		}
		// continue
		if (!stackPtr) return false;
		nodeIdx = nodeStack[--stackPtr];
	}
}

#endif // BVH_USEAVX2

#endif // BVH_USEAVX

// ============================================================================
//
//        I M P L E M E N T A T I O N  -  A R M / N E O N  C O D E
//
// ============================================================================

#ifdef BVH_USENEON

#define ILANE(a,b) vgetq_lane_s32(a, b)

inline float halfArea( const float32x4_t a /* a contains extent of aabb */ )
{
	ALIGNED( 64 ) float v[4];
	vst1q_f32( v, a );
	return v[0] * v[1] + v[1] * v[2] + v[2] * v[3];
}
inline float halfArea( const float32x4x2_t& a /* a contains aabb itself, with min.xyz negated */ )
{
	ALIGNED( 64 ) float c[8];
	vst1q_f32( c, a.val[0] );
	vst1q_f32( c + 4, a.val[1] );

	float ex = c[4] + c[0], ey = c[5] + c[1], ez = c[6] + c[2];
	return ex * ey + ey * ez + ez * ex;
}

#define PROCESS_PLANE( a, pos, ANLR, lN, rN, lb, rb ) if (lN * rN != 0) { \
    ANLR = halfArea( lb ) * (float)lN + halfArea( rb ) * (float)rN; \
    const float C = c_trav + c_int * rSAV * ANLR; if (C < splitCost) \
    splitCost = C, bestAxis = a, bestPos = pos, bestLBox = lb, bestRBox = rb; }

void BVH::BuildNEON( const bvhvec4* vertices, const uint32_t primCount )
{
	// build the BVH with a continuous array of bvhvec4 vertices:
	// in this case, the stride for the slice is 16 bytes.
	BuildNEON( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) } );
}
void BVH::BuildNEON( const bvhvec4slice& vertices )
{
	PrepareNEONBuild( vertices, 0, 0 );
	BuildNEON();
}
void BVH::BuildNEON( const bvhvec4* vertices, const uint32_t* indices, const uint32_t primCount )
{
	// build the BVH with an indexed array of bvhvec4 vertices.
	BuildNEON( bvhvec4slice{ vertices, primCount * 3, sizeof( bvhvec4 ) }, indices, primCount );
}
void BVH::BuildNEON( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t primCount )
{
	PrepareNEONBuild( vertices, indices, primCount );
	BuildNEON();
}
void BVH::PrepareNEONBuild( const bvhvec4slice& vertices, const uint32_t* indices, const uint32_t prims )
{
	BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareNEONBuild( .. ), primCount == 0." );
	BVH_FATAL_ERROR_IF( vertices.stride & 15, "BVH::PrepareNEONBuild( .. ), stride must be multiple of 16." );
	// some constants
	static const float32x4_t min4 = vdupq_n_f32( BVH_FAR ), max4 = vdupq_n_f32( -BVH_FAR );
	// reset node pool
	uint32_t primCount = prims > 0 ? prims : vertices.count / 3;
	const uint32_t spaceNeeded = primCount * 2;
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		primIdx = (uint32_t*)AlignedAlloc( primCount * sizeof( uint32_t ) );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		memset( &bvhNode[1], 0, 32 ); // avoid crash in refit.
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	else BVH_FATAL_ERROR_IF( !rebuildable, "BVH::BuildAVX( .. ), bvh not rebuildable." );
	verts = vertices; // note: we're not copying this data; don't delete.
	vertIdx = (uint32_t*)indices;
	triCount = idxCount = primCount;
	newNodePtr = 2;
	struct FragSSE { float32x4_t bmin4, bmax4; };
	FragSSE* frag4 = (FragSSE*)fragment;
	const float32x4_t* verts4 = (float32x4_t*)verts.data; // that's why it must be 16-byte aligned.
	// assign all triangles to the root node
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = triCount;
	// initialize fragments and update root bounds
	float32x4_t rootMin = min4, rootMax = max4;
	if (indices)
	{
		BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareAVXBuild( .. ), empty vertex slice." );
		BVH_FATAL_ERROR_IF( prims == 0, "BVH::PrepareAVXBuild( .. ), prims == 0." );
		// build the BVH over indexed triangles
		for (uint32_t i = 0; i < triCount; i++)
		{
			const uint32_t i0 = indices[i * 3], i1 = indices[i * 3 + 1], i2 = indices[i * 3 + 2];
			const float32x4_t v0 = verts4[i0], v1 = verts4[i1], v2 = verts4[i2];
			const float32x4_t t1 = vminq_f32( vminq_f32( v0, v1 ), v2 );
			const float32x4_t t2 = vmaxq_f32( vmaxq_f32( v0, v1 ), v2 );
			frag4[i].bmin4 = t1, frag4[i].bmax4 = t2, rootMin = vminq_f32( rootMin, t1 ), rootMax = vmaxq_f32( rootMax, t2 );
			primIdx[i] = i;
		}
	}
	else
	{
		BVH_FATAL_ERROR_IF( vertices.count == 0, "BVH::PrepareAVXBuild( .. ), empty vertex slice." );
		BVH_FATAL_ERROR_IF( prims != 0, "BVH::PrepareAVXBuild( .. ), indices == 0." );
		// build the BVH over a list of vertices: three per triangle
		for (uint32_t i = 0; i < triCount; i++)
		{
			const float32x4_t v0 = verts4[i * 3], v1 = verts4[i * 3 + 1], v2 = verts4[i * 3 + 2];
			const float32x4_t t1 = vminq_f32( vminq_f32( v0, v1 ), v2 );
			const float32x4_t t2 = vmaxq_f32( vmaxq_f32( v0, v1 ), v2 );
			frag4[i].bmin4 = t1, frag4[i].bmax4 = t2, rootMin = vminq_f32( rootMin, t1 ), rootMax = vmaxq_f32( rootMax, t2 );
			primIdx[i] = i;
		}
	}
	root.aabbMin = *(bvhvec3*)&rootMin, root.aabbMax = *(bvhvec3*)&rootMax;
	bvh_over_indices = indices != nullptr;
}

inline float32x4x2_t _mm256_set1_ps( float v )
{
	float32x4_t v4 = vdupq_n_f32( v );
	return float32x4x2_t{ v4, v4 };
}

inline float32x4x2_t _mm256_and_ps( float32x4x2_t v0, float32x4x2_t v1 )
{
	float32x4_t r0 = vreinterpretq_f32_s32( vandq_s32( vreinterpretq_s32_f32( v0.val[0] ), vreinterpretq_s32_f32( v1.val[0] ) ) );
	float32x4_t r1 = vreinterpretq_f32_s32( vandq_s32( vreinterpretq_s32_f32( v0.val[1] ), vreinterpretq_s32_f32( v1.val[1] ) ) );
	return float32x4x2_t{ r0, r1 };
}

inline float32x4x2_t _mm256_max_ps( float32x4x2_t v0, float32x4x2_t v1 )
{
	float32x4_t r0 = vmaxq_f32( v0.val[0], v1.val[0] );
	float32x4_t r1 = vmaxq_f32( v0.val[1], v1.val[1] );
	return float32x4x2_t{ r0, r1 };
}

inline float32x4x2_t _mm256_xor_ps( float32x4x2_t v0, float32x4x2_t v1 )
{
	float32x4_t r0 = vreinterpretq_f32_s32( veorq_s32( vreinterpretq_s32_f32( v0.val[0] ), vreinterpretq_s32_f32( v1.val[0] ) ) );
	float32x4_t r1 = vreinterpretq_f32_s32( veorq_s32( vreinterpretq_s32_f32( v0.val[1] ), vreinterpretq_s32_f32( v1.val[1] ) ) );
	return float32x4x2_t{ r0, r1 };
}

void BVH::BuildNEON()
{
#if 1
	// NEON code needs an overhaul.
	Build();
#else
	// aligned data
	ALIGNED( 64 ) float32x4x2_t binbox[3 * AVXBINS];            // 768 bytes
	ALIGNED( 64 ) float32x4x2_t binboxOrig[3 * AVXBINS];        // 768 bytes
	ALIGNED( 64 ) uint32_t count[3][AVXBINS]{};            // 96 bytes
	ALIGNED( 64 ) float32x4x2_t bestLBox, bestRBox;            // 64 bytes
	// some constants
	static const float32x4_t half4 = vdupq_n_f32( 0.5f );
	static const float32x4_t two4 = vdupq_n_f32( 2.0f ), min1 = vdupq_n_f32( -1 );
	static const int32x4_t maxbin4 = vdupq_n_s32( 7 );
	static const float32x4_t mask3 = vreinterpretq_f32_u32( vceqq_s32( SIMD_SETRVECS( 0, 0, 0, 1 ), vdupq_n_s32( 0 ) ) );
	static const float32x4_t binmul3 = vdupq_n_f32( AVXBINS * 0.49999f );
	static const float32x4x2_t max8 = _mm256_set1_ps( -BVH_FAR ), mask6 = { mask3, mask3 };
	static const float32x4_t signFlip4 = SIMD_SETRVEC( -0.0f, -0.0f, -0.0f, 0.0f );
	static const float32x4x2_t signFlip8 = { signFlip4, vdupq_n_f32( 0 ) };
	for (uint32_t i = 0; i < 3 * AVXBINS; i++) binboxOrig[i] = max8; // binbox initialization template
	struct FragSSE { float32x4_t bmin4, bmax4; };
	FragSSE* frag4 = (FragSSE*)fragment;
	float32x4x2_t* frag8 = (float32x4x2_t*)fragment;
	// subdivide recursively
	ALIGNED( 64 ) uint32_t task[128], taskCount = 0, nodeIdx = 0;
	BVHNode& root = bvhNode[0];
	const bvhvec3 minDim = (root.aabbMax - root.aabbMin) * 1e-7f;
	while (1)
	{
		while (1)
		{
			BVHNode& node = bvhNode[nodeIdx];
			float32x4_t* node4 = (float32x4_t*)&bvhNode[nodeIdx];
			// find optimal object split
			const float32x4_t d4 = vbslq_f32( vshrq_n_u32( vreinterpretq_u32_f32( mask3 ), 31 ), vsubq_f32( node4[1], node4[0] ), min1 );
			const float32x4_t nmin4 = vmulq_f32( vreinterpretq_f32_s32( vandq_s32( vreinterpretq_s32_f32( node4[0] ), vreinterpretq_s32_f32( mask3 ) ) ), two4 );
			const float32x4_t rpd4 = vreinterpretq_f32_s32( vandq_s32( vreinterpretq_s32_f32( vdivq_f32( binmul3, d4 ) ), vmvnq_s32( vreinterpretq_s32_u32( vceqq_f32( d4, vdupq_n_f32( 0 ) ) ) ) ) );
			// implementation of Section 4.1 of "Parallel Spatial Splits in Bounding Volume Hierarchies":
			// main loop operates on two fragments to minimize dependencies and maximize ILP.
			uint32_t fi = primIdx[node.leftFirst];
			memset( count, 0, sizeof( count ) );
			float32x4x2_t r0, r1, r2, f = _mm256_xor_ps( _mm256_and_ps( frag8[fi], mask6 ), signFlip8 );
			const float32x4_t fmin = vreinterpretq_f32_u32( vandq_u32( vreinterpretq_u32_f32( frag4[fi].bmin4 ), vreinterpretq_u32_f32( mask3 ) ) );
			const float32x4_t fmax = vreinterpretq_f32_u32( vandq_u32( vreinterpretq_u32_f32( frag4[fi].bmax4 ), vreinterpretq_u32_f32( mask3 ) ) );
			const int32x4_t bi4 = vcvtq_s32_f32( vrndnq_f32( vsubq_f32( vmulq_f32( vsubq_f32( vaddq_f32( frag4[fi].bmax4, frag4[fi].bmin4 ), nmin4 ), rpd4 ), half4 ) ) );
			const int32x4_t b4c = vmaxq_s32( vminq_s32( bi4, maxbin4 ), vdupq_n_s32( 0 ) ); // clamp needed after all
			memcpy( binbox, binboxOrig, sizeof( binbox ) );
			uint32_t i0 = ILANE( b4c, 0 ), i1 = ILANE( b4c, 1 ), i2 = ILANE( b4c, 2 ), * ti = primIdx + node.leftFirst + 1;
			for (uint32_t i = 0; i < node.triCount - 1; i++)
			{
				uint32_t fid = *ti++;
				//            #if defined __GNUC__ || _MSC_VER < 1920
				//                if (fid > triCount) fid = triCount - 1; // never happens but g++ *and* vs2017 need this to not crash...
				//            #endif
				const float32x4x2_t b0 = binbox[i0], b1 = binbox[AVXBINS + i1], b2 = binbox[2 * AVXBINS + i2];
				const float32x4_t frmin = vreinterpretq_f32_u32( vandq_u32( vreinterpretq_u32_f32( frag4[fid].bmin4 ), vreinterpretq_u32_f32( mask3 ) ) );
				const float32x4_t frmax = vreinterpretq_f32_u32( vandq_u32( vreinterpretq_u32_f32( frag4[fid].bmax4 ), vreinterpretq_u32_f32( mask3 ) ) );
				r0 = _mm256_max_ps( b0, f ), r1 = _mm256_max_ps( b1, f ), r2 = _mm256_max_ps( b2, f );
				const int32x4_t b4 = vcvtq_s32_f32( vrndnq_f32( (vsubq_f32( vmulq_f32( vsubq_f32( vaddq_f32( frmax, frmin ), nmin4 ), rpd4 ), half4 )) ) );
				const int32x4_t bc4 = vmaxq_s32( vminq_s32( b4, maxbin4 ), vdupq_n_s32( 0 ) ); // clamp needed after all
				f = _mm256_xor_ps( _mm256_and_ps( frag8[fid], mask6 ), signFlip8 ), count[0][i0]++, count[1][i1]++, count[2][i2]++;
				binbox[i0] = r0, i0 = ILANE( bc4, 0 );
				binbox[AVXBINS + i1] = r1, i1 = ILANE( bc4, 1 );
				binbox[2 * AVXBINS + i2] = r2, i2 = ILANE( bc4, 2 );
			}
			// final business for final fragment
			const float32x4x2_t b0 = binbox[i0], b1 = binbox[AVXBINS + i1], b2 = binbox[2 * AVXBINS + i2];
			count[0][i0]++, count[1][i1]++, count[2][i2]++;
			r0 = _mm256_max_ps( b0, f ), r1 = _mm256_max_ps( b1, f ), r2 = _mm256_max_ps( b2, f );
			binbox[i0] = r0, binbox[AVXBINS + i1] = r1, binbox[2 * AVXBINS + i2] = r2;
			// calculate per-split totals
			float splitCost = BVH_FAR, rSAV = 1.0f / node.SurfaceArea();
			uint32_t bestAxis = 0, bestPos = 0, n = newNodePtr, j = node.leftFirst + node.triCount, src = node.leftFirst;
			const float32x4x2_t* bb = binbox;
			for (int32_t a = 0; a < 3; a++, bb += AVXBINS) if ((node.aabbMax[a] - node.aabbMin[a]) > minDim[a])
			{
				// hardcoded bin processing for AVXBINS == 8
				assert( AVXBINS == 8 );
				const uint32_t lN0 = count[a][0], rN0 = count[a][7];
				const float32x4x2_t lb0 = bb[0], rb0 = bb[7];
				const uint32_t lN1 = lN0 + count[a][1], rN1 = rN0 + count[a][6], lN2 = lN1 + count[a][2];
				const uint32_t rN2 = rN1 + count[a][5], lN3 = lN2 + count[a][3], rN3 = rN2 + count[a][4];
				const float32x4x2_t lb1 = _mm256_max_ps( lb0, bb[1] ), rb1 = _mm256_max_ps( rb0, bb[6] );
				const float32x4x2_t lb2 = _mm256_max_ps( lb1, bb[2] ), rb2 = _mm256_max_ps( rb1, bb[5] );
				const float32x4x2_t lb3 = _mm256_max_ps( lb2, bb[3] ), rb3 = _mm256_max_ps( rb2, bb[4] );
				const uint32_t lN4 = lN3 + count[a][4], rN4 = rN3 + count[a][3], lN5 = lN4 + count[a][5];
				const uint32_t rN5 = rN4 + count[a][2], lN6 = lN5 + count[a][6], rN6 = rN5 + count[a][1];
				const float32x4x2_t lb4 = _mm256_max_ps( lb3, bb[4] ), rb4 = _mm256_max_ps( rb3, bb[3] );
				const float32x4x2_t lb5 = _mm256_max_ps( lb4, bb[5] ), rb5 = _mm256_max_ps( rb4, bb[2] );
				const float32x4x2_t lb6 = _mm256_max_ps( lb5, bb[6] ), rb6 = _mm256_max_ps( rb5, bb[1] );
				float ANLR3 = BVH_FAR; PROCESS_PLANE( a, 3, ANLR3, lN3, rN3, lb3, rb3 ); // most likely split
				float ANLR2 = BVH_FAR; PROCESS_PLANE( a, 2, ANLR2, lN2, rN4, lb2, rb4 );
				float ANLR4 = BVH_FAR; PROCESS_PLANE( a, 4, ANLR4, lN4, rN2, lb4, rb2 );
				float ANLR5 = BVH_FAR; PROCESS_PLANE( a, 5, ANLR5, lN5, rN1, lb5, rb1 );
				float ANLR1 = BVH_FAR; PROCESS_PLANE( a, 1, ANLR1, lN1, rN5, lb1, rb5 );
				float ANLR0 = BVH_FAR; PROCESS_PLANE( a, 0, ANLR0, lN0, rN6, lb0, rb6 );
				float ANLR6 = BVH_FAR; PROCESS_PLANE( a, 6, ANLR6, lN6, rN0, lb6, rb0 ); // least likely split
			}
			float noSplitCost = (float)node.triCount * c_int;
			if (splitCost >= noSplitCost) break; // not splitting is better.
			// in-place partition
			const float rpd = (*(bvhvec3*)&rpd4)[bestAxis], nmin = (*(bvhvec3*)&nmin4)[bestAxis];
			uint32_t t, fr = primIdx[src];
			for (uint32_t i = 0; i < node.triCount; i++)
			{
				const uint32_t bi = (uint32_t)((fragment[fr].bmax[bestAxis] + fragment[fr].bmin[bestAxis] - nmin) * rpd);
				if (bi <= bestPos) fr = primIdx[++src]; else t = fr, fr = primIdx[src] = primIdx[--j], primIdx[j] = t;
			}
			// create child nodes and recurse
			const uint32_t leftCount = src - node.leftFirst, rightCount = node.triCount - leftCount;
			if (leftCount == 0 || rightCount == 0 || taskCount == BVH_NUM_ELEMS( task )) break; // should not happen.
			*(float32x4x2_t*)&bvhNode[n] = _mm256_xor_ps( bestLBox, signFlip8 );
			bvhNode[n].leftFirst = node.leftFirst, bvhNode[n].triCount = leftCount;
			node.leftFirst = n++, node.triCount = 0, newNodePtr += 2;
			*(float32x4x2_t*)&bvhNode[n] = _mm256_xor_ps( bestRBox, signFlip8 );
			bvhNode[n].leftFirst = j, bvhNode[n].triCount = rightCount;
			task[taskCount++] = n, nodeIdx = n - 1;
		}
		// fetch subdivision task from stack
		if (taskCount == 0) break; else nodeIdx = task[--taskCount];
	}
	// all done.
	aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
	refittable = true; // not using spatial splits: can refit this BVH
	may_have_holes = false; // the AVX builder produces a continuous list of nodes
	usedNodes = newNodePtr;
#endif
}

// Traverse the second alternative BVH layout (ALT_SOA).
int32_t BVH_SoA::Intersect( Ray& ray ) const
{
	VALIDATE_RAY( ray );
	BVHNode* node = &bvhNode[0], * stack[64];
	const bvhvec4slice& verts = bvh.verts;
	const uint32_t* primIdx = bvh.primIdx;
	uint32_t stackPtr = 0;
	float cost = 0;
	const float32x4_t Ox4 = vdupq_n_f32( ray.O.x ), rDx4 = vdupq_n_f32( ray.rD.x );
	const float32x4_t Oy4 = vdupq_n_f32( ray.O.y ), rDy4 = vdupq_n_f32( ray.rD.y );
	const float32x4_t Oz4 = vdupq_n_f32( ray.O.z ), rDz4 = vdupq_n_f32( ray.rD.z );
	// const float32x4_t inf4 = vdupq_n_f32( BVH_FAR );
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint32_t tidx = primIdx[node->firstTri + i], vertIdx = tidx * 3;
				const bvhvec3 v0 = verts[vertIdx];
				const bvhvec3 e1 = bvhvec3( verts[vertIdx + 1] ) - v0;
				const bvhvec3 e2 = bvhvec3( verts[vertIdx + 2] ) - v0;
				MOLLER_TRUMBORE_TEST( ray.hit.t, continue );
				ray.hit.t = t, ray.hit.u = u, ray.hit.v = v, ray.hit.prim = tidx;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		float32x4_t x4 = vmulq_f32( vsubq_f32( node->xxxx, Ox4 ), rDx4 );
		float32x4_t y4 = vmulq_f32( vsubq_f32( node->yyyy, Oy4 ), rDy4 );
		float32x4_t z4 = vmulq_f32( vsubq_f32( node->zzzz, Oz4 ), rDz4 );
		// transpose
		float32x4_t t0 = vzip1q_f32( x4, y4 ), t2 = vzip1q_f32( z4, z4 );
		float32x4_t t1 = vzip2q_f32( x4, y4 ), t3 = vzip2q_f32( z4, z4 );
		float32x4_t xyzw1a = vcombine_f32( vget_low_f32( t0 ), vget_low_f32( t2 ) );
		float32x4_t xyzw2a = vcombine_f32( vget_high_f32( t0 ), vget_high_f32( t2 ) );
		float32x4_t xyzw1b = vcombine_f32( vget_low_f32( t1 ), vget_low_f32( t3 ) );
		float32x4_t xyzw2b = vcombine_f32( vget_high_f32( t1 ), vget_high_f32( t3 ) );
		// process
		float32x4_t tmina4 = vminq_f32( xyzw1a, xyzw2a ), tmaxa4 = vmaxq_f32( xyzw1a, xyzw2a );
		float32x4_t tminb4 = vminq_f32( xyzw1b, xyzw2b ), tmaxb4 = vmaxq_f32( xyzw1b, xyzw2b );
		// transpose back
		t0 = vzip1q_f32( tmina4, tmaxa4 ), t2 = vzip1q_f32( tminb4, tmaxb4 );
		t1 = vzip2q_f32( tmina4, tmaxa4 ), t3 = vzip2q_f32( tminb4, tmaxb4 );
		x4 = vcombine_f32( vget_low_f32( t0 ), vget_low_f32( t2 ) );
		y4 = vcombine_f32( vget_high_f32( t0 ), vget_high_f32( t2 ) );
		z4 = vcombine_f32( vget_low_f32( t1 ), vget_low_f32( t3 ) );
		uint32_t lidx = node->left, ridx = node->right;
		const float32x4_t min4 = vmaxq_f32( vmaxq_f32( vmaxq_f32( x4, y4 ), z4 ), vdupq_n_f32( 0 ) );
		const float32x4_t max4 = vminq_f32( vminq_f32( vminq_f32( x4, y4 ), z4 ), vdupq_n_f32( ray.hit.t ) );
		const float tmina_0 = vgetq_lane_f32( min4, 0 ), tmaxa_1 = vgetq_lane_f32( max4, 1 );
		const float tminb_2 = vgetq_lane_f32( min4, 2 ), tmaxb_3 = vgetq_lane_f32( max4, 3 );
		float dist1 = tmaxa_1 >= tmina_0 ? tmina_0 : BVH_FAR;
		float dist2 = tmaxb_3 >= tminb_2 ? tminb_2 : BVH_FAR;
		if (dist1 > dist2)
		{
			float t = dist1; dist1 = dist2; dist2 = t;
			uint32_t i = lidx; lidx = ridx; ridx = i;
		}
		if (dist1 == BVH_FAR)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else
		{
			node = bvhNode + lidx;
			if (dist2 != BVH_FAR) stack[stackPtr++] = bvhNode + ridx;
		}
	}
	return (int32_t)cost;
}

bool BVH_SoA::IsOccluded( const Ray& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	const bvhvec4slice& verts = bvh.verts;
	const uint32_t* primIdx = bvh.primIdx;
	uint32_t stackPtr = 0;
	const float32x4_t Ox4 = vdupq_n_f32( ray.O.x ), rDx4 = vdupq_n_f32( ray.rD.x );
	const float32x4_t Oy4 = vdupq_n_f32( ray.O.y ), rDy4 = vdupq_n_f32( ray.rD.y );
	const float32x4_t Oz4 = vdupq_n_f32( ray.O.z ), rDz4 = vdupq_n_f32( ray.rD.z );
	while (1)
	{
		if (node->isLeaf())
		{
			for (uint32_t i = 0; i < node->triCount; i++)
			{
				const uint32_t tidx = primIdx[node->firstTri + i], vertIdx = tidx * 3;
				const bvhvec3 v0 = verts[vertIdx];
				const bvhvec3 e1 = bvhvec3( verts[vertIdx + 1] ) - v0;
				const bvhvec3 e2 = bvhvec3( verts[vertIdx + 2] ) - v0;
				MOLLER_TRUMBORE_TEST( ray.hit.t, continue );
				return true;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		float32x4_t x4 = vmulq_f32( vsubq_f32( node->xxxx, Ox4 ), rDx4 );
		float32x4_t y4 = vmulq_f32( vsubq_f32( node->yyyy, Oy4 ), rDy4 );
		float32x4_t z4 = vmulq_f32( vsubq_f32( node->zzzz, Oz4 ), rDz4 );
		// transpose
		float32x4_t t0 = vzip1q_f32( x4, y4 ), t2 = vzip1q_f32( z4, z4 );
		float32x4_t t1 = vzip2q_f32( x4, y4 ), t3 = vzip2q_f32( z4, z4 );
		float32x4_t xyzw1a = vcombine_f32( vget_low_f32( t0 ), vget_low_f32( t2 ) );
		float32x4_t xyzw2a = vcombine_f32( vget_high_f32( t0 ), vget_high_f32( t2 ) );
		float32x4_t xyzw1b = vcombine_f32( vget_low_f32( t1 ), vget_low_f32( t3 ) );
		float32x4_t xyzw2b = vcombine_f32( vget_high_f32( t1 ), vget_high_f32( t3 ) );
		// process
		float32x4_t tmina4 = vminq_f32( xyzw1a, xyzw2a ), tmaxa4 = vmaxq_f32( xyzw1a, xyzw2a );
		float32x4_t tminb4 = vminq_f32( xyzw1b, xyzw2b ), tmaxb4 = vmaxq_f32( xyzw1b, xyzw2b );
		// transpose back
		t0 = vzip1q_f32( tmina4, tmaxa4 ), t2 = vzip1q_f32( tminb4, tmaxb4 );
		t1 = vzip2q_f32( tmina4, tmaxa4 ), t3 = vzip2q_f32( tminb4, tmaxb4 );
		x4 = vcombine_f32( vget_low_f32( t0 ), vget_low_f32( t2 ) );
		y4 = vcombine_f32( vget_high_f32( t0 ), vget_high_f32( t2 ) );
		z4 = vcombine_f32( vget_low_f32( t1 ), vget_low_f32( t3 ) );
		uint32_t lidx = node->left, ridx = node->right;
		const float32x4_t min4 = vmaxq_f32( vmaxq_f32( vmaxq_f32( x4, y4 ), z4 ), vdupq_n_f32( 0 ) );
		const float32x4_t max4 = vminq_f32( vminq_f32( vminq_f32( x4, y4 ), z4 ), vdupq_n_f32( ray.hit.t ) );
		const float tmina_0 = vgetq_lane_f32( min4, 0 ), tmaxa_1 = vgetq_lane_f32( max4, 1 );
		const float tminb_2 = vgetq_lane_f32( min4, 2 ), tmaxb_3 = vgetq_lane_f32( max4, 3 );
		float dist1 = tmaxa_1 >= tmina_0 ? tmina_0 : BVH_FAR;
		float dist2 = tmaxb_3 >= tminb_2 ? tminb_2 : BVH_FAR;
		if (dist1 > dist2)
		{
			float t = dist1; dist1 = dist2; dist2 = t;
			uint32_t i = lidx; lidx = ridx; ridx = i;
		}
		if (dist1 == BVH_FAR)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else
		{
			node = bvhNode + lidx;
			if (dist2 != BVH_FAR) stack[stackPtr++] = bvhNode + ridx;
		}
	}
	return false;
}

#endif // BVH_USENEON

// ============================================================================
//
//        D O U B L E   P R E C I S I O N   S U P P O R T
//
// ============================================================================

#ifdef DOUBLE_PRECISION_SUPPORT

BVH_Double::BVH_Double( BVH_Double&& other )
{
	*this = other;
	other.fragment = 0;
	other.bvhNode = 0;
	other.primIdx = 0;
}

// Destructor
BVH_Double::~BVH_Double()
{
	AlignedFree( fragment );
	AlignedFree( bvhNode );
	AlignedFree( primIdx );
}

void BVH_Double::Build( void (*customGetAABB)(const uint64_t, bvhdbl3&, bvhdbl3&), const uint64_t primCount )
{
	BVH_FATAL_ERROR_IF( primCount == 0, "BVH_Double::Build( void (*customGetAABB)( .. ), instCount ), instCount == 0." );
	triCount = idxCount = primCount;
	const uint64_t spaceNeeded = primCount * 2; // upper limit
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		primIdx = (uint64_t*)AlignedAlloc( primCount * sizeof( uint64_t ) );
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	// copy relevant data from instance array
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = primCount, root.aabbMin = bvhvec3( BVH_FAR ), root.aabbMax = bvhvec3( -BVH_FAR );
	for (uint32_t i = 0; i < primCount; i++)
	{
		customGetAABB( i, fragment[i].bmin, fragment[i].bmax );
		root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin ), fragment[i].primIdx = i;
		root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
	}
	// start build
	newNodePtr = 1;
	Build();
}

void BVH_Double::Build( BLASInstanceEx* bvhs, const uint64_t instCount, BVH_Double** blasses, const uint64_t bCount )
{
	BVH_FATAL_ERROR_IF( instCount == 0, "BVH_Double::Build( BLASInstanceEx*, instCount ), instCount == 0." );
	triCount = idxCount = instCount;
	const uint64_t spaceNeeded = instCount * 2; // upper limit
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		primIdx = (uint64_t*)AlignedAlloc( instCount * sizeof( uint64_t ) );
		fragment = (Fragment*)AlignedAlloc( instCount * sizeof( Fragment ) );
	}
	instList = (BLASInstanceEx*)bvhs;
	blasList = blasses;
	blasCount = bCount;
	// copy relevant data from instance array
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = instCount, root.aabbMin = bvhdbl3( BVH_DBL_FAR ), root.aabbMax = bvhdbl3( -BVH_DBL_FAR );
	for (uint64_t i = 0; i < instCount; i++)
	{
		if (blasList) // if a null pointer is passed, we'll assume the BLASInstances have been updated elsewhere.
		{
			uint64_t blasIdx = instList[i].blasIdx;
			BVH_Double* blas = blasList[blasIdx];
			instList[i].Update( blas );
		}
		fragment[i].bmin = instList[i].aabbMin, fragment[i].primIdx = i, fragment[i].bmax = instList[i].aabbMax;
		root.aabbMin = tinybvh_min( root.aabbMin, instList[i].aabbMin );
		root.aabbMax = tinybvh_max( root.aabbMax, instList[i].aabbMax ), primIdx[i] = i;
	}
	// start build
	newNodePtr = 1;
	Build();
}

void BVH_Double::Build( const bvhdbl3* vertices, const uint64_t primCount )
{
	PrepareBuild( vertices, primCount );
	Build();
}

void BVH_Double::PrepareBuild( const bvhdbl3* vertices, const uint64_t primCount )
{
	BVH_FATAL_ERROR_IF( primCount == 0, "BVH_Double::PrepareBuild( .. ), primCount == 0." );
	const uint64_t spaceNeeded = primCount * 2; // upper limit
	// allocate memory on first build
	if (allocatedNodes < spaceNeeded)
	{
		AlignedFree( bvhNode );
		AlignedFree( primIdx );
		AlignedFree( fragment );
		bvhNode = (BVHNode*)AlignedAlloc( spaceNeeded * sizeof( BVHNode ) );
		allocatedNodes = spaceNeeded;
		primIdx = (uint64_t*)AlignedAlloc( primCount * sizeof( uint64_t ) );
		fragment = (Fragment*)AlignedAlloc( primCount * sizeof( Fragment ) );
	}
	verts = (bvhdbl3*)vertices; // note: we're not copying this data; don't delete.
	idxCount = triCount = primCount;
	// prepare fragments
	BVHNode& root = bvhNode[0];
	root.leftFirst = 0, root.triCount = triCount, root.aabbMin = bvhdbl3( BVH_DBL_FAR ), root.aabbMax = bvhdbl3( -BVH_DBL_FAR );
	for (uint32_t i = 0; i < triCount; i++)
	{
		const bvhdbl3 v0 = verts[i * 3], v1 = verts[i * 3 + 1], v2 = verts[i * 3 + 2];
		fragment[i].bmin = tinybvh_min( tinybvh_min( v0, v1 ), v2 );
		fragment[i].bmax = tinybvh_max( tinybvh_max( v0, v1 ), v2 );
		root.aabbMin = tinybvh_min( root.aabbMin, fragment[i].bmin );
		root.aabbMax = tinybvh_max( root.aabbMax, fragment[i].bmax ), primIdx[i] = i;
	}
	// reset node pool
	newNodePtr = 1;
	// all set; actual build happens in BVH_Double::Build.
}

#ifndef MT_USE_JOBSYSTEM
void BuildDouble_( uint64_t nodeIdx, uint32_t depth, BVH_Double* bvh ) { bvh->Build( nodeIdx, depth ); }
#endif
void BVH_Double::Build( uint64_t nodeIdx, uint32_t depth )
{
	// avoid threaded building for small meshes: not efficient; build multiple in parallel instead.
	if (depth == 0)
	{
	#ifdef NO_THREADED_BUILDS
		threadedBuild = false;
	#else
		if (triCount < MT_BUILD_THRESHOLD) threadedBuild = false; else
		{
		#ifdef MT_USE_JOBSYSTEM
			if (!globalSubtreeJobs) globalSubtreeJobs = new JobSystem();
			subtreeJobs = globalSubtreeJobs;
		#endif
			atomicNewNodePtr = new std::atomic<uint64_t>( newNodePtr );
		}
	#endif
	}
	// subdivide root node recursively
	uint64_t task[256], taskCount = 0;
	BVHNode& root = bvhNode[0];
	bvhdbl3 minDim = (root.aabbMax - root.aabbMin) * 1e-40;
	bvhdbl3 bestLMin( 0 ), bestLMax( 0 ), bestRMin( 0 ), bestRMax( 0 );
	while (1)
	{
		while (1)
		{
			BVHNode& node = bvhNode[nodeIdx];
			const double SA = node.SurfaceArea();
			// find optimal object split
			bvhdbl3 binMin[3][BVHBINS], binMax[3][BVHBINS];
			for (uint32_t a = 0; a < 3; a++) for (uint32_t i = 0; i < BVHBINS; i++)
				binMin[a][i] = bvhdbl3( BVH_DBL_FAR ), binMax[a][i] = bvhdbl3( -BVH_DBL_FAR );
			uint32_t count[3][BVHBINS] = { 0 };
			const bvhdbl3 extent = node.aabbMax - node.aabbMin;
			const bvhdbl3 nmin3 = node.aabbMin, rpd3 = bvhdbl3(
				extent.x > minDim.x ? (BVHBINS / extent.x) : 0,
				extent.y > minDim.y ? (BVHBINS / extent.y) : 0,
				extent.z > minDim.z ? (BVHBINS / extent.z) : 0
			);
			for (uint32_t i = 0; i < node.triCount; i++) // process all tris for x,y and z at once
			{
				const uint64_t fi = primIdx[node.leftFirst + i];
				const bvhdbl3 fbi = ((fragment[fi].bmin + fragment[fi].bmax) * 0.5 - nmin3) * rpd3;
				bvhint3 bi( (int32_t)fbi.x, (int32_t)fbi.y, (int32_t)fbi.z );
				bi.x = tinybvh_clamp( bi.x, 0, BVHBINS - 1 );
				bi.y = tinybvh_clamp( bi.y, 0, BVHBINS - 1 );
				bi.z = tinybvh_clamp( bi.z, 0, BVHBINS - 1 );
				binMin[0][bi.x] = tinybvh_min( binMin[0][bi.x], fragment[fi].bmin );
				binMax[0][bi.x] = tinybvh_max( binMax[0][bi.x], fragment[fi].bmax ), count[0][bi.x]++;
				binMin[1][bi.y] = tinybvh_min( binMin[1][bi.y], fragment[fi].bmin );
				binMax[1][bi.y] = tinybvh_max( binMax[1][bi.y], fragment[fi].bmax ), count[1][bi.y]++;
				binMin[2][bi.z] = tinybvh_min( binMin[2][bi.z], fragment[fi].bmin );
				binMax[2][bi.z] = tinybvh_max( binMax[2][bi.z], fragment[fi].bmax ), count[2][bi.z]++;
			}
			// calculate per-split totals
			double splitCost = BVH_DBL_FAR;
			uint32_t bestAxis = 0, bestPos = 0;
			for (int32_t a = 0; a < 3; a++) if (extent[a] > minDim[a])
			{
				bvhdbl3 lBMin[BVHBINS - 1], rBMin[BVHBINS - 1], l1( BVH_DBL_FAR ), l2( -BVH_DBL_FAR );
				bvhdbl3 lBMax[BVHBINS - 1], rBMax[BVHBINS - 1], r1( BVH_DBL_FAR ), r2( -BVH_DBL_FAR );
				double ANL[BVHBINS - 1], ANR[BVHBINS - 1];
				for (uint32_t lN = 0, rN = 0, i = 0; i < BVHBINS - 1; i++)
				{
					lBMin[i] = l1 = tinybvh_min( l1, binMin[a][i] );
					rBMin[BVHBINS - 2 - i] = r1 = tinybvh_min( r1, binMin[a][BVHBINS - 1 - i] );
					lBMax[i] = l2 = tinybvh_max( l2, binMax[a][i] );
					rBMax[BVHBINS - 2 - i] = r2 = tinybvh_max( r2, binMax[a][BVHBINS - 1 - i] );
					lN += count[a][i], rN += count[a][BVHBINS - 1 - i];
					ANL[i] = lN == 0 ? BVH_DBL_FAR : (tinybvh_halfarea( l2 - l1 ) * (double)lN);
					ANR[BVHBINS - 2 - i] = rN == 0 ? BVH_DBL_FAR : (tinybvh_halfarea( r2 - r1 ) * (double)rN);
				}
				// evaluate bin totals to find best position for object split
				for (uint32_t i = 0; i < BVHBINS - 1; i++)
				{
					const double C = ANL[i] + ANR[i];
					if (C < splitCost)
					{
						splitCost = C, bestAxis = a, bestPos = i;
						bestLMin = lBMin[i], bestRMin = rBMin[i], bestLMax = lBMax[i], bestRMax = rBMax[i];
					}
				}
			}
			splitCost = c_trav + c_int * splitCost / SA;
			const double noSplitCost = (double)node.triCount * c_int;
			if (splitCost >= noSplitCost) break; // not splitting is better.
			// in-place partition
			uint64_t j = node.leftFirst + node.triCount, src = node.leftFirst;
			const double rpd = rpd3[bestAxis], nmin = nmin3[bestAxis];
			for (uint64_t i = 0; i < node.triCount; i++)
			{
				const uint64_t fi = primIdx[src];
				int32_t bi = (uint32_t)(((fragment[fi].bmin[bestAxis] + fragment[fi].bmax[bestAxis]) * 0.5 - nmin) * rpd);
				bi = tinybvh_clamp( bi, 0, BVHBINS - 1 );
				if ((uint32_t)bi <= bestPos) src++; else tinybvh_swap( primIdx[src], primIdx[--j] );
			}
			// create child nodes
			uint64_t leftCount = src - node.leftFirst, rightCount = node.triCount - leftCount;
			if (leftCount == 0 || rightCount == 0 || taskCount == BVH_NUM_ELEMS( task )) break; // should not happen.
			uint64_t n;
			if (threadedBuild) n = atomicNewNodePtr->fetch_add( 2 ); else n = newNodePtr, newNodePtr += 2;
			bvhNode[n].aabbMin = bestLMin, bvhNode[n].aabbMax = bestLMax;
			bvhNode[n].leftFirst = node.leftFirst, bvhNode[n].triCount = leftCount;
			bvhNode[n + 1].aabbMin = bestRMin, bvhNode[n + 1].aabbMax = bestRMax;
			bvhNode[n + 1].leftFirst = j, bvhNode[n + 1].triCount = rightCount;
			node.leftFirst = n, node.triCount = 0;
		#ifdef MT_USE_JOBSYSTEM
			if (depth < MT_JOBSYSTEM_DEPTH && threadedBuild)
			{
				BVH_Double* thisBVH = this; // avoid warnings / complexities of capturing this
				subtreeJobs->Execute( [=]() { thisBVH->Build( n, depth + 1 ); } );
				subtreeJobs->Execute( [=]() { thisBVH->Build( n + 1, depth + 1 ); } );
				break;
			}
		#else
			if (threadedBuild && (leftCount >= (1 << 11) || rightCount >= (1 << 11)))
			{
				std::thread t( &BuildDouble_, n, depth + 1, this );
				Build( n + 1, depth + 1 );
				t.join();
				break;
			}
		#endif
			// recurse
			task[taskCount++] = n + 1, nodeIdx = n;
		}
		// fetch subdivision task from stack
		if (taskCount == 0) break; else nodeIdx = task[--taskCount];
	}
	if (depth == 0 || triCount < MT_BUILD_THRESHOLD)
	{
		if (threadedBuild)
		{
		#ifdef MT_USE_JOBSYSTEM
			subtreeJobs->Wait();
		#endif
			newNodePtr = atomicNewNodePtr->load();
			delete atomicNewNodePtr;
			atomicNewNodePtr = 0;
		}
		usedNodes = newNodePtr;
		aabbMin = bvhNode[0].aabbMin, aabbMax = bvhNode[0].aabbMax;
		refittable = true; // not using spatial splits: can refit this BVH
		may_have_holes = false; // the reference builder produces a continuous list of nodes
		bvh_over_aabbs = (verts == 0); // bvh over aabbs is suitable as TLAS
	}
}

double BVH_Double::BVHNode::SurfaceArea() const
{
	const bvhdbl3 e = aabbMax - aabbMin;
	return e.x * e.y + e.y * e.z + e.z * e.x;
}

double BVH_Double::SAHCost( const uint64_t nodeIdx ) const
{
	// Determine the SAH cost of a double-precision tree.
	const BVHNode& n = bvhNode[nodeIdx];
	if (n.isLeaf()) return c_int * n.SurfaceArea() * n.triCount;
	double cost = c_trav * n.SurfaceArea() + SAHCost( n.leftFirst ) + SAHCost( n.leftFirst + 1 );
	return nodeIdx == 0 ? (cost / n.SurfaceArea()) : cost;
}

// Traverse the default BVH layout, double-precision.
int32_t BVH_Double::Intersect( RayEx& ray ) const
{
	if (instList) return IntersectTLAS( ray );
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	float cost = 0;
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			if (customEnabled && customIntersect != 0)
			{
				for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
					if ((*customIntersect)(ray, primIdx[node->leftFirst + i]))
						ray.hit.inst = ray.instIdx;
			}
			else for (uint32_t i = 0; i < node->triCount; i++, cost += c_int)
			{
				const uint64_t idx = primIdx[node->leftFirst + i];
				const uint64_t vertIdx = idx * 3;
				const bvhdbl3 e1 = verts[vertIdx + 1] - verts[vertIdx];
				const bvhdbl3 e2 = verts[vertIdx + 2] - verts[vertIdx];
				const bvhdbl3 h = tinybvh_cross( ray.D, e2 );
				const double a = tinybvh_dot( e1, h );
				if (fabs( a ) < 0.0000001) continue; // ray parallel to triangle
				const double f = 1 / a;
				const bvhdbl3 s = ray.O - bvhdbl3( verts[vertIdx] );
				const double u = f * tinybvh_dot( s, h );
				const bvhdbl3 q = tinybvh_cross( s, e1 );
				const double v = f * tinybvh_dot( ray.D, q );
				if (u < 0 || v < 0 || u + v > 1) continue;
				const double t = f * tinybvh_dot( e2, q );
				if (t > 0 && t < ray.hit.t)
				{
					// register a hit: ray is shortened to t
					ray.hit.t = t, ray.hit.u = u, ray.hit.v = v;
					ray.hit.prim = idx;
					ray.hit.inst = ray.instIdx;
				}
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst];
		BVHNode* child2 = &bvhNode[node->leftFirst + 1];
		double dist1 = child1->Intersect( ray ), dist2 = child2->Intersect( ray );
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_DBL_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_DBL_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return (int32_t)cost;
}

// Traverse a double-precision TLAS.
int32_t BVH_Double::IntersectTLAS( RayEx& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	float cost = 0;
	while (1)
	{
		cost += c_trav;
		if (node->isLeaf())
		{
			RayEx tmp;
			for (uint32_t i = 0; i < node->triCount; i++)
			{
				// BLAS traversal
				const uint64_t instIdx = primIdx[node->leftFirst + i];
				BLASInstanceEx& inst = instList[instIdx];
				if (!(inst.mask & ray.mask)) continue;
				BVH_Double* blas = blasList[inst.blasIdx];
				// 1. Transform ray with the inverse of the instance transform
				tmp.O = tinybvh_transform_point( ray.O, inst.invTransform );
				tmp.D = tinybvh_transform_vector( ray.D, inst.invTransform );
				tmp.rD = bvhdbl3( 1.0 / tmp.D.x, 1.0 / tmp.D.y, 1.0 / tmp.D.z );
				tmp.hit = ray.hit;
				// 2. Traverse BLAS with the transformed ray
				tmp.instIdx = instIdx;
				cost += blas->Intersect( tmp );
				// 3. Restore ray
				ray.hit = tmp.hit;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst];
		BVHNode* child2 = &bvhNode[node->leftFirst + 1];
		double dist1 = child1->Intersect( ray ), dist2 = child2->Intersect( ray );
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_DBL_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_DBL_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return (int32_t)cost;
}

bool BVH_Double::IsOccluded( const RayEx& ray ) const
{
	if (instList) return IsOccludedTLAS( ray );
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	while (1)
	{
		if (node->isLeaf())
		{
			if (customEnabled && customIntersect != 0) for (uint32_t i = 0; i < node->triCount; i++)
				(*customIsOccluded)(ray, primIdx[node->leftFirst + i]);
			else for (uint32_t i = 0; i < node->triCount; i++)
			{
				const uint64_t idx = primIdx[node->leftFirst + i];
				const uint64_t vertIdx = idx * 3;
				const bvhdbl3 e1 = verts[vertIdx + 1] - verts[vertIdx];
				const bvhdbl3 e2 = verts[vertIdx + 2] - verts[vertIdx];
				const bvhdbl3 h = tinybvh_cross( ray.D, e2 );
				const double a = tinybvh_dot( e1, h );
				if (fabs( a ) < 0.0000001) continue; // ray parallel to triangle
				const double f = 1 / a;
				const bvhdbl3 s = ray.O - bvhdbl3( verts[vertIdx] );
				const double u = f * tinybvh_dot( s, h );
				const bvhdbl3 q = tinybvh_cross( s, e1 );
				const double v = f * tinybvh_dot( ray.D, q );
				if (u < 0 || v < 0 || u + v > 1) continue;
				const double t = f * tinybvh_dot( e2, q );
				if (t > 0 && t < ray.hit.t) return true;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst];
		BVHNode* child2 = &bvhNode[node->leftFirst + 1];
		double dist1 = child1->Intersect( ray ), dist2 = child2->Intersect( ray );
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_DBL_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_DBL_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return false;
}

bool BVH_Double::IsOccludedTLAS( const RayEx& ray ) const
{
	BVHNode* node = &bvhNode[0], * stack[64];
	uint32_t stackPtr = 0;
	RayEx tmp;
	while (1)
	{
		if (node->isLeaf())
		{
			for (uint32_t i = 0; i < node->triCount; i++)
			{
				// BLAS traversal
				BLASInstanceEx& inst = instList[primIdx[node->leftFirst + i]];
				if (!(inst.mask & ray.mask)) continue;
				BVH_Double* blas = blasList[inst.blasIdx];
				// 1. Transform ray with the inverse of the instance transform
				tmp.O = tinybvh_transform_point( ray.O, inst.invTransform );
				tmp.D = tinybvh_transform_vector( ray.D, inst.invTransform );
				tmp.rD.x = tmp.D.x > 1e-24 ? (1.0 / tmp.D.x) : (tmp.D.x < -1e-24 ? (1.0 / tmp.D.x) : BVH_DBL_FAR);
				tmp.rD.y = tmp.D.y > 1e-24 ? (1.0 / tmp.D.y) : (tmp.D.y < -1e-24 ? (1.0 / tmp.D.y) : BVH_DBL_FAR);
				tmp.rD.z = tmp.D.z > 1e-24 ? (1.0 / tmp.D.z) : (tmp.D.z < -1e-24 ? (1.0 / tmp.D.z) : BVH_DBL_FAR);
				// 2. Traverse BLAS with the transformed ray
				if (blas->IsOccluded( tmp )) return true;
			}
			if (stackPtr == 0) break; else node = stack[--stackPtr];
			continue;
		}
		BVHNode* child1 = &bvhNode[node->leftFirst];
		BVHNode* child2 = &bvhNode[node->leftFirst + 1];
		double dist1 = child1->Intersect( ray ), dist2 = child2->Intersect( ray );
		if (dist1 > dist2) { tinybvh_swap( dist1, dist2 ); tinybvh_swap( child1, child2 ); }
		if (dist1 == BVH_DBL_FAR /* missed both child nodes */)
		{
			if (stackPtr == 0) break; else node = stack[--stackPtr];
		}
		else /* hit at least one node */
		{
			node = child1; /* continue with the nearest */
			if (dist2 != BVH_DBL_FAR) stack[stackPtr++] = child2; /* push far child */
		}
	}
	return false;
}

// BVHNode::Intersect, double precision
double BVH_Double::BVHNode::Intersect( const RayEx& ray ) const
{
	// double-precision "slab test" ray/AABB intersection
	double tx1 = (aabbMin.x - ray.O.x) * ray.rD.x, tx2 = (aabbMax.x - ray.O.x) * ray.rD.x;
	double tmin = tinybvh_min( tx1, tx2 ), tmax = tinybvh_max( tx1, tx2 );
	double ty1 = (aabbMin.y - ray.O.y) * ray.rD.y, ty2 = (aabbMax.y - ray.O.y) * ray.rD.y;
	tmin = tinybvh_max( tmin, tinybvh_min( ty1, ty2 ) );
	tmax = tinybvh_min( tmax, tinybvh_max( ty1, ty2 ) );
	double tz1 = (aabbMin.z - ray.O.z) * ray.rD.z, tz2 = (aabbMax.z - ray.O.z) * ray.rD.z;
	tmin = tinybvh_max( tmin, tinybvh_min( tz1, tz2 ) );
	tmax = tinybvh_min( tmax, tinybvh_max( tz1, tz2 ) );
	if (tmax >= tmin && tmin < ray.hit.t && tmax >= 0) return tmin; else return BVH_DBL_FAR;
}

#endif

// ============================================================================
//
//        H E L P E R S
//
// ============================================================================

// Update
void BLASInstance::Update( BVHBase* blas )
{
	InvertTransform(); // TODO: done unconditionally; for a big TLAS this may be wasteful. Detect changes automatically?
	// transform the eight corners of the root node aabb using the
	// instance transform and calculate the worldspace aabb over those.
	aabbMin = bvhvec3( BVH_FAR ), aabbMax = bvhvec3( -BVH_FAR );
	bvhvec3 bmin = blas->aabbMin, bmax = blas->aabbMax;
	for (int32_t j = 0; j < 8; j++)
	{
		const bvhvec3 p( j & 1 ? bmax.x : bmin.x, j & 2 ? bmax.y : bmin.y, j & 4 ? bmax.z : bmin.z );
		const bvhvec3 t = tinybvh_transform_point( p, transform );
		aabbMin = tinybvh_min( aabbMin, t ), aabbMax = tinybvh_max( aabbMax, t );
	}
}

// InvertTransform - calculate the inverse of the matrix stored in 'transform'
void BLASInstance::InvertTransform()
{
	// math from MESA, via http://stackoverflow.com/questions/1148309/inverting-a-4x4-matrix
	const float* T = (const float*)&this->transform;
	float* iT = (float*)&this->invTransform;
	iT[0] = T[5] * T[10] * T[15] - T[5] * T[11] * T[14] - T[9] * T[6] * T[15] + T[9] * T[7] * T[14] + T[13] * T[6] * T[11] - T[13] * T[7] * T[10];
	iT[1] = -T[1] * T[10] * T[15] + T[1] * T[11] * T[14] + T[9] * T[2] * T[15] - T[9] * T[3] * T[14] - T[13] * T[2] * T[11] + T[13] * T[3] * T[10];
	iT[2] = T[1] * T[6] * T[15] - T[1] * T[7] * T[14] - T[5] * T[2] * T[15] + T[5] * T[3] * T[14] + T[13] * T[2] * T[7] - T[13] * T[3] * T[6];
	iT[3] = -T[1] * T[6] * T[11] + T[1] * T[7] * T[10] + T[5] * T[2] * T[11] - T[5] * T[3] * T[10] - T[9] * T[2] * T[7] + T[9] * T[3] * T[6];
	iT[4] = -T[4] * T[10] * T[15] + T[4] * T[11] * T[14] + T[8] * T[6] * T[15] - T[8] * T[7] * T[14] - T[12] * T[6] * T[11] + T[12] * T[7] * T[10];
	iT[5] = T[0] * T[10] * T[15] - T[0] * T[11] * T[14] - T[8] * T[2] * T[15] + T[8] * T[3] * T[14] + T[12] * T[2] * T[11] - T[12] * T[3] * T[10];
	iT[6] = -T[0] * T[6] * T[15] + T[0] * T[7] * T[14] + T[4] * T[2] * T[15] - T[4] * T[3] * T[14] - T[12] * T[2] * T[7] + T[12] * T[3] * T[6];
	iT[7] = T[0] * T[6] * T[11] - T[0] * T[7] * T[10] - T[4] * T[2] * T[11] + T[4] * T[3] * T[10] + T[8] * T[2] * T[7] - T[8] * T[3] * T[6];
	iT[8] = T[4] * T[9] * T[15] - T[4] * T[11] * T[13] - T[8] * T[5] * T[15] + T[8] * T[7] * T[13] + T[12] * T[5] * T[11] - T[12] * T[7] * T[9];
	iT[9] = -T[0] * T[9] * T[15] + T[0] * T[11] * T[13] + T[8] * T[1] * T[15] - T[8] * T[3] * T[13] - T[12] * T[1] * T[11] + T[12] * T[3] * T[9];
	iT[10] = T[0] * T[5] * T[15] - T[0] * T[7] * T[13] - T[4] * T[1] * T[15] + T[4] * T[3] * T[13] + T[12] * T[1] * T[7] - T[12] * T[3] * T[5];
	iT[11] = -T[0] * T[5] * T[11] + T[0] * T[7] * T[9] + T[4] * T[1] * T[11] - T[4] * T[3] * T[9] - T[8] * T[1] * T[7] + T[8] * T[3] * T[5];
	iT[12] = -T[4] * T[9] * T[14] + T[4] * T[10] * T[13] + T[8] * T[5] * T[14] - T[8] * T[6] * T[13] - T[12] * T[5] * T[10] + T[12] * T[6] * T[9];
	iT[13] = T[0] * T[9] * T[14] - T[0] * T[10] * T[13] - T[8] * T[1] * T[14] + T[8] * T[2] * T[13] + T[12] * T[1] * T[10] - T[12] * T[2] * T[9];
	iT[14] = -T[0] * T[5] * T[14] + T[0] * T[6] * T[13] + T[4] * T[1] * T[14] - T[4] * T[2] * T[13] - T[12] * T[1] * T[6] + T[12] * T[2] * T[5];
	iT[15] = T[0] * T[5] * T[10] - T[0] * T[6] * T[9] - T[4] * T[1] * T[10] + T[4] * T[2] * T[9] + T[8] * T[1] * T[6] - T[8] * T[2] * T[5];
	const float det = T[0] * iT[0] + T[1] * iT[4] + T[2] * iT[8] + T[3] * iT[12];
	if (det == 0) return; // actually, invert failed. That's bad.
	const float invdet = 1.0f / det;
	for (int i = 0; i < 16; i++) iT[i] *= invdet;
}

#ifdef DOUBLE_PRECISION_SUPPORT

// Update
void BLASInstanceEx::Update( BVH_Double* blas )
{
	InvertTransform(); // TODO: done unconditionally; for a big TLAS this may be wasteful. Detect changes automatically?
	// transform the eight corners of the root node aabb using the
	// instance transform and calculate the worldspace aabb over those.
	aabbMin = bvhdbl3( BVH_FAR ), aabbMax = bvhdbl3( -BVH_FAR );
	bvhdbl3 bmin = blas->aabbMin, bmax = blas->aabbMax;
	for (int32_t j = 0; j < 8; j++)
	{
		const bvhdbl3 p( j & 1 ? bmax.x : bmin.x, j & 2 ? bmax.y : bmin.y, j & 4 ? bmax.z : bmin.z );
		const bvhdbl3 t = tinybvh_transform_point( p, transform );
		aabbMin = tinybvh_min( aabbMin, t ), aabbMax = tinybvh_max( aabbMax, t );
	}
}

// InvertTransform - calculate the inverse of the matrix stored in 'transform'
void BLASInstanceEx::InvertTransform()
{
	// math from MESA, via http://stackoverflow.com/questions/1148309/inverting-a-4x4-matrix
	const double* T = this->transform;
	invTransform[0] = T[5] * T[10] * T[15] - T[5] * T[11] * T[14] - T[9] * T[6] * T[15] + T[9] * T[7] * T[14] + T[13] * T[6] * T[11] - T[13] * T[7] * T[10];
	invTransform[1] = -T[1] * T[10] * T[15] + T[1] * T[11] * T[14] + T[9] * T[2] * T[15] - T[9] * T[3] * T[14] - T[13] * T[2] * T[11] + T[13] * T[3] * T[10];
	invTransform[2] = T[1] * T[6] * T[15] - T[1] * T[7] * T[14] - T[5] * T[2] * T[15] + T[5] * T[3] * T[14] + T[13] * T[2] * T[7] - T[13] * T[3] * T[6];
	invTransform[3] = -T[1] * T[6] * T[11] + T[1] * T[7] * T[10] + T[5] * T[2] * T[11] - T[5] * T[3] * T[10] - T[9] * T[2] * T[7] + T[9] * T[3] * T[6];
	invTransform[4] = -T[4] * T[10] * T[15] + T[4] * T[11] * T[14] + T[8] * T[6] * T[15] - T[8] * T[7] * T[14] - T[12] * T[6] * T[11] + T[12] * T[7] * T[10];
	invTransform[5] = T[0] * T[10] * T[15] - T[0] * T[11] * T[14] - T[8] * T[2] * T[15] + T[8] * T[3] * T[14] + T[12] * T[2] * T[11] - T[12] * T[3] * T[10];
	invTransform[6] = -T[0] * T[6] * T[15] + T[0] * T[7] * T[14] + T[4] * T[2] * T[15] - T[4] * T[3] * T[14] - T[12] * T[2] * T[7] + T[12] * T[3] * T[6];
	invTransform[7] = T[0] * T[6] * T[11] - T[0] * T[7] * T[10] - T[4] * T[2] * T[11] + T[4] * T[3] * T[10] + T[8] * T[2] * T[7] - T[8] * T[3] * T[6];
	invTransform[8] = T[4] * T[9] * T[15] - T[4] * T[11] * T[13] - T[8] * T[5] * T[15] + T[8] * T[7] * T[13] + T[12] * T[5] * T[11] - T[12] * T[7] * T[9];
	invTransform[9] = -T[0] * T[9] * T[15] + T[0] * T[11] * T[13] + T[8] * T[1] * T[15] - T[8] * T[3] * T[13] - T[12] * T[1] * T[11] + T[12] * T[3] * T[9];
	invTransform[10] = T[0] * T[5] * T[15] - T[0] * T[7] * T[13] - T[4] * T[1] * T[15] + T[4] * T[3] * T[13] + T[12] * T[1] * T[7] - T[12] * T[3] * T[5];
	invTransform[11] = -T[0] * T[5] * T[11] + T[0] * T[7] * T[9] + T[4] * T[1] * T[11] - T[4] * T[3] * T[9] - T[8] * T[1] * T[7] + T[8] * T[3] * T[5];
	invTransform[12] = -T[4] * T[9] * T[14] + T[4] * T[10] * T[13] + T[8] * T[5] * T[14] - T[8] * T[6] * T[13] - T[12] * T[5] * T[10] + T[12] * T[6] * T[9];
	invTransform[13] = T[0] * T[9] * T[14] - T[0] * T[10] * T[13] - T[8] * T[1] * T[14] + T[8] * T[2] * T[13] + T[12] * T[1] * T[10] - T[12] * T[2] * T[9];
	invTransform[14] = -T[0] * T[5] * T[14] + T[0] * T[6] * T[13] + T[4] * T[1] * T[14] - T[4] * T[2] * T[13] - T[12] * T[1] * T[6] + T[12] * T[2] * T[5];
	invTransform[15] = T[0] * T[5] * T[10] - T[0] * T[6] * T[9] - T[4] * T[1] * T[10] + T[4] * T[2] * T[9] + T[8] * T[1] * T[6] - T[8] * T[2] * T[5];
	const double det = T[0] * invTransform[0] + T[1] * invTransform[4] + T[2] * invTransform[8] + T[3] * invTransform[12];
	if (det == 0) return; // actually, invert failed. That's bad.
	const double invdet = 1. / det;
	for (int i = 0; i < 16; i++) invTransform[i] *= invdet;
}

#endif

// SA
float BVHBase::SA( const bvhvec3& aabbMin, const bvhvec3& aabbMax )
{
	bvhvec3 e = aabbMax - aabbMin; // extent of the node
	return e.x * e.y + e.y * e.z + e.z * e.x;
}

// IntersectTri
void BVHBase::IntersectTri( Ray& ray, const uint32_t triIdx, const bvhvec4slice& verts, const uint32_t i0, const uint32_t i1, const uint32_t i2 ) const
{
#ifdef WATERTIGHT_TRITEST
	// Woop et al.'s Watertight intersection algorithm.
	// PART 1 - Precalculations
	uint32_t kz = tinybvh_maxdim( ray.D ), kx = (1 << kz) & 3, ky = (1 << kx) & 3;
	if (ray.D[kz] < 0) std::swap( kx, ky );
	const float Sz = ray.rD[kz], Sx = ray.D[kx] * Sz, Sy = ray.D[ky] * Sz;
	// PART 2 - Intersection
	const bvhvec3 C = bvhvec3( verts[i0] ) - ray.O;
	const bvhvec3 A = bvhvec3( verts[i1] ) - ray.O;
	const bvhvec3 B = bvhvec3( verts[i2] ) - ray.O;
	const float Ax = A[kx] - Sx * A[kz], Ay = A[ky] - Sy * A[kz];
	const float Bx = B[kx] - Sx * B[kz], By = B[ky] - Sy * B[kz];
	const float Cx = C[kx] - Sx * C[kz], Cy = C[ky] - Sy * C[kz];
	const float U = Cx * By - Cy * Bx, V = Ax * Cy - Ay * Cx, W = Bx * Ay - By * Ax;
	if ((U < 0 || V < 0 || W < 0) && (U > 0 || V > 0 || W > 0)) return;
	const float det = U + V + W;
	if (det == 0) return;
	const float Az = Sz * A[kz], Bz = Sz * B[kz], Cz = Sz * C[kz];
	const float T = U * Az + V * Bz + W * Cz;
	const float invDet = 1.0f / det, t = T * invDet;
	if (t >= ray.hit.t || t < 0) return;
	const float u = U * invDet, v = V * invDet;
#else
	// Moeller-Trumbore ray/triangle intersection algorithm.
	const bvhvec4 v0_ = verts[i0];
	const bvhvec3 v0 = v0_, e1 = verts[i1] - v0_, e2 = verts[i2] - v0_;
	MOLLER_TRUMBORE_TEST( ray.hit.t, return );
#endif
	// evaluate opacity map, if present.
	if (opmap)
	{
		const float fN = (float)opmapN;
		const int row = int( (u + v) * fN ), diag = int( (1 - u) * fN );
		const int idx = (row * row) + int( v * fN ) + (diag - (opmapN - 1 - row));
		uint32_t* om = opmap + triIdx * ((opmapN * opmapN + 31) >> 5);
		if (!(om[idx >> 5] & (1 << (idx & 31)))) return;
	}
	// register a hit: ray is shortened to t.
	ray.hit.t = t, ray.hit.u = u, ray.hit.v = v;
#if INST_IDX_BITS == 32
	ray.hit.prim = triIdx, ray.hit.inst = ray.instIdx;
#else
	ray.hit.prim = triIdx + ray.instIdx;
#endif
}

// TriOccludes
bool BVHBase::TriOccludes( const Ray& ray, const bvhvec4slice& verts, const uint32_t triIdx, const uint32_t i0, const uint32_t i1, const uint32_t i2 ) const
{
#ifdef WATERTIGHT_TRITEST
	// Woop et al.'s Watertight intersection algorithm.
	// PART 1 - Precalculations
	uint32_t kz = tinybvh_maxdim( ray.D ), kx = (1 << kz) & 3, ky = (1 << kx) & 3;
	if (ray.D[kz] < 0) std::swap( kx, ky );
	const float Sz = ray.rD[kz], Sx = ray.D[kx] * Sz, Sy = ray.D[ky] * Sz;
	// PART 2 - Intersection
	const bvhvec3 A = bvhvec3( verts[i0] ) - ray.O;
	const bvhvec3 B = bvhvec3( verts[i1] ) - ray.O;
	const bvhvec3 C = bvhvec3( verts[i2] ) - ray.O;
	const float Ax = A[kx] - Sx * A[kz], Ay = A[ky] - Sy * A[kz];
	const float Bx = B[kx] - Sx * B[kz], By = B[ky] - Sy * B[kz];
	const float Cx = C[kx] - Sx * C[kz], Cy = C[ky] - Sy * C[kz];
	const float U = Cx * By - Cy * Bx, V = Ax * Cy - Ay * Cx, W = Bx * Ay - By * Ax;
	if ((U < 0 || V < 0 || W < 0) && (U > 0 || V > 0 || W > 0)) return false;
	const float det = U + V + W;
	if (det == 0) return false;
	const float Az = Sz * A[kz], Bz = Sz * B[kz], Cz = Sz * C[kz];
	const float T = U * Az + V * Bz + W * Cz;
	const float invDet = 1.0f / det, t = T * invDet;
	if (t < 0 || t > ray.hit.t) return false;
	const float u = U * invDet, v = V * invDet;
#else
	// Moeller-Trumbore ray/triangle intersection algorithm
	const bvhvec4 v0_ = verts[i0];
	const bvhvec3 v0 = v0_, e1 = verts[i1] - v0_, e2 = verts[i2] - v0_;
	MOLLER_TRUMBORE_TEST( ray.hit.t, return false );
#endif
	// evaluate opacity map, if present.
	if (opmap)
	{
		const float fN = (float)opmapN;
		const int row = int( (u + v) * fN ), diag = int( (1 - u) * fN );
		const int idx = (row * row) + int( v * fN ) + (diag - (opmapN - 1 - row));
		uint32_t* om = opmap + triIdx * ((opmapN * opmapN + 31) >> 5);
		if (!(om[idx >> 5] & (1 << (idx & 31)))) return false;
	}
	// occluded.
	return true;
}

// PrecomputeTriangle (helper), transforms a triangle to the format used in:
// Fast Ray-Triangle Intersections by Coordinate Transformation. Baldwin & Weber, 2016.
void BVHBase::PrecomputeTriangle( const bvhvec4slice& vert, const uint32_t ti0, const uint32_t ti1, const uint32_t ti2, float* T )
{
	bvhvec3 v0 = vert[ti0], v1 = vert[ti1], v2 = vert[ti2];
	bvhvec3 e1 = v1 - v0, e2 = v2 - v0, N = tinybvh_cross( e1, e2 );
	float x1, x2, n = tinybvh_dot( v0, N ), rN;
	if (fabs( N[0] ) > fabs( N[1] ) && fabs( N[0] ) > fabs( N[2] ))
	{
		x1 = v1.y * v0.z - v1.z * v0.y, x2 = v2.y * v0.z - v2.z * v0.y, rN = 1.0f / N.x;
		T[0] = 0, T[1] = e2.z * rN, T[2] = -e2.y * rN, T[3] = x2 * rN;
		T[4] = 0, T[5] = -e1.z * rN, T[6] = e1.y * rN, T[7] = -x1 * rN;
		T[8] = 1, T[9] = N.y * rN, T[10] = N.z * rN, T[11] = -n * rN;
	}
	else if (fabs( N.y ) > fabs( N.z ))
	{
		x1 = v1.z * v0.x - v1.x * v0.z, x2 = v2.z * v0.x - v2.x * v0.z, rN = 1.0f / N.y;
		T[0] = -e2.z * rN, T[1] = 0, T[2] = e2.x * rN, T[3] = x2 * rN;
		T[4] = e1.z * rN, T[5] = 0, T[6] = -e1.x * rN, T[7] = -x1 * rN;
		T[8] = N.x * rN, T[9] = 1, T[10] = N.z * rN, T[11] = -n * rN;
	}
	else if (fabs( N.z ) > 0)
	{
		x1 = v1.x * v0.y - v1.y * v0.x, x2 = v2.x * v0.y - v2.y * v0.x, rN = 1.0f / N.z;
		T[0] = e2.y * rN, T[1] = -e2.x * rN, T[2] = 0, T[3] = x2 * rN;
		T[4] = -e1.y * rN, T[5] = e1.x * rN, T[6] = 0, T[7] = -x1 * rN;
		T[8] = N.x * rN, T[9] = N.y * rN, T[10] = 1, T[11] = -n * rN;
	}
	else memset( T, 0, 12 * 4 );
}

bool BVH::BVHNode::Intersect( const bvhvec3& bmin, const bvhvec3& bmax ) const
{
	return bmin.x < aabbMax.x && bmax.x > aabbMin.x &&
		bmin.y < aabbMax.y && bmax.y > aabbMin.y &&
		bmin.z < aabbMax.z && bmax.z > aabbMin.z;
}

// SplitFrag: cut a fragment in two new fragments. Based on madmann91 code.
bool BVH::SplitFrag( const Fragment& orig, Fragment& left, Fragment& right, const uint32_t axis, const float pos ) const
{
#ifdef BVH_USESSE
	union { __m128 lbmin4; bvhvec4 lbmin; };
	union { __m128 lbmax4; bvhvec4 lbmax; };
	union { __m128 rbmin4; bvhvec4 rbmin; };
	union { __m128 rbmax4; bvhvec4 rbmax; };
	lbmin4 = _mm_set1_ps( BVH_FAR ), rbmin4 = lbmin4;
	lbmax4 = _mm_set1_ps( -BVH_FAR ), rbmax4 = lbmax4;
	union { __m128 v0_4; bvhvec4 v0; };
	union { __m128 v1_4; bvhvec4 v1; };
	union { __m128 v2_4; bvhvec4 v2; };
	const uint32_t vidx = orig.primIdx * 3;
	if (!vertIdx) v0 = verts[vidx], v1 = verts[vidx + 1], v2 = verts[vidx + 2];
	else v0 = verts[vertIdx[vidx]], v1 = verts[vertIdx[vidx + 1]], v2 = verts[vertIdx[vidx + 2]];
	const bool l0 = v0[axis] <= pos, l1 = v1[axis] <= pos, l2 = v2[axis] <= pos;
	if (l0) lbmin4 = _mm_min_ps( lbmin4, v0_4 ), lbmax4 = _mm_max_ps( lbmax4, v0_4 );
	else rbmin4 = _mm_min_ps( rbmin4, v0_4 ), rbmax4 = _mm_max_ps( rbmax4, v0_4 );
	if (l1) lbmin4 = _mm_min_ps( lbmin4, v1_4 ), lbmax4 = _mm_max_ps( lbmax4, v1_4 );
	else rbmin4 = _mm_min_ps( rbmin4, v1_4 ), rbmax4 = _mm_max_ps( rbmax4, v1_4 );
	if (l2) lbmin4 = _mm_min_ps( lbmin4, v2_4 ), lbmax4 = _mm_max_ps( lbmax4, v2_4 );
	else rbmin4 = _mm_min_ps( rbmin4, v2_4 ), rbmax4 = _mm_max_ps( rbmax4, v2_4 );
	union { __m128 c4; bvhvec4 c; };
	if (l0 ^ l1)
		c = v0 + (pos - v0[axis]) / (v1[axis] - v0[axis]) * (v1 - v0), c[axis] = pos,
		lbmin4 = _mm_min_ps( lbmin4, c4 ), lbmax4 = _mm_max_ps( lbmax4, c4 ),
		rbmin4 = _mm_min_ps( rbmin4, c4 ), rbmax4 = _mm_max_ps( rbmax4, c4 );
	if (l1 ^ l2)
		c = v1 + (pos - v1[axis]) / (v2[axis] - v1[axis]) * (v2 - v1), c[axis] = pos,
		lbmin4 = _mm_min_ps( lbmin4, c4 ), lbmax4 = _mm_max_ps( lbmax4, c4 ),
		rbmin4 = _mm_min_ps( rbmin4, c4 ), rbmax4 = _mm_max_ps( rbmax4, c4 );
	if (l2 ^ l0)
		c = v2 + (pos - v2[axis]) / (v0[axis] - v2[axis]) * (v0 - v2), c[axis] = pos,
		lbmin4 = _mm_min_ps( lbmin4, c4 ), lbmax4 = _mm_max_ps( lbmax4, c4 ),
		rbmin4 = _mm_min_ps( rbmin4, c4 ), rbmax4 = _mm_max_ps( rbmax4, c4 );
	if (orig.clipped) // clip against orig box
		lbmin4 = _mm_max_ps( lbmin4, _mm_and_ps( *(__m128*) & orig.bmin, mask3 ) ),
		lbmax4 = _mm_min_ps( lbmax4, _mm_and_ps( *(__m128*) & orig.bmax, mask3 ) ),
		rbmin4 = _mm_max_ps( rbmin4, _mm_and_ps( *(__m128*) & orig.bmin, mask3 ) ),
		rbmax4 = _mm_min_ps( rbmax4, _mm_and_ps( *(__m128*) & orig.bmax, mask3 ) );
	*(__m128*)& left.bmin = lbmin4, * (__m128*)& right.bmin = rbmin4;
	*(__m128*)& left.bmax = lbmax4, * (__m128*)& right.bmax = rbmax4;
	left.primIdx = right.primIdx = orig.primIdx;
	left.clipped = right.clipped = true;
#else
	left.bmin = bvhvec3( BVH_FAR ), left.bmax = bvhvec3( -BVH_FAR );
	left.primIdx = orig.primIdx, left.clipped = true, right = left;
	auto split_edge = [=]( const bvhvec3& a, const bvhvec3& b ) {
		bvhvec3 c = a + (pos - a[axis]) / (b[axis] - a[axis]) * (b - a);
		c[axis] = pos; /* exactly on split position */ return c; };
	bvhvec3 v0, v1, v2;
	const uint32_t vidx = orig.primIdx * 3;
	if (!vertIdx) v0 = verts[vidx], v1 = verts[vidx + 1], v2 = verts[vidx + 2];
	else v0 = verts[vertIdx[vidx]], v1 = verts[vertIdx[vidx + 1]], v2 = verts[vertIdx[vidx + 2]];
	const bool l0 = v0[axis] <= pos, l1 = v1[axis] <= pos, l2 = v2[axis] <= pos;
	if (l0) left.Extend( v0 ); else right.Extend( v0 );
	if (l1) left.Extend( v1 ); else right.Extend( v1 );
	if (l2) left.Extend( v2 ); else right.Extend( v2 );
	if (l0 ^ l1) { const bvhvec3 c = split_edge( v0, v1 ); left.Extend( c ); right.Extend( c ); }
	if (l1 ^ l2) { const bvhvec3 c = split_edge( v1, v2 ); left.Extend( c ); right.Extend( c ); }
	if (l2 ^ l0) { const bvhvec3 c = split_edge( v2, v0 ); left.Extend( c ); right.Extend( c ); }
	if (orig.clipped) // clip against orig box
		left.bmin = tinybvh_max( orig.bmin, left.bmin ),
		left.bmax = tinybvh_min( orig.bmax, left.bmax ),
		right.bmin = tinybvh_max( orig.bmin, right.bmin ),
		right.bmax = tinybvh_min( orig.bmax, right.bmax );
#endif
	return tinybvh_halfarea( left.bmax - left.bmin ) > 0 && tinybvh_halfarea( right.bmax - right.bmin ) > 0;
}

// ClipFrag: clip a fragment for binning.
bool BVH::ClipFrag( const Fragment& orig, Fragment& newFrag, bvhvec3 bmin, bvhvec3 bmax, const uint32_t axis ) const
{
#ifdef BVH_USESSE
	union { __m128 t1min4; bvhvec4 t1min; };
	union { __m128 t1max4; bvhvec4 t1max; };
	union { __m128 t2min4; bvhvec4 t2min; };
	union { __m128 t2max4; bvhvec4 t2max; };
	t1min4 = t2min4 = _mm_set1_ps( BVH_FAR );
	t1max4 = t2max4 = _mm_set1_ps( -BVH_FAR );
	union { __m128 v0_4; bvhvec4 v0; };
	union { __m128 v1_4; bvhvec4 v1; };
	union { __m128 v2_4; bvhvec4 v2; };
	const uint32_t vidx = orig.primIdx * 3;
	if (!vertIdx) v0 = verts[vidx], v1 = verts[vidx + 1], v2 = verts[vidx + 2];
	else v0 = verts[vertIdx[vidx]], v1 = verts[vertIdx[vidx + 1]], v2 = verts[vertIdx[vidx + 2]];
	const float left = bmin[axis], right = bmax[axis];
	// clip against min bounds
	bool in0 = v0[axis] >= left, in1 = v1[axis] >= left, in2 = v2[axis] >= left;
	if (in0) t1min4 = _mm_min_ps( t1min4, v0_4 ), t1max4 = _mm_max_ps( t1max4, v0_4 );
	if (in1) t1min4 = _mm_min_ps( t1min4, v1_4 ), t1max4 = _mm_max_ps( t1max4, v1_4 );
	if (in2) t1min4 = _mm_min_ps( t1min4, v2_4 ), t1max4 = _mm_max_ps( t1max4, v2_4 );
	union { __m128 c4; bvhvec4 c; };
	if (in0 ^ in1)
		c = v0 + (left - v0[axis]) / (v1[axis] - v0[axis]) * (v1 - v0), c[axis] = left,
		t1min4 = _mm_min_ps( t1min4, c4 ), t1max4 = _mm_max_ps( t1max4, c4 ),
		t1min4 = _mm_min_ps( t1min4, c4 ), t1max4 = _mm_max_ps( t1max4, c4 );
	if (in1 ^ in2)
		c = v1 + (left - v1[axis]) / (v2[axis] - v1[axis]) * (v2 - v1), c[axis] = left,
		t1min4 = _mm_min_ps( t1min4, c4 ), t1max4 = _mm_max_ps( t1max4, c4 ),
		t1min4 = _mm_min_ps( t1min4, c4 ), t1max4 = _mm_max_ps( t1max4, c4 );
	if (in2 ^ in0)
		c = v2 + (left - v2[axis]) / (v0[axis] - v2[axis]) * (v0 - v2), c[axis] = left,
		t1min4 = _mm_min_ps( t1min4, c4 ), t1max4 = _mm_max_ps( t1max4, c4 ),
		t1min4 = _mm_min_ps( t1min4, c4 ), t1max4 = _mm_max_ps( t1max4, c4 );
	// clip against max bounds
	in0 = v0[axis] <= right, in1 = v1[axis] <= right, in2 = v2[axis] <= right;
	if (in0) t2min4 = _mm_min_ps( t2min4, v0_4 ), t2max4 = _mm_max_ps( t2max4, v0_4 );
	if (in1) t2min4 = _mm_min_ps( t2min4, v1_4 ), t2max4 = _mm_max_ps( t2max4, v1_4 );
	if (in2) t2min4 = _mm_min_ps( t2min4, v2_4 ), t2max4 = _mm_max_ps( t2max4, v2_4 );
	if (in0 ^ in1)
		c = v0 + (right - v0[axis]) / (v1[axis] - v0[axis]) * (v1 - v0), c[axis] = right,
		t2min4 = _mm_min_ps( t2min4, c4 ), t2max4 = _mm_max_ps( t2max4, c4 ),
		t2min4 = _mm_min_ps( t2min4, c4 ), t2max4 = _mm_max_ps( t2max4, c4 );
	if (in1 ^ in2)
		c = v1 + (right - v1[axis]) / (v2[axis] - v1[axis]) * (v2 - v1), c[axis] = right,
		t2min4 = _mm_min_ps( t2min4, c4 ), t2max4 = _mm_max_ps( t2max4, c4 ),
		t2min4 = _mm_min_ps( t2min4, c4 ), t2max4 = _mm_max_ps( t2max4, c4 );
	if (in2 ^ in0)
		c = v2 + (right - v2[axis]) / (v0[axis] - v2[axis]) * (v0 - v2), c[axis] = right,
		t2min4 = _mm_min_ps( t2min4, c4 ), t2max4 = _mm_max_ps( t2max4, c4 ),
		t2min4 = _mm_min_ps( t2min4, c4 ), t2max4 = _mm_max_ps( t2max4, c4 );
	__m128 finalmin4, finalmax4;
	if (orig.clipped) // clip against orig box
		finalmin4 = _mm_max_ps( _mm_max_ps( t1min4, t2min4 ), _mm_and_ps( *(__m128*) & orig.bmin, mask3 ) ),
		finalmax4 = _mm_min_ps( _mm_min_ps( t1max4, t2max4 ), _mm_and_ps( *(__m128*) & orig.bmax, mask3 ) );
	else
		finalmin4 = _mm_max_ps( t1min4, t2min4 ),
		finalmax4 = _mm_min_ps( t1max4, t2max4 );
	*(__m128*)& newFrag.bmin = finalmin4;
	*(__m128*)& newFrag.bmax = finalmax4;
	newFrag.primIdx = orig.primIdx;
	newFrag.clipped = true;
#else
	Fragment tmp1, tmp2;
	tmp1.bmin = bvhvec3( BVH_FAR ), tmp1.bmax = bvhvec3( -BVH_FAR );
	tmp1.primIdx = orig.primIdx, tmp1.clipped = true, tmp2 = tmp1;
	auto split_edge = [=]( const bvhvec3& a, const bvhvec3& b, const float pos ) {
		bvhvec3 c = a + (pos - a[axis]) / (b[axis] - a[axis]) * (b - a);
		c[axis] = pos; /* exactly on split position */ return c; };
	bvhvec3 v0, v1, v2;
	const uint32_t vidx = orig.primIdx * 3;
	if (!vertIdx) v0 = verts[vidx], v1 = verts[vidx + 1], v2 = verts[vidx + 2];
	else v0 = verts[vertIdx[vidx]], v1 = verts[vertIdx[vidx + 1]], v2 = verts[vertIdx[vidx + 2]];
	const float left = bmin[axis], right = bmax[axis];
	// clip against min bounds
	bool in0 = v0[axis] >= left, in1 = v1[axis] >= left, in2 = v2[axis] >= left;
	if (in0) tmp1.Extend( v0 );
	if (in1) tmp1.Extend( v1 );
	if (in2) tmp1.Extend( v2 );
	if (in0 ^ in1) { const bvhvec3 c = split_edge( v0, v1, left ); tmp1.Extend( c ); }
	if (in1 ^ in2) { const bvhvec3 c = split_edge( v1, v2, left ); tmp1.Extend( c ); }
	if (in2 ^ in0) { const bvhvec3 c = split_edge( v2, v0, left ); tmp1.Extend( c ); }
	// clip against max bounds
	in0 = v0[axis] <= right, in1 = v1[axis] <= right, in2 = v2[axis] <= right;
	if (in0) tmp2.Extend( v0 );
	if (in1) tmp2.Extend( v1 );
	if (in2) tmp2.Extend( v2 );
	if (in0 ^ in1) { const bvhvec3 c = split_edge( v0, v1, right ); tmp2.Extend( c ); }
	if (in1 ^ in2) { const bvhvec3 c = split_edge( v1, v2, right ); tmp2.Extend( c ); }
	if (in2 ^ in0) { const bvhvec3 c = split_edge( v2, v0, right ); tmp2.Extend( c ); }
	newFrag.bmin = tinybvh_max( tmp1.bmin, tmp2.bmin );
	newFrag.bmax = tinybvh_min( tmp1.bmax, tmp2.bmax );
	if (orig.clipped) // clip against orig box
		newFrag.bmin = tinybvh_max( orig.bmin, newFrag.bmin ),
		newFrag.bmax = tinybvh_min( orig.bmax, newFrag.bmax );
#endif
	const float sa = tinybvh_halfarea( newFrag.bmax - newFrag.bmin );
	return sa > 0;
}

// RefitUp: Update bounding boxes of ancestors of the specified node.
void BVH_Verbose::RefitUp( uint32_t nodeIdx )
{
	while (1)
	{
		BVHNode& node = bvhNode[nodeIdx];
		if (!node.isLeaf())
		{
			const BVHNode& left = bvhNode[node.left];
			const BVHNode& right = bvhNode[node.right];
			node.aabbMin = tinybvh_min( left.aabbMin, right.aabbMin );
			node.aabbMax = tinybvh_max( left.aabbMax, right.aabbMax );
		}
		if (nodeIdx == 0) break; else nodeIdx = node.parent;
	}
}

// SAHCostUp: Calculate the SAH cost of a node and its ancestry
float BVH_Verbose::SAHCostUp( uint32_t nodeIdx ) const
{
	float sum = 0;
	while (nodeIdx != 0xffffffff)
	{
		BVHNode& node = bvhNode[nodeIdx];
		sum += BVH::SA( node.aabbMin, node.aabbMax );
		nodeIdx = node.parent;
	}
	return sum;
}

// FindBestNewPosition
// Part of "Fast Insertion-Based Optimization of Bounding Volume Hierarchies"
// K.I.S.S. version with brute-force array search.
uint32_t BVH_Verbose::FindBestNewPosition( const uint32_t Lid ) const
{
	struct Task { float ci; uint32_t node; };
	ALIGNED( 64 ) Task task[512];
	float Cbest = BVH_FAR;
	int tasks = 1 /* doesn't exceed 70 for Crytek Sponza */, Xbest = 0;
	const BVHNode& L = bvhNode[Lid];
	// reinsert L into BVH
	task[0].node = 0, task[0].ci = 0;
	while (tasks > 0)
	{
		// 'pop' task with smallest Ci
		uint32_t bestTask = 0;
		float minCi = task[0].ci; // tnx Brian
		for (int j = 1; j < tasks; j++) if (task[j].ci < minCi) minCi = task[j].ci, bestTask = j;
		const uint32_t Xid = task[bestTask].node;
		const float CiLX = task[bestTask].ci;
		if (--tasks > 0) task[bestTask] = task[tasks];
		// execute task
		const BVHNode& X = bvhNode[Xid];
		if (CiLX + L.SA() >= Cbest) break;
		const float CdLX = SA( tinybvh_min( L.aabbMin, X.aabbMin ), tinybvh_max( L.aabbMax, X.aabbMax ) );
		const float CLX = CiLX + CdLX;
		if (CLX < Cbest && Xid != 0) Cbest = CLX, Xbest = Xid;
		const float Ci = CLX - X.SA();
		if (Ci + L.SA() >= Cbest || X.isLeaf()) continue;
		task[tasks].node = X.left, task[tasks++].ci = Ci;
		task[tasks].node = X.right, task[tasks++].ci = Ci;
	}
	return Xbest;
}

// Determine for each node in the tree the number of primitives
// stored in that subtree. Helper function for MergeLeafs.
uint32_t BVH_Verbose::CountSubtreeTris( const uint32_t nodeIdx, uint32_t* counters )
{
	BVHNode& node = bvhNode[nodeIdx];
	uint32_t result = node.triCount;
	if (!result) result = CountSubtreeTris( node.left, counters ) + CountSubtreeTris( node.right, counters );
	counters[nodeIdx] = result;
	return result;
}

// Write the triangle indices stored in a subtree to a continuous
// slice in the 'newIdx' array. Helper function for MergeLeafs.
void BVH_Verbose::MergeSubtree( const uint32_t nodeIdx, uint32_t* newIdx, uint32_t& newIdxPtr )
{
	BVHNode& node = bvhNode[nodeIdx];
	if (node.isLeaf())
	{
		memcpy( newIdx + newIdxPtr, primIdx + node.firstTri, node.triCount * 4 );
		newIdxPtr += node.triCount;
		return;
	}
	MergeSubtree( node.left, newIdx, newIdxPtr );
	MergeSubtree( node.right, newIdx, newIdxPtr );
}

} // namespace tinybvh

#ifdef __GNUC__
#pragma GCC diagnostic pop
#endif

#endif // TINYBVH_IMPLEMENTATION
