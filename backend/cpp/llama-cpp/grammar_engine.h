#pragma once

#include <algorithm>
#include <cctype>
#include <optional>
#include <cstdlib>
#include <string>

namespace llama_grpc {

// grammar_engine is the value of the grammar_engine model option. It selects
// the engine that constrains sampling for the grammars LocalAI builds from a
// JSON schema (response_format and tool calls).
enum class grammar_engine {
    automatic,  // llguidance when the build has it, GBNF otherwise
    gbnf,       // always llama.cpp's GBNF sampler
    llguidance, // llguidance; a build without it falls back to GBNF with a warning
};

// The metadata key of PredictOptions that carries the llguidance form of the
// grammar. LocalAI sets it next to the GBNF grammar only when it built that
// grammar from a JSON schema, so a grammar the user wrote stays GBNF.
constexpr const char * llguidance_grammar_metadata_key = "llguidance_grammar";

// parse_grammar_engine reads auto, gbnf and llguidance (llg is an alias).
// An unknown value returns nullopt.
inline std::optional<grammar_engine> parse_grammar_engine(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(),
                   [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
    if (value == "auto") {
        return grammar_engine::automatic;
    }
    if (value == "gbnf") {
        return grammar_engine::gbnf;
    }
    if (value == "llguidance" || value == "llg") {
        return grammar_engine::llguidance;
    }
    return std::nullopt;
}

// select_grammar returns the grammar the request samples with. gbnf is the
// grammar of the request, llg the llguidance form LocalAI sent with it (empty
// when there is none). The llguidance form is used only when the build has
// llguidance, the engine allows it and the request has a grammar to replace.
inline const std::string & select_grammar(grammar_engine engine, bool built_with_llguidance,
                                          const std::string & gbnf, const std::string & llg) {
    if (engine == grammar_engine::gbnf || !built_with_llguidance || gbnf.empty()) {
        return gbnf;
    }
    // llama.cpp routes a grammar to llguidance by this prefix; anything else
    // would be parsed as GBNF and fail.
    if (llg.rfind("%llguidance", 0) != 0) {
        return gbnf;
    }
    return llg;
}

// Default of grammar_max_repetition: the limit of llama.cpp's GBNF repetition
// check (upstream 2000). The check multiplies the size of a repeated group by
// its repetition count, so the grammar of a large tool set, one group with
// many alternatives under a `*`, goes over 2000 and the request fails with
// "number of rules that are going to be repeated ... exceeds sane defaults".
// 100000 is the value users of large tool sets report working.
constexpr unsigned long long grammar_default_max_repetition = 100000;

// grammar_max_repetition returns the value for LLAMA_GRAMMAR_MAX_REPETITION
// (read by patches/0005): the model option when it is a positive number,
// else the environment value when it is set, else the default. An empty
// result leaves the environment as it is.
inline std::string grammar_max_repetition(const std::string & option, const char * env) {
    if (!option.empty()) {
        char * end = nullptr;
        const unsigned long long v = std::strtoull(option.c_str(), &end, 10);
        if (end != option.c_str() && *end == '\0' && v > 0) {
            return std::to_string(v);
        }
    }
    if (env != nullptr && *env != '\0') {
        return "";
    }
    return std::to_string(grammar_default_max_repetition);
}

} // namespace llama_grpc
