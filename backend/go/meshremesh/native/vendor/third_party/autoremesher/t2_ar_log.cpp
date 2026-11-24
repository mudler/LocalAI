// Copyright (c) 2026 Robert Beckebans. Part of trellis2.cpp (MIT).
// See t2_ar_log.h.

#include "t2_ar_log.h"

#include <iostream>

namespace t2ar {

std::ostream& log_stream()
{
    return log_enabled() ? std::cerr : null_stream();
}

} // namespace t2ar
