// SPDX-License-Identifier: MIT
#pragma once
#include <array>
#include <chrono>
#include <cstdint>
#include <cstdio>
#include <cstring>
#include <string>

namespace llama_grpc {
inline bool dispatch_opt_in(const char * value) noexcept {
    return value && std::strcmp(value, "1") == 0;
}
inline bool valid_dispatch_id(const char * data, std::size_t size) noexcept {
    if (!data || size != 36) return false;
    for (std::size_t i = 0; i < size; ++i) {
        if (i == 8 || i == 13 || i == 18 || i == 23) {
            if (data[i] != '-') return false;
        } else if (!((data[i] >= '0' && data[i] <= '9') || (data[i] >= 'a' && data[i] <= 'f'))) {
            return false;
        }
    }
    return data[14] == '4' && (data[19] == '8' || data[19] == '9' || data[19] == 'a' || data[19] == 'b');
}
// gRPC string_ref values are not NUL-terminated. Validate before copying.
template<class Metadata> std::string dispatch_id(const Metadata & metadata) {
    auto range = metadata.equal_range("localai-diagnostic-id");
    if (range.first == range.second) return {};
    auto value = range.first++;
    if (range.first != range.second || !valid_dispatch_id(value->second.data(), value->second.size())) return {};
    return std::string(value->second.data(), value->second.size());
}
enum class DispatchPhase : unsigned { receive, prepare_start, prepare_end, post_start, post_end,
                                      first_write_attempt, first_write_complete, count };
enum class DispatchOutcome : unsigned { none, ok, failed };
struct DispatchEvent {
    DispatchPhase phase;
    std::int64_t offset_ns;
    DispatchOutcome outcome;
};
class DispatchDiagnostics {
    using Clock = std::chrono::steady_clock;
    static constexpr std::size_t capacity = static_cast<std::size_t>(DispatchPhase::count);
    std::array<DispatchEvent, capacity> events_{};
    std::array<bool, capacity> seen_{};
    std::array<char, 37> id_{};
    Clock::time_point start_{};
    std::size_t size_ = 0;
    bool enabled_ = false;
    bool emit_ = false;
public:
    DispatchDiagnostics(bool enabled, const std::string & id, bool emit = false) noexcept
        : enabled_(enabled && valid_dispatch_id(id.data(), id.size())), emit_(emit) {
        if (enabled_) {
            std::memcpy(id_.data(), id.data(), 36);
            start_ = Clock::now();
            mark(DispatchPhase::receive);
        }
    }
    DispatchDiagnostics(const DispatchDiagnostics &) = delete;
    DispatchDiagnostics & operator=(const DispatchDiagnostics &) = delete;
    // Flush after reader cleanup, never inside preparation, post_tasks or Write.
    ~DispatchDiagnostics() noexcept {
        if (!enabled_ || !emit_) return;
        static const char * phases[] = {"worker_receive", "worker_prepare_start", "worker_prepare_end",
            "worker_post_start", "worker_post_end", "worker_first_write_attempt", "worker_first_write_complete"};
        static const char * outcomes[] = {"none", "ok", "failed"};
        for (std::size_t i = 0; i < size_; ++i) {
            const auto & e = events_[i];
            std::fprintf(stderr, "{\"diagnostic_id\":\"%s\",\"phase\":\"%s\",\"offset_ns\":%lld,\"outcome\":\"%s\",\"dropped_events\":0}\n",
                id_.data(), phases[static_cast<unsigned>(e.phase)], static_cast<long long>(e.offset_ns),
                outcomes[static_cast<unsigned>(e.outcome)]);
        }
    }
    void mark(DispatchPhase phase, DispatchOutcome outcome = DispatchOutcome::none) noexcept {
        const auto index = static_cast<std::size_t>(phase);
        if (!enabled_ || index >= capacity || seen_[index] || static_cast<unsigned>(outcome) > 2) return;
        seen_[index] = true;
        events_[size_++] = {phase, std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now() - start_).count(), outcome};
    }
    template<class Write> bool write(Write && operation) {
        mark(DispatchPhase::first_write_attempt);
        const bool ok = operation();
        mark(DispatchPhase::first_write_complete, ok ? DispatchOutcome::ok : DispatchOutcome::failed);
        return ok;
    }
    std::size_t size() const noexcept { return size_; }
    const DispatchEvent & event(std::size_t index) const { return events_.at(index); }
};
} // namespace llama_grpc
