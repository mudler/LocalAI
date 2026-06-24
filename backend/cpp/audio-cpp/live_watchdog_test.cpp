// Unit tests for the live stream idle watchdog. Standard library only; the
// harness compiles this as a single translation unit, so the implementation is
// included directly.
//
// These are TIMING tests, which is unavoidable: what is under test is a
// deadline. Every window here is short and every assertion waits several
// multiples of it, so a loaded machine slows the test down rather than
// flipping its answer. The one thing never asserted is how SOON something
// happens, only that it eventually does or never does.

#include "live_watchdog.cpp"

#include <atomic>
#include <cstdio>
#include <string>
#include <thread>

using namespace std::chrono_literals;

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

// A peer that goes quiet without closing. This is the whole point: the lane it
// holds has to come back.
static void test_it_fires_when_nothing_touches_it() {
    std::atomic<int> calls{0};
    audiocpp_backend::IdleWatchdog watchdog(100ms, [&calls] { ++calls; });
    std::this_thread::sleep_for(600ms);
    check(watchdog.fired(), "a window that elapses untouched fires");
    check(calls.load() == 1, "the callback runs exactly once, not once per window");
}

// A peer that is still speaking must never be cut off. Touches land at a third
// of the window, for six windows' worth of wall clock.
static void test_touching_defers_it_indefinitely() {
    std::atomic<int> calls{0};
    audiocpp_backend::IdleWatchdog watchdog(300ms, [&calls] { ++calls; });
    for (int i = 0; i < 20; ++i) {
        std::this_thread::sleep_for(100ms);
        watchdog.touch();
    }
    check(!watchdog.fired(),
          "a stream touched inside every window is never cancelled");
    check(calls.load() == 0, "no callback runs while the peer is still there");
}

// Disarm is what the handler calls when the read side closes, before a decode
// that can take longer than the window. Firing after that would throw away the
// transcript the client is waiting for.
static void test_disarm_stops_it_before_the_window() {
    std::atomic<int> calls{0};
    audiocpp_backend::IdleWatchdog watchdog(200ms, [&calls] { ++calls; });
    std::this_thread::sleep_for(20ms);
    watchdog.disarm();
    std::this_thread::sleep_for(600ms);
    check(!watchdog.fired(), "a disarmed watchdog does not fire");
    check(calls.load() == 0, "a disarmed watchdog runs no callback");
}

static void test_disarm_is_idempotent() {
    audiocpp_backend::IdleWatchdog watchdog(50ms, [] {});
    watchdog.disarm();
    watchdog.disarm();
    watchdog.disarm();
    check(true, "disarming three times joins once and does not abort");
}

// The operator's escape hatch, for a client that legitimately holds a stream
// open through long pauses. Not "expire immediately", which is what a naive
// reading of a zero timeout would give.
static void test_a_non_positive_window_disables_it() {
    std::atomic<int> calls{0};
    {
        audiocpp_backend::IdleWatchdog watchdog(0ms, [&calls] { ++calls; });
        std::this_thread::sleep_for(300ms);
        check(!watchdog.fired(), "a zero window never fires");
    }
    {
        audiocpp_backend::IdleWatchdog watchdog(-5ms, [&calls] { ++calls; });
        std::this_thread::sleep_for(300ms);
        check(!watchdog.fired(), "a negative window never fires");
    }
    check(calls.load() == 0, "a disabled watchdog runs no callback");
}

// fired() has to survive the disarm, because the handler reads it AFTER the
// read loop ends to tell "the peer closed" from "we cancelled the peer", and
// those two get different statuses.
static void test_fired_survives_a_later_disarm() {
    audiocpp_backend::IdleWatchdog watchdog(80ms, [] {});
    std::this_thread::sleep_for(500ms);
    watchdog.disarm();
    check(watchdog.fired(), "a watchdog that fired still says so after disarm");
}

// The destructor joins, so a callback capturing the handler's frame cannot run
// after that frame is gone. Without the join this is a use after free that only
// shows up under load.
//
// The window is LONGER than the scope on purpose. An earlier version of this
// test slept past the window inside the scope, so the callback had already run
// by the time the object was destroyed and a destructor that DETACHED the thread
// instead of joining it passed unnoticed. Mutation testing is what found that;
// the shape below kills it, because a detached thread wakes after the object is
// gone and calls a callback that must never run.
static void test_the_destructor_joins() {
    std::atomic<int> calls{0};
    std::atomic<bool> alive{true};
    {
        audiocpp_backend::IdleWatchdog watchdog(200ms, [&calls, &alive] {
            check(alive.load(),
                  "the callback never runs after the watched scope ended");
            ++calls;
        });
        std::this_thread::sleep_for(20ms);
    }
    alive.store(false);
    std::this_thread::sleep_for(600ms);
    check(calls.load() == 0,
          "destruction stops the timer rather than leaving it running against a "
          "dead frame");
}

// The other half of that pair: a callback that DOES fire inside the scope runs
// exactly once, so the test above is not passing merely because nothing ever
// fires.
static void test_a_firing_watchdog_still_joins_cleanly() {
    std::atomic<int> calls{0};
    {
        audiocpp_backend::IdleWatchdog watchdog(50ms, [&calls] { ++calls; });
        std::this_thread::sleep_for(400ms);
    }
    check(calls.load() == 1, "the callback ran once, inside the scope");
}

// The predicate the live read loop filters on.
static void test_only_a_frame_with_audio_counts() {
    using audiocpp_backend::live_frame_carries_audio;
    check(live_frame_carries_audio(true, false),
          "a frame with a non-empty pcm field carries audio");
    check(!live_frame_carries_audio(true, true),
          "an empty pcm field does not");
    check(!live_frame_carries_audio(false, false),
          "an unset audio oneof does not, whatever the pcm field looks like");
    check(!live_frame_carries_audio(false, true),
          "and neither does an unset oneof with an empty pcm field");
}

// The defect this closes, expressed as behaviour rather than as a call order:
// a peer writing frames the decoder cannot consume, faster than the window,
// used to hold the model's only inference lane forever, because the read loop
// touched the watchdog before it filtered them out. One lane per model and one
// model per process, so that is a single client denying the whole backend,
// which is exactly what the watchdog exists to prevent.
static void test_empty_frames_do_not_hold_the_lane() {
    std::atomic<int> cancels{0};
    std::atomic<bool> stop{false};
    audiocpp_backend::IdleWatchdog watchdog(80ms, [&cancels] { ++cancels; });

    // The read loop with the real filter in it: frames arrive continuously,
    // none of them carries audio, and only a frame that does may touch.
    std::thread peer([&] {
        while (!stop.load()) {
            if (audiocpp_backend::live_frame_carries_audio(false, true)) {
                watchdog.touch();
            }
            std::this_thread::sleep_for(5ms);
        }
    });

    std::this_thread::sleep_for(600ms);
    stop.store(true);
    peer.join();
    watchdog.disarm();

    check(cancels.load() == 1,
          "a flood of frames with no audio in them still releases the lane");

    // The mirror image, so this cannot pass merely because the watchdog always
    // fires: a peer that keeps sending audio is left alone, exactly as before.
    std::atomic<int> live_cancels{0};
    std::atomic<bool> live_stop{false};
    audiocpp_backend::IdleWatchdog live(80ms, [&live_cancels] { ++live_cancels; });
    std::thread speaker([&] {
        while (!live_stop.load()) {
            if (audiocpp_backend::live_frame_carries_audio(true, false)) {
                live.touch();
            }
            std::this_thread::sleep_for(5ms);
        }
    });
    std::this_thread::sleep_for(600ms);
    live_stop.store(true);
    speaker.join();
    live.disarm();
    check(live_cancels.load() == 0,
          "a peer that keeps sending audio is never cancelled");
}

int main() {
    test_it_fires_when_nothing_touches_it();
    test_touching_defers_it_indefinitely();
    test_disarm_stops_it_before_the_window();
    test_disarm_is_idempotent();
    test_a_non_positive_window_disables_it();
    test_fired_survives_a_later_disarm();
    test_the_destructor_joins();
    test_a_firing_watchdog_still_joins_cleanly();
    test_only_a_frame_with_audio_counts();
    test_empty_frames_do_not_hold_the_lane();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all live_watchdog checks passed\n");
    return 0;
}
