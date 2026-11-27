#include "grammar_engine.h"

#include <cstdio>

using namespace llama_grpc;

static int failures = 0;

static void check(bool ok, const char * what) {
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        failures++;
    }
}

int main() {
    check(parse_grammar_engine("auto") == grammar_engine::automatic, "auto");
    check(parse_grammar_engine("AUTO") == grammar_engine::automatic, "AUTO");
    check(parse_grammar_engine("gbnf") == grammar_engine::gbnf, "gbnf");
    check(parse_grammar_engine("llguidance") == grammar_engine::llguidance, "llguidance");
    check(parse_grammar_engine("llg") == grammar_engine::llguidance, "llg");
    check(!parse_grammar_engine("").has_value(), "empty is unknown");
    check(!parse_grammar_engine("lark").has_value(), "lark is unknown");

    const std::string gbnf = "root ::= \"a\"";
    const std::string llg  = "%llguidance {}\nstart: %json {\"type\":\"object\"}";
    const std::string none;

    // The llguidance form replaces the GBNF grammar only in a build with
    // llguidance and when the engine is not gbnf.
    check(&select_grammar(grammar_engine::automatic, true, gbnf, llg) == &llg, "auto, built: llguidance");
    check(&select_grammar(grammar_engine::llguidance, true, gbnf, llg) == &llg, "llguidance, built: llguidance");
    check(&select_grammar(grammar_engine::gbnf, true, gbnf, llg) == &gbnf, "gbnf, built: gbnf");
    check(&select_grammar(grammar_engine::automatic, false, gbnf, llg) == &gbnf, "auto, not built: gbnf");
    check(&select_grammar(grammar_engine::llguidance, false, gbnf, llg) == &gbnf, "llguidance, not built: gbnf");

    // A user grammar comes without an llguidance form and stays GBNF.
    check(&select_grammar(grammar_engine::automatic, true, gbnf, none) == &gbnf, "no llguidance form: gbnf");
    // Without a grammar there is nothing to replace.
    check(&select_grammar(grammar_engine::automatic, true, none, llg) == &none, "no grammar: none");
    // A value that llama.cpp would not route to llguidance is ignored.
    check(&select_grammar(grammar_engine::automatic, true, gbnf, std::string("start: \"a\"")) != &llg, "no prefix: gbnf");

    // grammar_max_repetition: option, then environment, then the default.
    check(grammar_max_repetition("", nullptr) == "100000", "default when nothing is set");
    check(grammar_max_repetition("5000", nullptr) == "5000", "the option");
    check(grammar_max_repetition("5000", "300") == "5000", "the option wins over the environment");
    check(grammar_max_repetition("", "300").empty(), "the environment is kept");
    check(grammar_max_repetition("0", nullptr) == "100000", "0 is not a limit");
    check(grammar_max_repetition("abc", nullptr) == "100000", "garbage is ignored");

    if (failures == 0) {
        std::printf("grammar_engine_test: all checks passed\n");
        return 0;
    }
    return 1;
}
