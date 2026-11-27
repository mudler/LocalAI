#pragma once

#include <algorithm>
#include <cctype>
#include <optional>
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

} // namespace llama_grpc
