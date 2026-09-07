// SPDX-License-Identifier: MIT
#pragma once

#include <algorithm>

namespace ds4cpp {

inline int EffectiveGenerationLimit(int requested, int context_size,
                                    int session_position) {
  const int limit = requested > 0 ? requested : 256;
  const int room = context_size - session_position;
  if (room <= 1) return 0;
  return std::min(limit, room - 1);
}

inline int RemainingGenerationBudget(int effective_limit, int produced) {
  if (effective_limit <= produced) return 0;
  return effective_limit - produced;
}

inline int SpeculativeAcceptedCapacity(int remaining, int draft_allowance,
                                       int buffer_capacity) {
  if (remaining <= 0 || draft_allowance < 0 || buffer_capacity <= 0) return 0;
  return std::min({remaining, draft_allowance + 1, buffer_capacity});
}

} // namespace ds4cpp
