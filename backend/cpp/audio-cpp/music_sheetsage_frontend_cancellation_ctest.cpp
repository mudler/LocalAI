// SPDX-License-Identifier: MIT
// Host-waveform checkpoint only: GraphOnly remains not music-admissible.
#include "engine/models/sheetsage/audio_frontend.h"
#include "engine/framework/runtime/session.h"
#include <atomic>
#include <array>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <climits>
#include <thread>
#ifdef _OPENMP
#include <omp.h>
#endif
using Frontend = engine::models::sheetsage::SheetSage2AudioFrontend;
using Work = Frontend::Work;
using namespace engine::core;
static void require(bool ok, const char * message) {
    if (!ok) { std::fprintf(stderr, "FAIL: %s\n", message); std::exit(1); }
}
static std::vector<float> input(int frames, int channels) {
    std::vector<float> x(static_cast<size_t>(frames) * channels);
    for (size_t i = 0; i < x.size(); ++i) x[i] = float(int((i * 73 + 19) % 257) - 128) / 128;
    return x;
}
static uint64_t hash(const std::vector<float> & x) {
    uint64_t h = 1469598103934665603ULL;
    for (float v : x) { uint32_t b; std::memcpy(&b, &v, 4); h ^= b; h *= 1099511628211ULL; }
    return h;
}
struct Frozen { int channels, source, target, frames; size_t count; uint64_t hash; };
// Frozen BEFORE 0003, compiled from the unmodified pinned frontend with GCC -O3.
// Nonconstant dyadic input; FP32 operation order/FMA/interpolation/reflection oracle.
// Hashing float words avoids byte-order dependence. Platform libm may need a
// separately reviewed scalar-tolerance oracle; do not refresh from new code.
static const Frozen frozen[] = {
{1,48000,48000,0,0,0x14650fb0739d0383ULL},
{1,48000,48000,1,1,0x6ebd2c6bf62af799ULL},
{1,48000,48000,7,7,0x346c3cace17e7711ULL},
{1,48000,48000,257,257,0x38f4185950b85b99ULL},
{1,48000,16000,0,0,0x14650fb0739d0383ULL},
{1,48000,16000,1,0,0x14650fb0739d0383ULL},
{1,48000,16000,7,0,0x14650fb0739d0383ULL},
{1,48000,16000,257,86,0xe7043cc6bfeebea3ULL},
{1,16000,24000,0,0,0x14650fb0739d0383ULL},
{1,16000,24000,1,0,0x14650fb0739d0383ULL},
{1,16000,24000,7,3,0xf442dc5b2bb70bd7ULL},
{1,16000,24000,257,386,0x16b5c7ca706d9037ULL},
{1,44117,48000,0,0,0x14650fb0739d0383ULL},
{1,44117,48000,1,0,0x14650fb0739d0383ULL},
{1,44117,48000,7,3,0x8eb5743ed9016bf9ULL},
{1,44117,48000,257,280,0x1fd7dd07833fdd38ULL},
{2,48000,48000,0,0,0x14650fb0739d0383ULL},
{2,48000,48000,1,1,0x77c5c16c05848cc8ULL},
{2,48000,48000,7,7,0xb2fe6502e15d3e57ULL},
{2,48000,48000,257,257,0xee5a8b7c7fc5d525ULL},
{2,48000,16000,0,0,0x14650fb0739d0383ULL},
{2,48000,16000,1,0,0x14650fb0739d0383ULL},
{2,48000,16000,7,0,0x14650fb0739d0383ULL},
{2,48000,16000,257,86,0xfac20a522224533cULL},
{2,16000,24000,0,0,0x14650fb0739d0383ULL},
{2,16000,24000,1,0,0x14650fb0739d0383ULL},
{2,16000,24000,7,3,0x5e4f10571b69369bULL},
{2,16000,24000,257,386,0x1a5dc17b30dae6e7ULL},
{2,44117,48000,0,0,0x14650fb0739d0383ULL},
{2,44117,48000,1,0,0x14650fb0739d0383ULL},
{2,44117,48000,7,3,0x28031e8ac18768dfULL},
{2,44117,48000,257,280,0xeac13a188f4f76f2ULL},
{3,48000,48000,0,0,0x14650fb0739d0383ULL},
{3,48000,48000,1,1,0x77c5c16c05848cc8ULL},
{3,48000,48000,7,7,0x6ae783d16d32e73cULL},
{3,48000,48000,257,257,0xf4800dab7bb53b63ULL},
{3,48000,16000,0,0,0x14650fb0739d0383ULL},
{3,48000,16000,1,0,0x14650fb0739d0383ULL},
{3,48000,16000,7,0,0x14650fb0739d0383ULL},
{3,48000,16000,257,86,0x56d63ca5eaa5b5f3ULL},
{3,16000,24000,0,0,0x14650fb0739d0383ULL},
{3,16000,24000,1,0,0x14650fb0739d0383ULL},
{3,16000,24000,7,3,0x72f6eb8b0fce1da6ULL},
{3,16000,24000,257,386,0xd8fb3383b8c3ba97ULL},
{3,44117,48000,0,0,0x14650fb0739d0383ULL},
{3,44117,48000,1,0,0x14650fb0739d0383ULL},
{3,44117,48000,7,3,0x6722eac73dd9cf96ULL},
{3,44117,48000,257,280,0x4a106caea79e160ULL},
{4,48000,48000,0,0,0x14650fb0739d0383ULL},
{4,48000,48000,1,1,0xdd39046e64e767a1ULL},
{4,48000,48000,7,7,0x85d7557e7540022fULL},
{4,48000,48000,257,257,0xb658107e680aa4dULL},
{4,48000,16000,0,0,0x14650fb0739d0383ULL},
{4,48000,16000,1,0,0x14650fb0739d0383ULL},
{4,48000,16000,7,0,0x14650fb0739d0383ULL},
{4,48000,16000,257,86,0xc8e160241054464eULL},
{4,16000,24000,0,0,0x14650fb0739d0383ULL},
{4,16000,24000,1,0,0x14650fb0739d0383ULL},
{4,16000,24000,7,3,0xc1d077cc4e158633ULL},
{4,16000,24000,257,386,0xe15b9e85e0f8d206ULL},
{4,44117,48000,0,0,0x14650fb0739d0383ULL},
{4,44117,48000,1,0,0x14650fb0739d0383ULL},
{4,44117,48000,7,3,0x66f45d1d3b34ebaULL},
{4,44117,48000,257,280,0xf52d60b70acc8156ULL},
{5,48000,48000,0,0,0x14650fb0739d0383ULL},
{5,48000,48000,1,1,0xcad65e6c92a9c78fULL},
{5,48000,48000,7,7,0xe33c492e256bbeabULL},
{5,48000,48000,257,257,0xec12ccbc18a9338eULL},
{5,48000,16000,0,0,0x14650fb0739d0383ULL},
{5,48000,16000,1,0,0x14650fb0739d0383ULL},
{5,48000,16000,7,0,0x14650fb0739d0383ULL},
{5,48000,16000,257,86,0xc784faa83cb8716cULL},
{5,16000,24000,0,0,0x14650fb0739d0383ULL},
{5,16000,24000,1,0,0x14650fb0739d0383ULL},
{5,16000,24000,7,3,0x4b638d3e2fedfdbcULL},
{5,16000,24000,257,386,0x5b0d0cf998bb352bULL},
{5,44117,48000,0,0,0x14650fb0739d0383ULL},
{5,44117,48000,1,0,0x14650fb0739d0383ULL},
{5,44117,48000,7,3,0x98d0e3690760a354ULL},
{5,44117,48000,257,280,0x802ace5abbb0e2eaULL},
{6,48000,48000,0,0,0x14650fb0739d0383ULL},
{6,48000,48000,1,1,0x7ac5c16c0a9d8cc8ULL},
{6,48000,48000,7,7,0x65fc9a00ea8ab2fdULL},
{6,48000,48000,257,257,0x2cceb79b5628b889ULL},
{6,48000,16000,0,0,0x14650fb0739d0383ULL},
{6,48000,16000,1,0,0x14650fb0739d0383ULL},
{6,48000,16000,7,0,0x14650fb0739d0383ULL},
{6,48000,16000,257,86,0x9fe255fba9fb7b0bULL},
{6,16000,24000,0,0,0x14650fb0739d0383ULL},
{6,16000,24000,1,0,0x14650fb0739d0383ULL},
{6,16000,24000,7,3,0xff40e008a3692fadULL},
{6,16000,24000,257,386,0x34e7f3221608f8d2ULL},
{6,44117,48000,0,0,0x14650fb0739d0383ULL},
{6,44117,48000,1,0,0x14650fb0739d0383ULL},
{6,44117,48000,7,3,0x1fec127637e75a16ULL},
{6,44117,48000,257,280,0x2090164f312f9c59ULL},
{7,48000,48000,0,0,0x14650fb0739d0383ULL},
{7,48000,48000,1,1,0x42c5c16bab758cc8ULL},
{7,48000,48000,7,7,0xe73f830eecb67f7fULL},
{7,48000,48000,257,257,0xbc77cce9e8a8fbf2ULL},
{7,48000,16000,0,0,0x14650fb0739d0383ULL},
{7,48000,16000,1,0,0x14650fb0739d0383ULL},
{7,48000,16000,7,0,0x14650fb0739d0383ULL},
{7,48000,16000,257,86,0x32cdbd9fea942f2cULL},
{7,16000,24000,0,0,0x14650fb0739d0383ULL},
{7,16000,24000,1,0,0x14650fb0739d0383ULL},
{7,16000,24000,7,3,0xd69bed0b4dc48951ULL},
{7,16000,24000,257,386,0x64a29f3bfeb394d7ULL},
{7,44117,48000,0,0,0x14650fb0739d0383ULL},
{7,44117,48000,1,0,0x14650fb0739d0383ULL},
{7,44117,48000,7,3,0x4a66bef3dd07fb60ULL},
{7,44117,48000,257,280,0xcadeb02aea0eddc8ULL},
{8,48000,48000,0,0,0x14650fb0739d0383ULL},
{8,48000,48000,1,1,0x92c5c16c33658cc8ULL},
{8,48000,48000,7,7,0xe08d5d8a3a551486ULL},
{8,48000,48000,257,257,0xa558d6e735be5b6bULL},
{8,48000,16000,0,0,0x14650fb0739d0383ULL},
{8,48000,16000,1,0,0x14650fb0739d0383ULL},
{8,48000,16000,7,0,0x14650fb0739d0383ULL},
{8,48000,16000,257,86,0x31bf86f2e62039b5ULL},
{8,16000,24000,0,0,0x14650fb0739d0383ULL},
{8,16000,24000,1,0,0x14650fb0739d0383ULL},
{8,16000,24000,7,3,0x3c2fa6184b62ffe8ULL},
{8,16000,24000,257,386,0x80ec1c326d9e7e89ULL},
{8,44117,48000,0,0,0x14650fb0739d0383ULL},
{8,44117,48000,1,0,0x14650fb0739d0383ULL},
{8,44117,48000,7,3,0xfb7a60a5b93c3454ULL},
{8,44117,48000,257,280,0x2a1e38812c84dbd3ULL},
};
struct Progress {
    std::array<std::atomic<uint64_t>, 8> units{};
    std::atomic<unsigned> workers{0};
    RunStopHandle stop;
    int stage = -1;
    uint64_t at = 17;
    bool wait_workers = false;
};
static void progress(void * data, Work work) noexcept {
    auto & p = *static_cast<Progress *>(data);
    const auto stage = static_cast<size_t>(work);
    unsigned bit = 1;
#ifdef _OPENMP
    bit <<= omp_get_thread_num();
#endif
    if (work == Work::Convolution) p.workers.fetch_or(bit);
    const auto count = p.units[stage].fetch_add(1) + 1;
    if (p.stage == static_cast<int>(stage) && count >= p.at) {
        if (p.wait_workers) {
            // Real post-tap work on both workers is required before cancellation.
            if ((p.workers.load() & 3) != 3) return;
        }
        p.stop->cancel();
    }
}
static void numerics() {
    for (const auto & f : frozen) {
        Frontend frontend;
        auto x = input(f.frames, f.channels);
        for (int warm = 0; warm < 2; ++warm) {
            auto y = frontend.prepare(x, f.source, f.channels, f.target, 1);
            require(y.size() == f.count && hash(y) == f.hash, "frozen pre-change waveform cold/warm");
        }
    }
    // Explicit default-layout LFE silence, including resampling.
    for (int c : {3, 6, 7, 8}) for (int target : {48000, 16000}) {
        std::vector<float> x(512 * c);
        for (int i = 0; i < 512; ++i) x[i * c + (c == 3 ? 2 : 3)] = float(i % 19);
        Frontend f;
        for (float v : f.prepare(x, 48000, c, target, 1)) require(v == 0, "LFE gain zero");
    }
}
static void cancellation(int stage, int threads, bool warm) {
    Frontend f;
    const int channels = (stage == int(Work::Extraction) || stage == int(Work::Accumulation) || stage == int(Work::Mixdown)) ? 3 : 1;
    const int source = 44117, target = stage == int(Work::Mixdown) ? source : 48000;
    auto x = input(8192, channels);
    if (warm) f.prepare(x, source, channels, target, threads);
    Progress baseline;
    Frontend::set_progress(progress, &baseline);
    auto expected = f.prepare(x, source, channels, target, threads);
    // Coefficient/normalization tests require an actually cold instance.
    Frontend cold;
    Frontend & active = warm ? f : cold;
    Progress p;
    p.stop = std::make_shared<RunStopState>(); p.stage = stage;
    p.at = stage == int(Work::PhaseCopy) ? 2 : 17;
    p.wait_workers = threads == 2 && stage == int(Work::Convolution);
    Frontend::set_progress(progress, &p);
    bool cancelled = false;
    try { active.prepare(x, source, channels, target, threads, p.stop); }
    catch (const engine::runtime::RunCancelled &) { cancelled = true; }
    require(cancelled, "typed active cancellation");
    const auto units = p.units[stage].load();
    // Cold baseline must include bank work (f was used above only if warm).
    require(units > 0 && units < baseline.units[stage].load(), "work bounded below baseline, not final throw only");
    // Serial polling is per actual scalar unit, not merely per frame/stage.
    if (threads == 1) require(units == p.at, "fixed one-unit polling bound");
    std::array<uint64_t, 8> settled{};
    for (size_t i = 0; i < settled.size(); ++i) settled[i] = p.units[i].load();
    std::this_thread::yield();
    for (size_t i = 0; i < settled.size(); ++i)
        require(p.units[i].load() == settled[i], "all progress settled after return/barrier");
    if (p.wait_workers) {
        require((p.workers.load() & 3) == 3, "two actual OpenMP tap workers");
        std::printf("OpenMP actual workers mask=%u cancelled taps=%llu baseline=%llu\n", p.workers.load(),
            (unsigned long long)units, (unsigned long long)baseline.units[stage].load());
    }
    Frontend::set_progress(nullptr, nullptr);
    Frontend fresh;
    auto recovered = active.prepare(x, source, channels, target, threads);
    require(recovered == fresh.prepare(x, source, channels, target, threads) && recovered == expected,
        "cold partial-bank abort/warm cancellation recovery equals fresh and baseline");
    // A cancelled twice must not poison a new B.
    p.stop->cancel(); p.stop->cancel();
    auto b = std::make_shared<RunStopState>();
    require(active.prepare(x, source, channels, target, threads, b) == expected, "old A/new B isolation");
}
int main(int argc, char ** argv) {
#ifdef _OPENMP
    omp_set_dynamic(0);
#endif
    if (argc == 2) { cancellation(std::atoi(argv[1]), 1, false); return 0; }
    numerics();
    for (int stage = 0; stage < 8; ++stage) cancellation(stage, 1, false);
    cancellation(int(Work::Convolution), 1, true);
#ifdef _OPENMP
    cancellation(int(Work::Convolution), 2, false);
    cancellation(int(Work::Convolution), 2, true);
#else
    std::puts("OpenMP unavailable: parallel acceptance NOT RUN");
#endif
    Frontend f;
    Progress p; p.stop = std::make_shared<RunStopState>(); p.stop->cancel();
    Frontend::set_progress(progress, &p);
    try { f.prepare(input(64, 2), 48000, 2, 16000, 1, p.stop); require(false, "pre-cancel throws"); }
    catch (const engine::runtime::RunCancelled &) {}
    for (auto & n : p.units) require(n.load() == 0, "pre-cancel zero work");
    Frontend::set_progress(nullptr, nullptr);
    ExecutionContext context(BackendConfig{});
    require(!context.current_run_stop(), "unbound accessor empty");
    auto a = std::make_shared<RunStopState>(); RunStopHandle retained;
    { auto binding = context.begin_cancellable_run(a); retained = context.current_run_stop(); require(retained == a, "actual owned stop handle"); }
    require(!context.current_run_stop() && retained == a, "retained accessor copy lifetime");
    for (auto rates : {std::pair<int,int>{INT_MAX,1},{0,48000},{48000,0},{-1,1}}) {
        try { f.prepare(input(1,1), rates.first, 1, rates.second, 1); require(false, "invalid/overflow config rejected"); }
        catch (const std::runtime_error & e) { require(std::strlen(e.what()) > 0, "clear arithmetic error"); }
    }
    // Extreme VALID equal rates and severe upsampling with zero output stay valid.
    require(f.prepare(input(3,1), INT_MAX, 1, INT_MAX, 1) == input(3,1), "valid extreme equal rates");
    require(f.prepare({}, INT_MAX, 1, 1, 1).empty(), "empty bypass does not invent limits");
    std::puts("frontend frozen numerics, active bounded work and recovery PASS");
}
