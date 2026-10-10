// SPDX-License-Identifier: MIT
#include "gemx_stream.h"

// PureGo supports at most 15 machine arguments; bundle scalar configuration
// without asking Go callers to reproduce a platform-dependent C structure.
extern "C" GEMX_API gemx_status localai_gemx_create(
    const char *gem, const char *pose, const char *detector,
    const char *module, const char *backend, const uint32_t *options,
    int64_t gap, gemx_live **out, char *error, uint64_t capacity) {
  return gemx_live_create(gem, pose, detector, module, backend, options[0],
      "", options[1], options[2], options[3], options[4], options[5],
      GEMX_LIVE_FRAME_INDEX, gap, out, error, capacity);
}
