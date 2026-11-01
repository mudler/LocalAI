// Graph-only runtime tests; host DSP/session cancellation is not established.
#include "engine/models/sheetsage/runtime.h"
#include "engine/models/sheetsage/session.h"
#include <iostream>
#include <type_traits>
#include <map>
#include <cstring>
#include <cmath>
#include <algorithm>

using namespace engine;
static_assert(std::is_base_of_v<runtime::ICancellableOfflineVoiceTaskSession,
                              models::sheetsage::SheetSage2Session>);
static_assert(!std::is_base_of_v<runtime::ICancellableOfflineVoiceTaskSession,
                               runtime::IOfflineVoiceTaskSession>);

// Deliberately unavailable tensors: this tests pre-load refusal, NOT graph execution.
class UnavailableSource final : public assets::TensorSource {
public:
    mutable int reads = 0;
    std::filesystem::path path;
    const std::filesystem::path & source_path() const noexcept override { return path; }
    bool has_tensor(std::string_view) const noexcept override { return false; }
    assets::TensorMetadata require_metadata(std::string_view) const override { ++reads; throw std::runtime_error("unavailable tensor"); }
    std::vector<assets::TensorMetadata> tensors() const override { return {}; }
    assets::RawTensorData require_tensor_data(std::string_view) const override { ++reads; throw std::runtime_error("unavailable tensor"); }
    std::vector<float> require_f32(std::string_view, const std::optional<std::vector<int64_t>> &) const override { ++reads; throw std::runtime_error("unavailable tensor"); }
    std::optional<std::vector<float>> optional_f32(std::string_view, const std::optional<std::vector<int64_t>> &) const override { return std::nullopt; }
    int64_t require_i64_scalar(std::string_view) const override { ++reads; throw std::runtime_error("unavailable tensor"); }
};


namespace engine::core {
using AbortLookupForTest = void * (*)(ggml_backend_reg_t, const char *);
AbortLookupForTest exchange_abort_lookup_for_test(AbortLookupForTest);
}

static void require(bool ok, const char * message) {
    if (!ok) throw std::runtime_error(message);
}

// The registry still supplies the real setter. The installed wrapper delegates
// every decision to the original ExecutionContext callback, never fabricates one.
struct CallbackProbe {
    using Setter = void (*)(ggml_backend_t, ggml_abort_callback, void *);
    Setter setter = nullptr;
    ggml_abort_callback original = nullptr;
    void * original_data = nullptr;
    size_t nodes = 0, cancel_at = 0, true_returns = 0;
    core::RunStopHandle stop;
    static CallbackProbe * installing;
    static bool callback(void * data) noexcept {
        auto & self = *static_cast<CallbackProbe *>(data);
        ++self.nodes; // Actual CPU callback is after a completed nonempty node.
        if (self.cancel_at && self.nodes == self.cancel_at) self.stop->cancel();
        const bool abort = self.original(self.original_data);
        self.true_returns += abort;
        return abort;
    }
    static void install(ggml_backend_t backend, ggml_abort_callback callback, void * data) {
        auto & self = *installing;
        self.original = callback;
        self.original_data = data;
        self.setter(backend, CallbackProbe::callback, &self);
    }
    static void * lookup(ggml_backend_reg_t reg, const char * name) {
        void * actual = ggml_backend_reg_get_proc_address(reg, name);
        if (std::string_view(name) != "ggml_backend_set_abort_callback" || !actual) return actual;
        installing->setter = reinterpret_cast<Setter>(actual);
        return reinterpret_cast<void *>(install);
    }
    void reset(core::RunStopHandle handle = {}, size_t at = 0) {
        nodes = true_returns = 0; stop = std::move(handle); cancel_at = at;
    }
};
CallbackProbe * CallbackProbe::installing = nullptr;
struct ScopedProbeInstall {
    core::AbortLookupForTest previous;
    explicit ScopedProbeInstall(CallbackProbe & probe) {
        CallbackProbe::installing = &probe;
        previous = core::exchange_abort_lookup_for_test(CallbackProbe::lookup);
    }
    ~ScopedProbeInstall() {
        core::exchange_abort_lookup_for_test(previous);
        CallbackProbe::installing = nullptr;
    }
};

// Explicit named tensors, not a source that invents whatever shape is requested.
// Each weight has deterministic nonconstant values; norm scales are near one.
class DecoderSource final : public assets::TensorSource {
    struct Entry { assets::TensorMetadata metadata; std::vector<float> values; };
    std::map<std::string, Entry, std::less<>> entries;
    std::filesystem::path path;
    void add(const std::string & name, std::vector<int64_t> shape, bool scale = false) {
        size_t count = 1;
        for (auto dim : shape) count *= static_cast<size_t>(dim);
        uint32_t seed = 2166136261u;
        for (unsigned char ch : name) seed = (seed ^ ch) * 16777619u;
        std::vector<float> values(count);
        for (size_t i = 0; i < count; ++i) {
            seed = seed * 1664525u + 1013904223u;
            values[i] = (scale ? 1.0f : 0.0f) + (static_cast<int>((seed >> 16) % 101) - 50) * 0.002f;
        }
        entries.emplace(name, Entry{{name, "F32", std::move(shape)}, std::move(values)});
    }
    void norm(const std::string & name, int64_t hidden) {
        add(name + ".weight", {hidden}, true); add(name + ".bias", {hidden});
    }
    void linear(const std::string & name, int64_t out, int64_t in) {
        add(name + ".weight", {out, in}); add(name + ".bias", {out});
    }
public:
    explicit DecoderSource(const models::sheetsage::SheetSage2DecoderConfig & c) {
        add("token_embedding.weight", {c.vocab_size, c.hidden_size});
        add("decoder.embed_positions.weight", {c.max_position_embeddings + 2, c.hidden_size});
        norm("decoder.layernorm_embedding", c.hidden_size);
        linear("encoder_projection", c.hidden_size, c.encoder_hidden_size);
        for (int64_t i = 0; i < c.decoder_layers; ++i) {
            const auto prefix = "decoder.layers." + std::to_string(i);
            for (const auto * attn : {"self_attn", "encoder_attn"}) {
                for (const auto * proj : {"q_proj", "k_proj", "v_proj", "out_proj"})
                    linear(prefix + "." + attn + "." + proj, c.hidden_size, c.hidden_size);
                norm(prefix + "." + attn + "_layer_norm", c.hidden_size);
            }
            linear(prefix + ".fc1", c.intermediate_size, c.hidden_size);
            linear(prefix + ".fc2", c.hidden_size, c.intermediate_size);
            norm(prefix + ".final_layer_norm", c.hidden_size);
        }
    }
    const std::filesystem::path & source_path() const noexcept override { return path; }
    bool has_tensor(std::string_view name) const noexcept override { return entries.find(name) != entries.end(); }
    assets::TensorMetadata require_metadata(std::string_view name) const override { return entries.at(std::string(name)).metadata; }
    std::vector<assets::TensorMetadata> tensors() const override {
        std::vector<assets::TensorMetadata> result;
        for (const auto & entry : entries) result.push_back(entry.second.metadata);
        return result;
    }
    assets::RawTensorData require_tensor_data(std::string_view name) const override {
        const auto & e = entries.at(std::string(name));
        assets::RawTensorData result{e.metadata, std::vector<std::byte>(e.values.size() * sizeof(float))};
        std::memcpy(result.bytes.data(), e.values.data(), result.bytes.size()); return result;
    }
    std::vector<float> require_f32(std::string_view name, const std::optional<std::vector<int64_t>> & expected) const override {
        const auto & e = entries.at(std::string(name));
        require(!expected || *expected == e.metadata.shape, "fixture shape mismatch"); return e.values;
    }
    std::optional<std::vector<float>> optional_f32(std::string_view name, const std::optional<std::vector<int64_t>> & expected) const override {
        if (!has_tensor(name)) return std::nullopt;
        return require_f32(name, expected);
    }
    int64_t require_i64_scalar(std::string_view) const override { throw std::runtime_error("no scalar tensor"); }
};

static void same(const std::vector<float> & a, const std::vector<float> & b, const char * label) {
    require(!a.empty() && a.size() == b.size(), "empty/mismatched numerical output");
    float error = 0;
    for (size_t i = 0; i < a.size(); ++i) {
        require(std::isfinite(a[i]) && std::isfinite(b[i]), "nonfinite logits");
        error = std::max(error, std::abs(a[i] - b[i]));
        require(std::abs(a[i] - b[i]) <= 1e-5f + 1e-5f * std::abs(a[i]), label);
    }
    std::cout << label << " values=" << a.size() << " max_abs=" << error << '\n';
}

static void decoder_graph_tests() {
    using namespace models::sheetsage;
    SheetSage2DecoderConfig c;
    c.vocab_size = 16; c.hidden_size = 8; c.encoder_hidden_size = 8;
    c.intermediate_size = 16; c.decoder_layers = 2;
    c.num_attention_heads = 2; c.max_position_embeddings = 16;
    SheetSage2DecoderRuntimeOptions options;
    // Fixed 524288-node graph capacities (three in cached runtime) dominate
    // metadata even with tiny tensors. 32 MiB was insufficient; bound at 160 MiB.
    options.graph_arena_bytes = 160 * 1024 * 1024;
    options.weight_context_bytes = 2 * 1024 * 1024;
    auto source = std::make_shared<DecoderSource>(c);
    core::BackendConfig config; config.type = core::BackendType::Cpu; config.threads = 1;
    CallbackProbe probe;
    ScopedProbeInstall install(probe);
    core::ExecutionContext execution(config);
    require(execution.supports_cancellation() && probe.original && probe.setter, "real callback not installed");
    SheetSage2DecoderRuntime decoder(source, execution, c, options);
    std::vector<float> memory(3 * c.encoder_hidden_size);
    for (size_t i = 0; i < memory.size(); ++i) memory[i] = (static_cast<int>(i % 13) - 6) * 0.03f;
    const std::vector<int32_t> tokens{2, 4, 6};
    probe.reset();
    const auto full = decoder.decode_logits(memory, 3, tokens);
    const auto full_nodes = probe.nodes;
    const auto range = std::minmax_element(full.begin(), full.end());
    require(*range.second - *range.first > 0.01f, "degenerate fixture logits");
    require(full_nodes > 4, "full graph did not execute nodes");
    auto b = std::make_shared<core::RunStopState>();
    auto old = std::make_shared<core::RunStopState>();
    {
        auto binding = execution.begin_cancellable_run(b);
        old->cancel(); old->cancel();
        probe.reset();
        same(full, decoder.decode_logits(memory, 3, tokens), "full legacy/bound");
        require(probe.nodes == full_nodes, "bound full graph progress differs");
    }
    probe.reset(); decoder.reset_cached_decode(memory, 3, 8);
    const auto cross_nodes = probe.nodes;
    probe.reset(); const auto cached = decoder.prefill_cached_decode(tokens);
    const auto prefill_nodes = probe.nodes;
    probe.reset(); const auto next = decoder.decode_cached_step(7);
    const auto step_nodes = probe.nodes;
    probe.reset(); decoder.reset_cached_decode(memory, 3, 8);
    probe.reset(); decoder.prefill_cached_decode({2});
    const auto one_step_nodes = probe.nodes;
    require(one_step_nodes == step_nodes && prefill_nodes > one_step_nodes, "state-only prefill not exercised");
    const auto state_nodes = (prefill_nodes - one_step_nodes) / 2;
    require(state_nodes > 2 && prefill_nodes == 2 * state_nodes + one_step_nodes, "state-only progress baseline");
    std::cout << "baseline full=" << full_nodes << " cross=" << cross_nodes
              << " state_only=" << state_nodes << " logits_step=" << step_nodes << '\n';
    // Independently compare the last full-sequence logits to cached output.
    std::vector<float> last(full.end() - c.vocab_size, full.end());
    same(last, cached, "full/cached last logits");
    {
        auto binding = execution.begin_cancellable_run(b);
        probe.reset(); decoder.reset_cached_decode(memory, 3, 8);
        require(probe.nodes == cross_nodes, "bound cross graph progress differs");
        probe.reset(); same(cached, decoder.prefill_cached_decode(tokens), "prefill legacy/bound");
        require(probe.nodes == prefill_nodes, "bound prefill graph progress differs");
        probe.reset(); same(next, decoder.decode_cached_step(7), "cached step legacy/bound");
        require(probe.nodes == step_nodes, "bound step graph progress differs");
    }
    // Each cancellation is triggered after the second real completed CPU node.
    // Baseline callback counts prove later compute exists, not just input setup.
    for (int path = 0; path < 4; ++path) {
        probe.reset();
        if (path >= 2) decoder.reset_cached_decode(memory, 3, 8);
        if (path == 3) decoder.prefill_cached_decode(tokens);
        const size_t baseline = path == 0 ? full_nodes : path == 1 ? cross_nodes : path == 2 ? state_nodes : step_nodes;
        require(baseline > 2, "no later graph work");
        auto stop = std::make_shared<core::RunStopState>();
        bool cancelled = false;
        {
            auto binding = execution.begin_cancellable_run(stop);
            probe.reset(stop, 2);
            try {
                if (path == 0) decoder.decode_logits(memory, 3, tokens);
                else if (path == 1) decoder.reset_cached_decode(memory, 3, 8);
                else if (path == 2) decoder.prefill_cached_decode(tokens);
                else decoder.decode_cached_step(7);
            } catch (const runtime::RunCancelled &) { cancelled = true; }
            require(cancelled && probe.nodes == 2 && probe.true_returns == 1 && stop->cancelled(), "active abort not observed");
        }
        std::cout << "active decoder path=" << path << " completed_callbacks=" << probe.nodes
                  << " baseline_callbacks=" << baseline << " original_abort_true=" << probe.true_returns << '\n';
        probe.reset();
        bool invalid = false;
        try { decoder.decode_cached_step(7); }
        catch (const runtime::RunCancelled &) { throw std::runtime_error("unbound failure classified as abort"); }
        catch (const std::runtime_error &) { invalid = true; }
        require(invalid, "abort retained cached graph");
        decoder.reset_cached_decode(memory, 3, 8);
        auto recovered = decoder.prefill_cached_decode(tokens);
        SheetSage2DecoderRuntime fresh(source, execution, c, options);
        fresh.reset_cached_decode(memory, 3, 8);
        same(fresh.prefill_cached_decode(tokens), recovered, "abort/reset/fresh prefill");
        same(fresh.decode_cached_step(7), decoder.decode_cached_step(7), "abort/reset/fresh step");
        same(full, decoder.decode_logits(memory, 3, tokens), "abort/rebuild full");
    }
    std::cout << "Decoder numerical/active-graph checkpoint PASS; encoder and complete offline remain unproved\n";
}

int main() {
    try {
        core::BackendConfig config;
        config.type = core::BackendType::Cpu;
        config.threads = 1;
        core::ExecutionContext execution(config);
        if (!execution.supports_cancellation()) throw std::runtime_error("CPU cancellation unavailable");
        // A null source must fail normally, not masquerade as cancellation.
        bool failed = false;
        try {
            models::sheetsage::SheetSage2DecoderRuntime decoder(nullptr, execution);
        } catch (const runtime::RunCancelled &) {
            throw std::runtime_error("load failure misclassified as cancellation");
        } catch (const std::runtime_error &) { failed = true; }
        if (!failed) throw std::runtime_error("null tensor source accepted");
        auto source = std::make_shared<UnavailableSource>();
        models::sheetsage::SheetSage2DecoderRuntime decoder(source, execution);
        models::sheetsage::Mert2EncoderRuntime encoder(source, execution);
        auto a = std::make_shared<core::RunStopState>();
        a->cancel();
        a->cancel();
        {
            auto binding = execution.begin_cancellable_run(a);
            if (!execution.stop_requested()) throw std::runtime_error("pre-stop lost");
            int cancelled = 0;
            try { decoder.decode_logits({}, 0, {}); } catch (const runtime::RunCancelled &) { ++cancelled; }
            try { decoder.reset_cached_decode({}, 0, 0); } catch (const runtime::RunCancelled &) { ++cancelled; }
            try { decoder.prefill_cached_decode({2, 3}); } catch (const runtime::RunCancelled &) { ++cancelled; }
            try { decoder.decode_cached_step(2); } catch (const runtime::RunCancelled &) { ++cancelled; }
            try { encoder.encode_mel({}, 0); } catch (const runtime::RunCancelled &) { ++cancelled; }
            if (cancelled != 5 || source->reads != 0) throw std::runtime_error("pre-stop reached loading/validation");
            bool nested = false;
            try { auto duplicate = execution.begin_cancellable_run(a); }
            catch (const std::logic_error &) { nested = true; }
            if (!nested) throw std::runtime_error("nested binding accepted");
        }
        auto b = std::make_shared<core::RunStopState>();
        {
            auto binding = execution.begin_cancellable_run(b);
            a->cancel();
            if (execution.stop_requested()) throw std::runtime_error("old handle stopped new binding");
        }
        decoder_graph_tests();
        return 0;
    } catch (const std::exception & error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
