// SPDX-License-Identifier: MIT
// Standalone regression tests for DS4 request cancellation policy.

#include "request_lifecycle.h"

#include <cstdio>

namespace {

int failures = 0;

struct FakeCancelTarget {
    ds4cpp::CancelCallback callback = nullptr;
    void *userdata = nullptr;
    int installs = 0;
    int clears = 0;
};

struct PostludeCounts {
    int finalize_attempts = 0;
    int finalize_commits = 0;
    int cache_persists = 0;
    bool cache_followed_commit = true;
};

ds4cpp::TerminalDecision run_fake_postlude(
        ds4cpp::TerminalDecision terminal, bool finalize_succeeds,
        PostludeCounts *counts) {
    return ds4cpp::RunPostlude(
        terminal,
        [=]() {
            counts->finalize_attempts++;
            if (!finalize_succeeds) return false;
            counts->finalize_commits++;
            return true;
        },
        [=]() {
            counts->cache_followed_commit = counts->finalize_commits == 1;
            counts->cache_persists++;
        });
}

bool fake_cancel(void *) {
    return false;
}

void fake_set_cancel(void *target, ds4cpp::CancelCallback callback,
                     void *userdata) noexcept {
    auto *fake = static_cast<FakeCancelTarget *>(target);
    fake->callback = callback;
    fake->userdata = userdata;
    if (callback) {
        fake->installs++;
    } else {
        fake->clears++;
    }
}

void check(bool condition, const char *name) {
    if (condition) return;
    std::fprintf(stderr, "FAIL %s\n", name);
    failures++;
}

// Production mutation caught: treating an active request as abandoned would
// skip its parser finalization and cache save.
void test_active_request_continues_and_finalizes() {
    ds4cpp::RequestLifecycle request;

    check(request.ShouldContinue(), "active:continue");
    check(request.ShouldFinalize(), "active:finalize");
}

// Production mutation caught: omitting the ServerContext cancellation branch
// would continue decoding and finalize a partial response.
void test_context_cancellation_stops_without_finalizing() {
    ds4cpp::RequestLifecycle request;

    request.ObserveContextCancellation(true);

    check(!request.ShouldContinue(), "context_cancelled:stop");
    check(!request.ShouldFinalize(), "context_cancelled:no_finalize");
}

// Production mutation caught: ignoring ServerWriter::Write failure would keep
// streaming and finalize a response whose client has gone away.
void test_stream_write_abort_stops_without_finalizing() {
    ds4cpp::RequestLifecycle request;

    request.ObserveStreamWrite(false);

    check(!request.ShouldContinue(), "write_abort:stop");
    check(!request.ShouldFinalize(), "write_abort:no_finalize");
}

// Production mutation caught: combining cancellation and write failure with
// AND would fail to stop when either signal occurs on its own.
void test_cancellation_and_write_abort_are_independent_or_conditions() {
    ds4cpp::RequestLifecycle cancelled;
    cancelled.ObserveContextCancellation(true);
    cancelled.ObserveStreamWrite(true);

    ds4cpp::RequestLifecycle write_aborted;
    write_aborted.ObserveContextCancellation(false);
    write_aborted.ObserveStreamWrite(false);

    check(!cancelled.ShouldContinue(), "or:context_only");
    check(!write_aborted.ShouldContinue(), "or:write_only");
}

// Production mutation caught: treating an incomplete distributed route as an
// error would return before workers have time to connect.
void test_route_wait_pending() {
    check(ds4cpp::DecideRouteWait(0, false) ==
              ds4cpp::RouteWaitDecision::Pending,
          "route_wait:pending");
}

// Production mutation caught: failing to recognize a complete route would
// keep a ready inference request in the polling loop.
void test_route_wait_ready() {
    check(ds4cpp::DecideRouteWait(1, false) ==
              ds4cpp::RouteWaitDecision::Ready,
          "route_wait:ready");
}

// Production mutation caught: ignoring a route probe error would poll until a
// misleading timeout instead of returning UNAVAILABLE promptly.
void test_route_wait_error() {
    check(ds4cpp::DecideRouteWait(-1, false) ==
              ds4cpp::RouteWaitDecision::Error,
          "route_wait:error");
}

// Production mutation caught: omitting cancellation from route waiting would
// leave an abandoned request blocked until the distributed timeout.
void test_route_wait_cancellation() {
    check(ds4cpp::DecideRouteWait(0, true) ==
              ds4cpp::RouteWaitDecision::Cancelled,
          "route_wait:cancelled");
}

// Production mutation caught: checking route errors before cancellation would
// report UNAVAILABLE for a request the client already abandoned.
void test_route_wait_cancellation_precedes_error() {
    check(ds4cpp::DecideRouteWait(-1, true) ==
              ds4cpp::RouteWaitDecision::Cancelled,
          "route_wait:cancellation_precedence");
}

// Production mutation caught: classifying a successful active request as a
// terminal failure would suppress its normal response finalization.
void test_terminal_success() {
    check(ds4cpp::DecideTerminalCause(false, false, false) ==
              ds4cpp::TerminalCause::Success,
          "terminal:success");
}

// Production mutation caught: treating DS4's cooperative sync interruption
// as an ordinary engine error would return INTERNAL instead of CANCELLED.
void test_terminal_sync_interruption_is_cancelled() {
    check(ds4cpp::DecideTerminalCause(true, true, true) ==
              ds4cpp::TerminalCause::Cancelled,
          "terminal:sync_interrupted");
}

// Production mutation caught: treating every nonzero engine result as client
// abandonment would hide genuine DS4 failures behind CANCELLED.
void test_terminal_engine_error() {
    check(ds4cpp::DecideTerminalCause(false, true, false) ==
              ds4cpp::TerminalCause::EngineError,
          "terminal:engine_error");
}

// Production mutation caught: ignoring an rc==0 context cancellation would
// finalize and cache an abandoned request.
void test_terminal_context_abandonment() {
    ds4cpp::RequestLifecycle request;
    request.ObserveContextCancellation(true);

    check(ds4cpp::DecideTerminalCause(
              false, false, !request.ShouldFinalize()) ==
              ds4cpp::TerminalCause::Cancelled,
          "terminal:context_abandonment");
}

// Production mutation caught: ignoring an rc==0 stream write failure would
// finalize and cache an abandoned streaming request.
void test_terminal_write_abandonment() {
    ds4cpp::RequestLifecycle request;
    request.ObserveStreamWrite(false);

    check(ds4cpp::DecideTerminalCause(
              false, false, !request.ShouldFinalize()) ==
              ds4cpp::TerminalCause::Cancelled,
          "terminal:write_abandonment");
}

// Production mutation caught: checking late cancellation or write failure
// before a determined ordinary DS4 error would replace INTERNAL with CANCELLED.
void test_terminal_engine_error_precedes_late_abandonment() {
    ds4cpp::RequestLifecycle cancelled;
    cancelled.ObserveContextCancellation(true);
    ds4cpp::RequestLifecycle write_aborted;
    write_aborted.ObserveStreamWrite(false);

    check(ds4cpp::DecideTerminalCause(
              false, true, !cancelled.ShouldFinalize()) ==
              ds4cpp::TerminalCause::EngineError,
          "terminal:engine_error_precedes_cancellation");
    check(ds4cpp::DecideTerminalCause(
              false, true, !write_aborted.ShouldFinalize()) ==
              ds4cpp::TerminalCause::EngineError,
          "terminal:engine_error_precedes_write_abort");
}

// Production mutation caught: using status precedence alone to gate side
// effects would finalize and persist an engine-error request abandoned later.
void test_abandoned_engine_error_keeps_internal_without_finalizing() {
    ds4cpp::RequestLifecycle request;
    request.ObserveContextCancellation(true);

    ds4cpp::TerminalDecision terminal = ds4cpp::ResolveTerminalDecision(
        false, true, !request.ShouldFinalize());

    check(terminal.cause == ds4cpp::TerminalCause::EngineError,
          "terminal_decision:abandoned_engine_error_status");
    check(!terminal.should_finalize,
          "terminal_decision:abandoned_engine_error_no_finalize");
}

// Production mutation caught: suppressing side effects for every engine error
// would change the existing finalization and cache behavior of active failures.
void test_active_engine_error_still_finalizes() {
    ds4cpp::RequestLifecycle request;

    ds4cpp::TerminalDecision terminal = ds4cpp::ResolveTerminalDecision(
        false, true, !request.ShouldFinalize());

    check(terminal.cause == ds4cpp::TerminalCause::EngineError,
          "terminal_decision:active_engine_error_status");
    check(terminal.should_finalize,
          "terminal_decision:active_engine_error_finalize");
}

// Production mutation caught: persisting before committed finalization would
// cache a state whose final buffered stream reply was never completed.
void test_postlude_active_success_commits_then_persists() {
    PostludeCounts counts;

    ds4cpp::TerminalDecision terminal = run_fake_postlude(
        {ds4cpp::TerminalCause::Success, true}, true, &counts);

    check(terminal.cause == ds4cpp::TerminalCause::Success,
          "postlude:success_outcome");
    check(terminal.should_finalize, "postlude:success_committed");
    check(counts.finalize_attempts == 1, "postlude:success_attempts");
    check(counts.finalize_commits == 1, "postlude:success_commits");
    check(counts.cache_persists == 1, "postlude:success_cache");
    check(counts.cache_followed_commit, "postlude:success_cache_order");
}

// Production mutation caught: starting the postlude for an already-cancelled
// request would flush buffered parser state or persist an abandoned session.
void test_postlude_cancellation_skips_all_side_effects() {
    PostludeCounts counts;

    ds4cpp::TerminalDecision terminal = run_fake_postlude(
        {ds4cpp::TerminalCause::Cancelled, false}, true, &counts);

    check(terminal.cause == ds4cpp::TerminalCause::Cancelled,
          "postlude:cancelled_outcome");
    check(counts.finalize_attempts == 0, "postlude:cancelled_attempts");
    check(counts.finalize_commits == 0, "postlude:cancelled_commits");
    check(counts.cache_persists == 0, "postlude:cancelled_cache");
}

// Production mutation caught: committing the live parser or cache after a
// failed final Write would publish an abandoned streaming postlude.
void test_postlude_finalize_failure_cancels_without_commit_or_cache() {
    PostludeCounts counts;

    ds4cpp::TerminalDecision terminal = run_fake_postlude(
        {ds4cpp::TerminalCause::Success, true}, false, &counts);

    check(terminal.cause == ds4cpp::TerminalCause::Cancelled,
          "postlude:write_failure_outcome");
    check(!terminal.should_finalize, "postlude:write_failure_not_committed");
    check(counts.finalize_attempts == 1, "postlude:write_failure_attempts");
    check(counts.finalize_commits == 0, "postlude:write_failure_commits");
    check(counts.cache_persists == 0, "postlude:write_failure_cache");
}

// Production mutation caught: skipping the postlude for every engine error
// would change active internal-error finalization and cache behavior.
void test_postlude_active_engine_error_finalizes_and_persists() {
    PostludeCounts counts;

    ds4cpp::TerminalDecision terminal = run_fake_postlude(
        {ds4cpp::TerminalCause::EngineError, true}, true, &counts);

    check(terminal.cause == ds4cpp::TerminalCause::EngineError,
          "postlude:engine_error_outcome");
    check(counts.finalize_attempts == 1, "postlude:engine_error_attempts");
    check(counts.finalize_commits == 1, "postlude:engine_error_commits");
    check(counts.cache_persists == 1, "postlude:engine_error_cache");
    check(counts.cache_followed_commit, "postlude:engine_error_cache_order");
}

// Production mutation caught: replacing every failed transactional finalize
// with cancellation would hide an already-determined engine error.
void test_postlude_engine_error_finalize_failure_preserves_internal() {
    PostludeCounts counts;

    ds4cpp::TerminalDecision terminal = run_fake_postlude(
        {ds4cpp::TerminalCause::EngineError, true}, false, &counts);

    check(terminal.cause == ds4cpp::TerminalCause::EngineError,
          "postlude:engine_error_write_failure_outcome");
    check(!terminal.should_finalize,
          "postlude:engine_error_write_failure_not_committed");
    check(counts.finalize_attempts == 1,
          "postlude:engine_error_write_failure_attempts");
    check(counts.finalize_commits == 0,
          "postlude:engine_error_write_failure_commits");
    check(counts.cache_persists == 0,
          "postlude:engine_error_write_failure_cache");
}

// Production mutation caught: status precedence must not grant side-effect
// permission to an engine-error request that was also abandoned.
void test_postlude_abandoned_engine_error_skips_all_side_effects() {
    PostludeCounts counts;

    ds4cpp::TerminalDecision terminal = run_fake_postlude(
        {ds4cpp::TerminalCause::EngineError, false}, true, &counts);

    check(terminal.cause == ds4cpp::TerminalCause::EngineError,
          "postlude:abandoned_engine_error_outcome");
    check(counts.finalize_attempts == 0,
          "postlude:abandoned_engine_error_attempts");
    check(counts.finalize_commits == 0,
          "postlude:abandoned_engine_error_commits");
    check(counts.cache_persists == 0,
          "postlude:abandoned_engine_error_cache");
}

// Production mutation caught: failing to install the request callback would
// make DS4 prompt synchronization unable to observe client cancellation.
void test_cancel_callback_scope_installs_callback() {
    FakeCancelTarget target;
    int request_context = 42;

    {
        ds4cpp::CancelCallbackScope scope(
            &target, fake_set_cancel, fake_cancel, &request_context);
        check(target.callback == fake_cancel, "cancel_scope:callback_installed");
        check(target.userdata == &request_context, "cancel_scope:userdata_installed");
        check(target.installs == 1, "cancel_scope:installed_once");
    }
}

// Production mutation caught: failing to clear the callback at every scope
// exit would leave DS4 pointing at a destroyed stack-owned ServerContext.
void test_cancel_callback_scope_clears_callback() {
    FakeCancelTarget target;
    int request_context = 42;

    {
        ds4cpp::CancelCallbackScope scope(
            &target, fake_set_cancel, fake_cancel, &request_context);
    }

    check(target.callback == nullptr, "cancel_scope:callback_cleared");
    check(target.userdata == nullptr, "cancel_scope:userdata_cleared");
    check(target.clears == 1, "cancel_scope:cleared_once");
}

} // namespace

int main() {
    test_active_request_continues_and_finalizes();
    test_context_cancellation_stops_without_finalizing();
    test_stream_write_abort_stops_without_finalizing();
    test_cancellation_and_write_abort_are_independent_or_conditions();
    test_route_wait_pending();
    test_route_wait_ready();
    test_route_wait_error();
    test_route_wait_cancellation();
    test_route_wait_cancellation_precedes_error();
    test_terminal_success();
    test_terminal_sync_interruption_is_cancelled();
    test_terminal_engine_error();
    test_terminal_context_abandonment();
    test_terminal_write_abandonment();
    test_terminal_engine_error_precedes_late_abandonment();
    test_abandoned_engine_error_keeps_internal_without_finalizing();
    test_active_engine_error_still_finalizes();
    test_postlude_active_success_commits_then_persists();
    test_postlude_cancellation_skips_all_side_effects();
    test_postlude_finalize_failure_cancels_without_commit_or_cache();
    test_postlude_active_engine_error_finalizes_and_persists();
    test_postlude_engine_error_finalize_failure_preserves_internal();
    test_postlude_abandoned_engine_error_skips_all_side_effects();
    test_cancel_callback_scope_installs_callback();
    test_cancel_callback_scope_clears_callback();

    if (failures == 0) {
        std::fprintf(stderr, "all request_lifecycle checks passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d check(s) failed\n", failures);
    return 1;
}
