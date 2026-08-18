// Unit tests for model_options. Standard library only, so
// backend/cpp/run-unit-tests.sh picks this up with no engine checkout.
//
// The harness compiles this file as a single translation unit with no other
// sources, so the implementation is included directly rather than linked.
//
// Build and run standalone:
//   g++ -std=c++17 -I. model_options_test.cpp -o t && ./t

#include "model_options.cpp"

#include <cstdio>
#include <map>
#include <string>
#include <vector>

static int failures = 0;

static void check(bool ok, const std::string &name) {
    if (!ok) {
        failures++;
        fprintf(stderr, "FAIL: %s\n", name.c_str());
    } else {
        fprintf(stderr, "ok:   %s\n", name.c_str());
    }
}

// Returns the mapped value, or an empty string when the key is absent. map::at
// would throw on a miss and abort the whole binary, so one prefix off-by-one
// would hide every check that follows it instead of failing a single one.
static std::string lookup(const std::map<std::string, std::string> &values,
                          const std::string &key) {
    const auto found = values.find(key);
    return found == values.end() ? std::string() : found->second;
}

using audiocpp_backend::parse_model_options;

static void test_defaults() {
    auto r = parse_model_options({});
    check(r.error.empty(), "empty option list is not an error");
    check(r.options.family.empty(), "family defaults to empty");
    check(r.options.task.empty(), "task defaults to empty");
    check(r.options.backend == "cpu", "backend defaults to cpu");
    check(r.options.device == 0, "device defaults to 0");
    check(r.options.threads == 0, "threads defaults to 0");
    check(r.options.busy_timeout_ms == 0, "busy_timeout_ms defaults to 0");
    // NOT zero, unlike every other numeric option here. A live stream holds the
    // model's lane while it waits on the client, so the default has to bound
    // that wait; 0 is the explicit "no limit" the operator opts into.
    check(r.options.live_idle_timeout_ms == 30000,
          "live_idle_timeout_ms defaults to 30000");
    check(r.options.load_options.empty(), "load_options defaults empty");
    check(r.options.session_options.empty(), "session_options defaults empty");
}

static void test_scalar_options() {
    auto r = parse_model_options({
        "family:qwen3_tts",
        "task:tts",
        "backend:cuda",
        "device:1",
        "threads:8",
        "busy_timeout_ms:30000",
        "live_idle_timeout_ms:5000",
    });
    check(r.error.empty(), "scalar options parse without error");
    check(r.options.family == "qwen3_tts", "family parsed");
    check(r.options.task == "tts", "task parsed");
    check(r.options.backend == "cuda", "backend parsed");
    check(r.options.device == 1, "device parsed");
    check(r.options.threads == 8, "threads parsed");
    check(r.options.busy_timeout_ms == 30000, "busy_timeout_ms parsed");
    check(r.options.live_idle_timeout_ms == 5000, "live_idle_timeout_ms parsed");
    check(parse_model_options({"live_idle_timeout_ms:0"}).options.live_idle_timeout_ms == 0,
          "an explicit 0 turns the live idle limit off rather than reverting to "
          "the default");

    check(parse_model_options({"backend:hip"}).error.empty(),
          "HIP backend option is accepted");
    check(parse_model_options({"backend:rocm"}).error.empty(),
          "ROCm backend alias is accepted");
}

// Values containing colons must survive: split on the FIRST colon only.
static void test_value_containing_colon() {
    auto r = parse_model_options({"model_spec_override:/models/a:b/spec.json"});
    check(r.error.empty(), "colon-bearing value is not an error");
    check(r.options.model_spec_override == "/models/a:b/spec.json",
          "value keeps every colon after the first separator");
}

static void test_namespaced_options() {
    auto r = parse_model_options({
        "load.weight_type:q8_0",
        "session.miocodec.weight_type:f16",
        "session.graph_capacity:tiered",
    });
    check(r.error.empty(), "namespaced options parse without error");
    check(r.options.load_options.size() == 1, "one load option");
    check(lookup(r.options.load_options, "weight_type") == "q8_0", "load prefix stripped");
    check(r.options.session_options.size() == 2, "two session options");
    check(lookup(r.options.session_options, "miocodec.weight_type") == "f16",
          "session prefix stripped, inner dots kept");
    check(lookup(r.options.session_options, "graph_capacity") == "tiered",
          "second session option parsed");
}

static void test_errors() {
    check(!parse_model_options({"family"}).error.empty(),
          "entry without a colon is rejected");
    check(!parse_model_options({"nonsense:1"}).error.empty(),
          "unknown key is rejected");
    check(!parse_model_options({"device:abc"}).error.empty(),
          "non-numeric device is rejected");
    check(!parse_model_options({"threads:-1"}).error.empty(),
          "negative threads is rejected");
    check(!parse_model_options({"load.:x"}).error.empty(),
          "empty load key is rejected");
    check(!parse_model_options({"session.:x"}).error.empty(),
          "empty session key is rejected");
    check(!parse_model_options({"busy_timeout_ms:abc"}).error.empty(),
          "non-numeric busy_timeout_ms is rejected");
    check(!parse_model_options({"live_idle_timeout_ms:abc"}).error.empty(),
          "non-numeric live_idle_timeout_ms is rejected");
    check(!parse_model_options({"live_idle_timeout_ms:-1"}).error.empty(),
          "negative live_idle_timeout_ms is rejected");
    check(!parse_model_options({"device:-1"}).error.empty(),
          "negative device is rejected");
    check(!parse_model_options({"threads:x"}).error.empty(),
          "non-numeric threads is rejected");
    check(!parse_model_options({"backend:unknown"}).error.empty(),
          "unknown compute backend is rejected before model loading");

    // Values too large for int must be rejected, not silently wrapped into a
    // negative device index that then reaches the ggml backend selector.
    check(!parse_model_options({"device:2147483648"}).error.empty(),
          "device above INT_MAX is rejected");
    check(!parse_model_options({"threads:99999999999999"}).error.empty(),
          "threads above INT_MAX is rejected");

    // The error text must name the offending entry so a user can fix their YAML.
    const auto r = parse_model_options({"nonsense:1"});
    check(r.error.find("nonsense") != std::string::npos,
          "error names the offending key");

    // An empty key still has to give the user something to grep for.
    const auto empty_key = parse_model_options({":value"});
    check(empty_key.error.find(":value") != std::string::npos,
          "unknown-key error names the entry even when the key is empty");
}

static void test_blank_entries_ignored() {
    auto r = parse_model_options({"", "   ", "family:supertonic"});
    check(r.error.empty(), "blank entries are skipped, not rejected");
    check(r.options.family == "supertonic", "real entry still parsed");
}

int main() {
    test_defaults();
    test_scalar_options();
    test_value_containing_colon();
    test_namespaced_options();
    test_errors();
    test_blank_entries_ignored();
    if (failures) {
        fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    fprintf(stderr, "all model_options checks passed\n");
    return 0;
}
