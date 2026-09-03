#include "stream_peer.h"

#include <cstdio>

namespace {

int failures = 0;

void check(bool condition, const char *what) {
    if (!condition) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        ++failures;
    }
}

} // namespace

int main() {
    {
        llama_grpc::StreamPeer peer;
        check(peer.alive(), "a fresh peer is alive");
        check(!peer.gone(), "a fresh peer is not gone");
    }

    {
        llama_grpc::StreamPeer peer;
        peer.observe_write(true);
        peer.observe_write(true);
        check(peer.alive(), "successful writes keep the peer alive");
    }

    {
        llama_grpc::StreamPeer peer;
        peer.observe_write(false);
        check(peer.gone(), "a failed write marks the peer gone");
    }

    {
        // The whole point of the guard: a stream never comes back, so a later
        // success must not resurrect a peer an earlier failure retired.
        llama_grpc::StreamPeer peer;
        peer.observe_write(false);
        peer.observe_write(true);
        check(peer.gone(), "a failed write is sticky across later writes");
    }

    {
        llama_grpc::StreamPeer peer;
        peer.observe_cancelled(false);
        check(peer.alive(), "an uncancelled RPC keeps the peer alive");
        peer.observe_cancelled(true);
        check(peer.gone(), "cancellation marks the peer gone");
    }

    {
        llama_grpc::StreamPeer peer;
        peer.observe_cancelled(true);
        peer.observe_cancelled(false);
        check(peer.gone(), "cancellation is sticky across later checks");
    }

    if (failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    return 0;
}
