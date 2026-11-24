// SPDX-License-Identifier: MIT
#pragma once

namespace ds4cpp {

using CancelCallback = bool (*)(void *);
using CancelSetter = void (*)(void *, CancelCallback, void *) noexcept;

class CancelCallbackScope {
public:
    CancelCallbackScope(void *target, CancelSetter setter,
                        CancelCallback callback, void *userdata) noexcept
        : target_(target), setter_(setter) {
        setter_(target_, callback, userdata);
    }

    ~CancelCallbackScope() noexcept {
        setter_(target_, nullptr, nullptr);
    }

    CancelCallbackScope(const CancelCallbackScope &) = delete;
    CancelCallbackScope &operator=(const CancelCallbackScope &) = delete;

private:
    void *target_;
    CancelSetter setter_;
};

enum class RouteWaitDecision {
    Pending,
    Ready,
    Error,
    Cancelled,
};

inline RouteWaitDecision DecideRouteWait(int route_status, bool cancelled) {
    if (cancelled) return RouteWaitDecision::Cancelled;
    if (route_status > 0) return RouteWaitDecision::Ready;
    if (route_status < 0) return RouteWaitDecision::Error;
    return RouteWaitDecision::Pending;
}

enum class TerminalCause {
    Success,
    Cancelled,
    EngineError,
};

inline TerminalCause DecideTerminalCause(bool sync_interrupted,
                                         bool engine_error,
                                         bool abandoned) {
    if (sync_interrupted) return TerminalCause::Cancelled;
    if (engine_error) return TerminalCause::EngineError;
    if (abandoned) return TerminalCause::Cancelled;
    return TerminalCause::Success;
}

struct TerminalDecision {
    TerminalCause cause;
    bool should_finalize;
};

inline TerminalDecision ResolveTerminalDecision(bool sync_interrupted,
                                                 bool engine_error,
                                                 bool abandoned) {
    return {
        DecideTerminalCause(sync_interrupted, engine_error, abandoned),
        !sync_interrupted && !abandoned,
    };
}

template <typename Finalize, typename Persist>
TerminalDecision RunPostlude(TerminalDecision terminal,
                             Finalize transactional_finalize,
                             Persist persist) {
    if (!terminal.should_finalize) return terminal;
    if (!transactional_finalize()) {
        terminal.should_finalize = false;
        if (terminal.cause != TerminalCause::EngineError) {
            terminal.cause = TerminalCause::Cancelled;
        }
        return terminal;
    }
    persist();
    return terminal;
}

class RequestLifecycle {
public:
    void ObserveContextCancellation(bool cancelled) {
        context_cancelled_ = context_cancelled_ || cancelled;
    }

    void ObserveStreamWrite(bool succeeded) {
        stream_write_aborted_ = stream_write_aborted_ || !succeeded;
    }

    bool ShouldContinue() const {
        return !context_cancelled_ && !stream_write_aborted_;
    }

    bool ShouldFinalize() const {
        return ShouldContinue();
    }

private:
    bool context_cancelled_ = false;
    bool stream_write_aborted_ = false;
};

} // namespace ds4cpp
