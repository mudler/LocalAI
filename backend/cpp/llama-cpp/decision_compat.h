// SPDX-License-Identifier: MIT
#pragma once

#include <type_traits>
#include <utility>

// Nimble framing needs every question. Detect the callable signature rather
// than a revision number so older decision-capable forks keep working too.
template <typename Decision, typename State, typename Questions, typename... Args>
void localai_fill_decision_task(const Decision & decision, const State & state,
                                const Questions & questions, Args &&... args) {
    if constexpr (std::is_invocable_v<decltype(&Decision::fill_task),
                  const Decision &, const State &, const Questions &, Args...>) {
        decision.fill_task(state, questions, std::forward<Args>(args)...);
    } else {
        decision.fill_task(state, std::forward<Args>(args)...);
    }
}
