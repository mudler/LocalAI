// SPDX-License-Identifier: MIT
#include "dispatch_diagnostics.h"
#include <cassert>
#include <map>
#include <string>
#include <thread>
#include <vector>
#include <algorithm>
#include <cstdlib>
#include <regex>
#include <set>
#include <sstream>
#include <stdexcept>
#include <unistd.h>

using namespace llama_grpc;
static const std::string id = "01234567-89ab-4cde-8fab-0123456789ab";
using Metadata = std::multimap<std::string, std::string>;

// Redirect the real stderr descriptor, including fprintf in the destructor.
// Call only outside concurrent sections; join all writers before reading.
class StderrCapture {
    int saved_;
    FILE * file_;
public:
    StderrCapture() {
        const char * tmp = std::getenv("TMPDIR");
        std::string path = std::string(tmp ? tmp : ".") + "/dispatch-capture-XXXXXX";
        std::vector<char> name(path.begin(), path.end());
        name.push_back('\0');
        int fd = mkstemp(name.data());
        assert(fd >= 0);
        assert(unlink(name.data()) == 0);
        file_ = fdopen(fd, "w+");
        assert(file_);
        std::fflush(stderr);
        saved_ = dup(fileno(stderr));
        assert(saved_ >= 0);
        assert(dup2(fd, fileno(stderr)) >= 0);
    }
    std::string read() {
        std::fflush(stderr);
        std::rewind(file_);
        std::string result;
        char buffer[4096];
        std::size_t count;
        while ((count = std::fread(buffer, 1, sizeof(buffer), file_))) result.append(buffer, count);
        assert(!std::ferror(file_));
        return result;
    }
    ~StderrCapture() {
        std::fflush(stderr);
        assert(dup2(saved_, fileno(stderr)) >= 0);
        close(saved_);
        std::fclose(file_);
    }
};

static void emission_tests() {
    StderrCapture capture;
    const std::string payload = "payload-private-sentinel";
    const std::string auth = "Bearer auth-private-sentinel";
    const std::string error = "error-private-sentinel";
    {
        DispatchDiagnostics disabled(false, id, true);
        disabled.write([] { return true; });
        DispatchDiagnostics invalid(true, auth, true);
    }
    assert(capture.read().empty());
    std::set<std::string> expected;
    std::vector<std::thread> threads;
    for (int i = 0; i < 16; ++i) {
        auto distinct = id;
        distinct.back() = "0123456789abcdef"[i];
        expected.insert(distinct);
        threads.emplace_back([distinct, &payload, &auth] {
            Metadata metadata{{"localai-diagnostic-id", distinct}, {"authorization", auth}, {"payload", payload}};
            DispatchDiagnostics local(true, dispatch_id(metadata), true);
            local.mark(DispatchPhase::prepare_start);
            local.mark(DispatchPhase::prepare_end);
            local.mark(DispatchPhase::post_start);
            local.mark(DispatchPhase::post_end);
            local.write([&] { assert(!payload.empty()); return true; });
        });
    }
    for (auto & thread : threads) thread.join();
    // Throw through the real write wrapper, then emit during stack unwinding.
    auto throwing_id = id;
    throwing_id[0] = 'f';
    try {
        DispatchDiagnostics throwing(true, throwing_id, true);
        throwing.write([&]() -> bool { throw std::runtime_error(error); });
        assert(false);
    } catch (const std::runtime_error & e) {
        assert(e.what() == error);
    }
    const auto output = capture.read();
    for (const auto & secret : {payload, auth, error}) assert(output.find(secret) == std::string::npos);
    // Exact grammar enforces the five-field closed schema and whole JSON lines.
    const std::regex record(R"re(^\{"diagnostic_id":"([0-9a-f-]{36})","phase":"(worker_receive|worker_prepare_start|worker_prepare_end|worker_post_start|worker_post_end|worker_first_write_attempt|worker_first_write_complete)","offset_ns":([0-9]+),"outcome":"(none|ok|failed)","dropped_events":0\}$)re");
    std::map<std::string, std::vector<std::string>> phases;
    std::map<std::string, long long> offsets;
    std::istringstream lines(output);
    std::string line;
    while (std::getline(lines, line)) {
        std::smatch match;
        assert(std::regex_match(line, match, record));
        const std::string key = match[1], phase = match[2], outcome = match[4];
        assert(valid_dispatch_id(key.data(), key.size()));
        assert(expected.count(key) || key == throwing_id);
        const auto offset = std::stoll(match[3]);
        assert(offset >= offsets[key]);
        offsets[key] = offset;
        assert(outcome == (phase == "worker_first_write_complete" ? "ok" : "none"));
        phases[key].push_back(phase);
    }
    assert(!output.empty() && output.back() == '\n');
    const std::vector<std::string> complete{"worker_receive", "worker_prepare_start", "worker_prepare_end", "worker_post_start", "worker_post_end", "worker_first_write_attempt", "worker_first_write_complete"};
    for (const auto & key : expected) assert(phases[key] == complete);
    assert((phases[throwing_id] == std::vector<std::string>{"worker_receive", "worker_first_write_attempt"}));
    assert(phases.size() == 17);
}

// Informational distribution, not a performance gate. Each sample includes
// construction, all seven milestones, and destruction; emission uses a file.
static void overhead_evidence() {
    using Clock = std::chrono::steady_clock;
    for (int mode = 0; mode < 3; ++mode) {
        StderrCapture capture;
        std::vector<long long> samples;
        for (int batch = 0; batch < 101; ++batch) {
            const auto start = Clock::now();
            for (int i = 0; i < 100; ++i) {
                DispatchDiagnostics trace(mode != 0, id, mode == 2);
                trace.mark(DispatchPhase::prepare_start);
                trace.mark(DispatchPhase::prepare_end);
                trace.mark(DispatchPhase::post_start);
                trace.mark(DispatchPhase::post_end);
                trace.write([] { return true; });
            }
            samples.push_back(std::chrono::duration_cast<std::chrono::nanoseconds>(Clock::now() - start).count() / 100);
        }
        std::sort(samples.begin(), samples.end());
        std::printf("dispatch overhead mode=%s batch_mean_ns min=%lld p50=%lld p95=%lld p99=%lld max=%lld batches=101 requests_per_batch=100\n",
            mode == 0 ? "disabled" : mode == 1 ? "collector" : "collector+stderr-file",
            samples.front(), samples[50], samples[95], samples[99], samples.back());
    }
}

int main() {
    emission_tests();
    overhead_evidence();
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
