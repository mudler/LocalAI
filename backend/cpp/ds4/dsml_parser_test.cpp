// SPDX-License-Identifier: MIT
// Standalone regression tests for the DSML streaming parser.
//
// The repository's backend/cpp/run-unit-tests.sh harness compiles each
// *_test.cpp as a single translation unit, so include the implementation here.

#include "dsml_parser.cpp"

#include <cstdio>
#include <string>
#include <type_traits>
#include <vector>

namespace {

struct ParsedText {
    std::string content;
    std::string reasoning;
};

int failures = 0;

void check_equal(const std::string &got, const std::string &want,
                 const char *name) {
    if (got == want) return;
    std::fprintf(stderr, "FAIL %s: got \"%s\", want \"%s\"\n",
                 name, got.c_str(), want.c_str());
    failures++;
}

void collect_text(const std::vector<ds4cpp::ParserEvent> &events,
                  ParsedText *parsed) {
    for (const auto &event : events) {
        if (event.type == ds4cpp::ParserEvent::CONTENT) {
            parsed->content += event.text;
        } else if (event.type == ds4cpp::ParserEvent::REASONING) {
            parsed->reasoning += event.text;
        }
    }
}

ParsedText parse_chunks(ds4cpp::DsmlParser *parser,
                        const std::vector<std::string> &chunks) {
    ParsedText parsed;
    for (const auto &chunk : chunks) {
        std::vector<ds4cpp::ParserEvent> events;
        parser->Feed(chunk, events);
        collect_text(events, &parsed);
    }
    std::vector<ds4cpp::ParserEvent> events;
    parser->Flush(events);
    collect_text(events, &parsed);
    return parsed;
}

template <typename Parser>
void test_reasoning_opened_by_prompt() {
    if constexpr (!std::is_constructible_v<Parser, bool>) {
        std::fprintf(stderr,
                     "FAIL reasoning_opened_by_prompt: parser cannot start in thinking state\n");
        failures++;
    } else {
        Parser parser(true);
        ParsedText parsed = parse_chunks(
            &parser,
            {"We need to calculate factorial recursively.</think>Here is the answer."});
        check_equal(parsed.reasoning,
                    "We need to calculate factorial recursively.",
                    "reasoning_opened_by_prompt:reasoning");
        check_equal(parsed.content, "Here is the answer.",
                    "reasoning_opened_by_prompt:content");
    }
}

template <typename Parser>
Parser text_parser() {
    if constexpr (std::is_constructible_v<Parser, bool>) {
        return Parser(false);
    } else {
        return Parser();
    }
}

void test_reasoning_disabled() {
    auto parser = text_parser<ds4cpp::DsmlParser>();
    ParsedText parsed = parse_chunks(&parser, {"Here is the answer."});
    check_equal(parsed.reasoning, "", "reasoning_disabled:reasoning");
    check_equal(parsed.content, "Here is the answer.",
                "reasoning_disabled:content");
}

void test_explicit_think_tag() {
    auto parser = text_parser<ds4cpp::DsmlParser>();
    ParsedText parsed = parse_chunks(
        &parser, {"<think>reasoning</think>answer"});
    check_equal(parsed.reasoning, "reasoning", "explicit_think_tag:reasoning");
    check_equal(parsed.content, "answer", "explicit_think_tag:content");
}

template <typename Parser>
void test_split_think_close_marker() {
    if constexpr (!std::is_constructible_v<Parser, bool>) {
        std::fprintf(stderr,
                     "FAIL split_think_close_marker: parser cannot start in thinking state\n");
        failures++;
    } else {
        Parser parser(true);
        ParsedText parsed = parse_chunks(
            &parser,
            {"We need ", "to calculate ", "factorial", "</thi", "nk>",
             "Here is ", "the answer."});
        check_equal(parsed.reasoning, "We need to calculate factorial",
                    "split_think_close_marker:reasoning");
        check_equal(parsed.content, "Here is the answer.",
                    "split_think_close_marker:content");
    }
}

} // namespace

int main() {
    test_reasoning_opened_by_prompt<ds4cpp::DsmlParser>();
    test_reasoning_disabled();
    test_explicit_think_tag();
    test_split_think_close_marker<ds4cpp::DsmlParser>();

    if (failures == 0) {
        std::fprintf(stderr, "all dsml_parser checks passed\n");
        return 0;
    }
    std::fprintf(stderr, "%d check(s) failed\n", failures);
    return 1;
}
