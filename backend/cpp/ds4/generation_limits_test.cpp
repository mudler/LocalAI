// SPDX-License-Identifier: MIT

#include "generation_limits.h"

#include <cstdio>

namespace {

int failures = 0;

void check_equal(int got, int want, const char *name) {
  if (got == want) return;
  std::fprintf(stderr, "FAIL %s: got %d, want %d\n", name, got, want);
  failures++;
}

// Mutation caught: treating omitted or negative max_tokens as unlimited instead
// of preserving DS4's legacy 256-token default.
void test_nonpositive_uses_legacy_default_when_space_permits() {
  check_equal(ds4cpp::EffectiveGenerationLimit(0, 4096, 100), 256,
              "zero max_tokens uses legacy default");
  check_equal(ds4cpp::EffectiveGenerationLimit(-1, 4096, 100), 256,
              "negative max_tokens uses legacy default");
}

// Mutation caught: applying the legacy default without clamping it to the
// post-prefill context room and reserved slot.
void test_legacy_default_is_clamped_by_context() {
  check_equal(ds4cpp::EffectiveGenerationLimit(0, 300, 100), 199,
              "legacy default is context-clamped");
}

// Mutation caught: allowing an explicitly large request to overrun the
// post-prefill context boundary.
void test_large_positive_limit_is_clamped_to_context() {
  check_equal(ds4cpp::EffectiveGenerationLimit(32768, 32768, 100), 32667,
              "large positive is context-clamped");
}

// Mutation caught: replacing every positive request with the legacy default
// rather than preserving a smaller configured limit.
void test_smaller_positive_limit_is_preserved() {
  check_equal(ds4cpp::EffectiveGenerationLimit(64, 4096, 100), 64,
              "smaller positive is preserved");
}

// Mutation caught: consuming the final context slot instead of reserving it as
// required by DS4's generation loop.
void test_no_usable_room_returns_zero() {
  check_equal(ds4cpp::EffectiveGenerationLimit(32, 100, 99), 0,
              "one remaining context slot is not usable");
}

// Mutation caught: sending the original generation limit to a later
// speculative cycle instead of subtracting tokens already produced.
void test_remaining_budget_accounts_for_produced_tokens() {
  check_equal(ds4cpp::RemainingGenerationBudget(10, 4), 6,
              "remaining budget subtracts produced tokens");
  check_equal(ds4cpp::RemainingGenerationBudget(10, 12), 0,
              "remaining budget never becomes negative");
}

// Mutation caught: giving speculative evaluation capacity beyond either the
// output budget, the draft allowance plus its first target token, or the fixed
// accepted-token buffer.
void test_speculative_capacity_obeys_all_bounds() {
  check_equal(ds4cpp::SpeculativeAcceptedCapacity(3, 8, 8), 3,
              "capacity respects remaining output budget");
  check_equal(ds4cpp::SpeculativeAcceptedCapacity(20, 4, 8), 5,
              "capacity includes one target token beyond draft allowance");
  check_equal(ds4cpp::SpeculativeAcceptedCapacity(20, 8, 6), 6,
              "capacity respects fixed buffer");
}

} // namespace

int main() {
  test_nonpositive_uses_legacy_default_when_space_permits();
  test_legacy_default_is_clamped_by_context();
  test_large_positive_limit_is_clamped_to_context();
  test_smaller_positive_limit_is_preserved();
  test_no_usable_room_returns_zero();
  test_remaining_budget_accounts_for_produced_tokens();
  test_speculative_capacity_obeys_all_bounds();

  if (failures == 0) {
    std::fprintf(stderr, "all generation limit checks passed\n");
    return 0;
  }
  std::fprintf(stderr, "%d check(s) failed\n", failures);
  return 1;
}
