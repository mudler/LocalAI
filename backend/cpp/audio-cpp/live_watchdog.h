#pragma once

// A one-shot idle timer for a bidirectional stream. Standard library only, so
// it is tested without an audio.cpp checkout or a gRPC server.
//
// WHY IT EXISTS. AudioTranscriptionLive holds the model's inference lane for the
// whole stream, because the streaming session is stateful and a concurrent run
// would interleave two callers' audio. Every other RPC in this backend holds the
// lane across COMPUTE, or across a write to a slow reader, and both of those
// terminate on their own. A live stream instead blocks in a client-driven read,
// and a peer that goes silent WITHOUT closing the stream never terminates
// anything: the lane stays taken and every other RPC against that model queues
// behind a client that has stopped speaking. A websocket death does cancel the
// RPC and free it, but "the peer's TCP connection eventually dies" is not a
// bound anyone can state, so this supplies one.
//
// HOW IT ENDS THE STREAM, and the part that is not obvious: gRPC's synchronous
// ServerReaderWriter::Read has no timeout and cannot be given one. The only way
// to unblock it from another thread is ServerContext::TryCancel, which is what
// the callback is for. That means the client sees CANCELLED rather than whatever
// status the handler goes on to return: the returned status is for the server's
// own record. Releasing the lane is the point.
//
// ONE SHOT on purpose. Once the callback has run the stream is being torn down,
// so there is nothing left to watch, and a repeating timer would call TryCancel
// on a context the handler may already have returned from.

#include <chrono>
#include <condition_variable>
#include <functional>
#include <mutex>
#include <thread>

namespace audiocpp_backend {

class IdleWatchdog {
public:
    // A window that is not positive DISABLES the watchdog entirely: no thread is
    // started and fired() never becomes true. That is the operator's escape
    // hatch for a client that legitimately holds a stream open through long
    // pauses, and it is why the option carrying it documents 0 as "no limit"
    // rather than as "expire immediately".
    //
    // `on_idle` runs on the watchdog's own thread with no lock held. It must be
    // safe to call while the watched thread is blocked in a read, which is the
    // only reason this class exists; ServerContext::TryCancel is documented as
    // exactly that.
    IdleWatchdog(std::chrono::milliseconds window, std::function<void()> on_idle);

    // Joins the thread, so the callback can safely capture anything that
    // outlives this object's scope and nothing else has to be reasoned about.
    ~IdleWatchdog();

    IdleWatchdog(const IdleWatchdog &) = delete;
    IdleWatchdog &operator=(const IdleWatchdog &) = delete;

    // Restarts the window. Call it whenever the peer proves it is still there.
    void touch();

    // Stops watching and joins. Idempotent, and REQUIRED before any long
    // non-read work the window must not cover: the caller's own decode is not
    // the peer going quiet, and cancelling in the middle of it would throw away
    // a transcript the client is waiting for.
    void disarm();

    // True once the window elapsed and the callback ran. Stays true after
    // disarm, so the caller can tell "the peer closed" from "we cancelled it".
    bool fired() const;

private:
    void run();

    const std::chrono::milliseconds window_;
    std::function<void()> on_idle_;

    mutable std::mutex mu_;
    std::condition_variable cv_;
    std::chrono::steady_clock::time_point last_;
    bool stop_ = false;
    bool fired_ = false;
    std::thread thread_;
};

// Whether one message read off a live stream is a frame the decoder can
// actually consume, which is the ONLY thing that counts as the peer proving it
// is still there.
//
// Split out of the read loop so the distinction is testable, and because
// getting it wrong is silent. The loop used to touch the watchdog on ANY
// message, before it filtered on has_audio and on an empty pcm field, so a peer
// writing unset-oneof or zero-length frames faster than the window held the
// lane forever: no audio was ever fed, no work was ever done, and the timer
// that exists to break exactly that grip was reset by the frames doing it.
// There is one lane per model and one model per process, so that is a single
// client denying the whole backend. The thrown message already said "no audio
// frame arrived"; this is the code agreeing with it.
bool live_frame_carries_audio(bool has_audio, bool pcm_empty);

} // namespace audiocpp_backend
