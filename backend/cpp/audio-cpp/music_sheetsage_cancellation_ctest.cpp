// Graph-only runtime tests; host DSP/session cancellation is not established.
#include "engine/models/sheetsage/runtime.h"
#include "engine/models/sheetsage/session.h"
#include <iostream>
#include <type_traits>
#include <map>
#include <cstring>
#include <cmath>
#include <algorithm>
#include <sys/wait.h>
#include <unistd.h>
#include <csignal>
#include <ggml-alloc.h>
#include "engine/framework/core/backend.h"

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
class DecoderSource : public assets::TensorSource {
protected:
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
    DecoderSource() = default;
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

// Shapes mirror load_encoder_weights: fixed 128/512/1024 and 3/4/5.
class EncoderSource final : public DecoderSource {
public:
    explicit EncoderSource(const models::sheetsage::SheetSage2DecoderConfig & c) : DecoderSource() {
        add("feature_extractor.mel_mean", {128});
        add("feature_extractor.mel_std", {128}, true);
        add("layer_weight", {c.encoder_layers + 1});
        const int64_t channels[] = {128, 512, 1024}, inputs[] = {128, 128, 512}, depths[] = {3, 4, 5};
        for (int b = 0; b < 3; ++b) {
            const std::string p = "subsampling_module." + std::to_string(b);
            if (b) {
                norm(p + ".resampling_layer.0", inputs[b]);
                add(p + ".resampling_layer.2.weight", {channels[b], inputs[b], 2});
                add(p + ".resampling_layer.2.bias", {channels[b]});
            }
            for (int l = 0; l < depths[b]; ++l) {
                const auto q = p + ".convnext_layers." + std::to_string(l);
                const auto h = channels[b];
                add(q + ".depthwise_block.1.weight", {h, 1, 7});
                add(q + ".depthwise_block.1.bias", {h});
                norm(q + ".pointwise_block.0", h);
                linear(q + ".pointwise_block.1", h * 4, h);
                add(q + ".pointwise_block.3.weight", {1, 1, h * 4});
                add(q + ".pointwise_block.3.bias", {1, 1, h * 4});
                linear(q + ".pointwise_block.4", h, h * 4);
            }
        }
        const auto h = c.encoder_hidden_size;
        for (int l = 0; l < c.encoder_layers; ++l) {
            const auto p = "layers." + std::to_string(l);
            for (const auto * n : {"ffn1_layer_norm", "attn_layer_norm", "conv_module.layer_norm", "conv_module.conv_block.4.1", "ffn2_layer_norm", "final_layer_norm"}) norm(p + "." + n, h);
            for (const auto * f : {"ffn1", "ffn2"}) {
                linear(p + "." + f + ".w_1", c.encoder_intermediate_size, h);
                linear(p + "." + f + ".w_2", h, c.encoder_intermediate_size);
            }
            for (const auto * a : {"query_proj", "key_proj", "value_proj", "out_proj"}) linear(p + ".attn." + a, h, h);
            add(p + ".conv_module.conv_block.1.weight", {h * 2, h, 1});
            add(p + ".conv_module.conv_block.3.weight", {h, 1, c.conformer_conv_kernel_size});
            add(p + ".conv_module.conv_block.6.weight", {h, h, 1});
        }
        size_t bytes = 0;
        for (auto & e : entries) {
            bytes += e.second.values.size() * sizeof(float);
            // Large real matrices need bounded magnitudes, not zero weights.
            if (e.second.metadata.shape.size() > 1)
                for (auto & value : e.second.values) value *= 0.02f;
        }
        std::cout << "encoder F32 fixture bytes=" << bytes << std::endl;
        require(bytes < 320 * 1024 * 1024, "encoder fixture weight budget exceeded");
    }
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

namespace engine::models::sheetsage {
ggml_tensor * sheetsage_grn_for_test(ggml_context *, ggml_tensor *, ggml_tensor *, ggml_tensor *, ggml_tensor *, int64_t, int64_t);
}

static void grn_reference_tests() {
    core::BackendConfig config; config.type = core::BackendType::Cpu; config.threads = 1;
    core::ExecutionContext execution(config);
    // Include singleton time at normal first-block width (128*4) and
    // non-singleton time. We inspect the real GRN reduction, not a fake node.
    for (const int64_t frames : {1, 4, 7500}) {
        const int64_t channels = frames == 7500 ? 4096 : 512;
        auto * ctx = ggml_init({2 * 1024 * 1024, nullptr, true});
        require(ctx, "GRN context allocation");
        auto * input = ggml_new_tensor_3d(ctx, GGML_TYPE_F32, channels, frames, 1);
        auto * eps = ggml_new_tensor_3d(ctx, GGML_TYPE_F32, 1, 1, 1);
        auto * weight = ggml_new_tensor_3d(ctx, GGML_TYPE_F32, channels, 1, 1);
        auto * bias = ggml_new_tensor_3d(ctx, GGML_TYPE_F32, channels, 1, 1);
        auto * output = models::sheetsage::sheetsage_grn_for_test(ctx, input, eps, weight, bias, frames, channels);
        auto * graph = ggml_new_graph(ctx); ggml_build_forward_expand(graph, output);
        if (frames == 7500) {
            // Normal default last-block GRN shape: graph/strides only, no
            // large activation compute needed to verify the layout contract.
            for (int i = 0; i < ggml_graph_n_nodes(graph); ++i) {
                auto * node = ggml_graph_node(graph, i);
                if (node->op == GGML_OP_SUM_ROWS) {
                    std::cout << "GRN normal-default T=7500 C=4096 axis0_stride=" << node->src[0]->nb[0] << std::endl;
                    require(node->src[0]->nb[0] == sizeof(float), "normal GRN stride invalid");
                }
            }
            ggml_free(ctx); continue;
        }
        auto * alloc = ggml_gallocr_new(ggml_backend_get_default_buffer_type(execution.backend()));
        require(ggml_gallocr_alloc_graph(alloc, graph), "GRN buffer allocation");
        std::vector<float> x(frames * channels), w(channels), b(channels), expected(x.size());
        std::vector<double> magnitude(channels);
        for (size_t i = 0; i < x.size(); ++i) x[i] = (static_cast<int>(i % 31) - 15) * 0.03f;
        double mean = 0;
        for (int64_t c = 0; c < channels; ++c) {
            w[c] = 0.02f * (c % 7 - 3); b[c] = 0.01f * (c % 5 - 2);
            for (int64_t f = 0; f < frames; ++f) magnitude[c] += double(x[f * channels + c]) * x[f * channels + c];
            magnitude[c] = std::sqrt(magnitude[c]); mean += magnitude[c] / channels;
        }
        for (int64_t f = 0; f < frames; ++f)
            for (int64_t c = 0; c < channels; ++c)
                expected[f * channels + c] = x[f * channels + c] * magnitude[c] / (mean + 1e-6f) * w[c] + b[c] + x[f * channels + c];
        const float epsilon = 1e-6f;
        ggml_backend_tensor_set(input, x.data(), 0, x.size() * sizeof(float));
        ggml_backend_tensor_set(eps, &epsilon, 0, sizeof(float));
        ggml_backend_tensor_set(weight, w.data(), 0, w.size() * sizeof(float));
        ggml_backend_tensor_set(bias, b.data(), 0, b.size() * sizeof(float));
        for (int i = 0; i < ggml_graph_n_nodes(graph); ++i) {
            auto * node = ggml_graph_node(graph, i);
            if (node->op == GGML_OP_SUM_ROWS) {
                auto * a = node->src[0];
                std::cout << "GRN frames=" << frames << " SUM_ROWS ne=" << a->ne[0] << "," << a->ne[1]
                          << " nb=" << a->nb[0] << "," << a->nb[1] << " contiguous=" << ggml_is_contiguous(a) << std::endl;
                require(a->nb[0] == sizeof(float), "GRN reduction axis-zero stride invalid");
            }
        }
        require(core::compute_backend_graph(execution, graph, nullptr, "sheetsage.grn.reference") == GGML_STATUS_SUCCESS, "GRN compute");
        std::vector<float> actual(x.size()); ggml_backend_tensor_get(output, actual.data(), 0, actual.size() * sizeof(float));
        same(expected, actual, "GRN independent scalar reference");
        ggml_gallocr_free(alloc); ggml_free(ctx);
    }
}

static void encoder_graph_tests() {
    using namespace models::sheetsage;
    SheetSage2DecoderConfig c;
    // No projection after fixed subsampling: hidden must remain 1024.
    // Eight mel frames keeps two output frames: singleton convolution
    // layouts are not supported by the current CPU im2col path.
    c.encoder_layers = 1; c.encoder_hidden_size = 1024;
    c.encoder_attention_heads = 512; // smallest even RoPE head dimension: 2
    c.encoder_intermediate_size = 1; c.conformer_conv_kernel_size = 1;
    SheetSage2DecoderRuntimeOptions options;
    options.graph_arena_bytes = 16 * 1024 * 1024; // runtime multiplies by 8
    options.weight_context_bytes = 2 * 1024 * 1024; // encoder multiplies by 3
    // Matrix payload ~227 MiB, source+backend+fresh backend <1 GiB;
    // graph metadata 128 MiB each, activation buffers small at T=8.
    auto source = std::make_shared<EncoderSource>(c);
    core::BackendConfig config; config.type = core::BackendType::Cpu; config.threads = 1;
    CallbackProbe probe; ScopedProbeInstall install(probe);
    core::ExecutionContext execution(config);
    Mert2EncoderRuntime encoder(source, execution, c, options);
    std::vector<float> mel(8 * 128);
    for (size_t i = 0; i < mel.size(); ++i) mel[i] = (static_cast<int>(i % 29) - 14) * 0.03f;
    probe.reset(); const auto legacy = encoder.encode_mel(mel, 8);
    const auto baseline = probe.nodes;
    require(legacy.size() == 2048 && baseline > 2, "encoder shape/progress baseline");
    auto range = std::minmax_element(legacy.begin(), legacy.end());
    require(*range.second - *range.first > 0.001f, "degenerate encoder output");
    std::cout << "encoder baseline=" << baseline << " output range=" << *range.first << "," << *range.second << std::endl;
    auto old = std::make_shared<core::RunStopState>();
    { auto binding = execution.begin_cancellable_run(old); }
    auto live = std::make_shared<core::RunStopState>();
    {
        auto binding = execution.begin_cancellable_run(live);
        old->cancel(); probe.reset();
        same(legacy, encoder.encode_mel(mel, 8), "encoder legacy/bound old-handle isolation");
        require(probe.nodes == baseline, "encoder bound progress mismatch");
    }
    auto stop = std::make_shared<core::RunStopState>();
    {
        auto binding = execution.begin_cancellable_run(stop);
        probe.reset(stop, 2); bool cancelled = false;
        try { encoder.encode_mel(mel, 8); }
        catch (const runtime::RunCancelled &) { cancelled = true; }
        require(cancelled && probe.nodes == 2 && probe.true_returns == 1, "encoder real abort classification/progress");
        std::cout << "encoder abort completed=" << probe.nodes << " original_true=" << probe.true_returns << std::endl;
    }
    probe.reset();
    const auto recovered = encoder.encode_mel(mel, 8);
    require(probe.nodes == baseline, "encoder recovery progress");
    same(legacy, recovered, "encoder abort/rebuild");
    encoder.release_runtime_graphs();
    Mert2EncoderRuntime fresh(source, execution, c, options);
    probe.reset(); same(recovered, fresh.encode_mel(mel, 8), "encoder recovery/fresh");
    std::cout << "Encoder numerical/active-graph checkpoint PASS; host DSP/session loops remain unproved" << std::endl;
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
    std::cout << "Decoder numerical/active-graph checkpoint PASS; complete offline remains unproved\n";
}

// Isolate arithmetic regressions and bound both hangs and accidental signals.
static void empty_memory_test() {
    const pid_t child = fork();
    require(child >= 0, "fork failed");
    if (child == 0) {
        alarm(5);
        try {
            core::BackendConfig config; config.type = core::BackendType::Cpu; config.threads = 1;
            core::ExecutionContext execution(config);
            auto source = std::make_shared<UnavailableSource>();
            models::sheetsage::SheetSage2DecoderRuntime decoder(source, execution);
            try { decoder.decode_logits({}, 1, {2}); }
            catch (const runtime::RunCancelled &) { _exit(2); }
            catch (const std::invalid_argument &) { _exit(source->reads == 0 ? 0 : 3); }
        } catch (...) { _exit(4); }
        _exit(5);
    }
    int status = 0;
    require(waitpid(child, &status, 0) == child, "waitpid failed");
    std::cout << "empty-memory subprocess status=" << status << std::endl;
    require(WIFEXITED(status) && WEXITSTATUS(status) == 0, "empty-memory invalid_argument/zero-read regression");
}

int main() {
    try {
        empty_memory_test();
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
        grn_reference_tests();
        encoder_graph_tests();
        return 0;
    } catch (const std::exception & error) {
        std::cerr << error.what() << '\n';
        return 1;
    }
}
