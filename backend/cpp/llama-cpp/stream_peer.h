// SPDX-License-Identifier: MIT
#pragma once

namespace llama_grpc {

// Tracks whether a server-streaming RPC still has somewhere to send tokens.
//
// grpc::ServerWriter::Write() returns false once the peer is gone, and a
// stream never recovers afterwards. Ignoring that result is not harmless: the
// handler goes on draining decoded tokens into a dead stream, so the llama.cpp
// slot stays busy for the rest of the request's token budget. A model config
// with no max_tokens and a large context turns that into tens of minutes per
// abandoned request, and the slots are exactly what every other request queues
// behind.
//
// Returning as soon as the peer is gone is what frees the slot: the handler's
// server_response_reader then goes out of scope and its destructor posts
// SERVER_TASK_TYPE_CANCEL for whatever is still decoding.
class StreamPeer {
public:
    // Records the outcome of a Write(). Once a write has failed the peer stays
    // gone -- a later write cannot succeed on a broken stream.
    void observe_write(bool ok) noexcept {
        if (!ok) {
            gone_ = true;
        }
    }

    // Folds in the RPC's own cancellation flag, so callers have a single
    // predicate to test rather than two that can disagree.
    void observe_cancelled(bool cancelled) noexcept {
        if (cancelled) {
            gone_ = true;
        }
    }

    bool gone() const noexcept { return gone_; }
    bool alive() const noexcept { return !gone_; }

private:
    bool gone_ = false;
};

} // namespace llama_grpc
