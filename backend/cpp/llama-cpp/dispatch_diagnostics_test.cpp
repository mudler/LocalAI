// SPDX-License-Identifier: MIT
#include "dispatch_diagnostics.h"
#include <cassert>
#include <map>
#include <string>
#include <thread>
#include <vector>

using namespace llama_grpc;
static const std::string id = "01234567-89ab-4cde-8fab-0123456789ab";
using Metadata = std::multimap<std::string, std::string>;

int main() {
    assert(dispatch_opt_in("1"));
    for (auto value : {static_cast<const char *>(nullptr), "", "true", "0", "01"}) assert(!dispatch_opt_in(value));
    assert(valid_dispatch_id(id.data(), id.size()));
    for (auto bad : {id + "x", id.substr(1), std::string(4096, 'a'),
                    std::string("01234567-89ab-1cde-8fab-0123456789ab"),
                    std::string("01234567-89ab-4cde-7fab-0123456789ab"),
                    std::string("01234567-89AB-4cde-8fab-0123456789ab")}) {
        assert(!valid_dispatch_id(bad.data(), bad.size()));
    }
    auto nul = id; nul[5] = '\0';
    assert(!valid_dispatch_id(nul.data(), nul.size()));
    Metadata metadata{{"localai-diagnostic-id", id}};
    assert(dispatch_id(metadata) == id);
    metadata.emplace("localai-diagnostic-id", id);
    assert(dispatch_id(metadata).empty());
    assert(dispatch_id(Metadata{}).empty());
    metadata = {{"localai-diagnostic-id", nul}};
    assert(dispatch_id(metadata).empty());

    DispatchDiagnostics off(false, id);
    off.mark(DispatchPhase::prepare_start);
    assert(off.size() == 0);
    DispatchDiagnostics invalid(true, "bad");
    assert(invalid.size() == 0);
    DispatchDiagnostics trace(true, id);
    trace.mark(DispatchPhase::prepare_start);
    trace.mark(DispatchPhase::prepare_end);
    trace.mark(DispatchPhase::post_start);
    trace.mark(DispatchPhase::post_end);
    int writes = 0;
    assert(!trace.write([&] { ++writes; return false; }));
    assert(trace.write([&] { ++writes; return true; }));
    assert(writes == 2);
    assert(trace.size() == 7);
    assert(trace.event(5).phase == DispatchPhase::first_write_attempt);
    assert(trace.event(6).phase == DispatchPhase::first_write_complete);
    assert(trace.event(6).outcome == DispatchOutcome::failed);
    for (std::size_t i = 1; i < trace.size(); ++i)
        assert(trace.event(i).offset_ns >= trace.event(i - 1).offset_ns);
    trace.mark(DispatchPhase::receive);
    trace.mark(static_cast<DispatchPhase>(255));
    assert(trace.size() == 7);
    std::vector<std::thread> threads;
    for (int i = 0; i < 16; ++i) threads.emplace_back([] {
        DispatchDiagnostics local(true, id);
        local.write([] { return true; });
        assert(local.size() == 3);
        assert(local.event(2).outcome == DispatchOutcome::ok);
    });
    for (auto & thread : threads) thread.join();
}
