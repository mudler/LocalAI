/*
 *  Copyright (c) 2026 Robert Beckebans. Part of trellis2.cpp (MIT).
 *
 *  The vendored AutoRemesher core writes progress, timings, and per-island
 *  failures straight to std::cerr.  That is fine for the upstream desktop
 *  application and wrong for a library the Go demo server loads, so every
 *  `std::cerr` in the vendored sources is rewritten to `T2_AR_LOG` by the
 *  mechanical substitution documented in PATCHES.md.
 *
 *  Default: silent.  Set TRELLIS2_AUTOREMESHER_VERBOSE in the environment to
 *  get the upstream output back on stderr, which is what the Phase 1
 *  measurements need.
 *
 *  This is a diagnostic sink, not a logging framework: writes from several
 *  island threads interleave exactly as they did upstream.
 */
#ifndef T2_AR_LOG_H
#define T2_AR_LOG_H

#include <cstdlib>
#include <ostream>
#include <streambuf>

namespace t2ar {

inline bool log_enabled()
{
    static const bool enabled = nullptr != std::getenv("TRELLIS2_AUTOREMESHER_VERBOSE");
    return enabled;
}

// Discards everything written to it.  Returning a real stream (rather than
// compiling the call sites away) keeps the upstream expressions untouched.
inline std::ostream& null_stream()
{
    struct null_buffer : std::streambuf {
        int overflow(int ch) override { return ch; }
    };
    static null_buffer buffer;
    static std::ostream stream(&buffer);
    return stream;
}

std::ostream& log_stream();

} // namespace t2ar

#define T2_AR_LOG (::t2ar::log_stream())

#endif
