// SPDX-License-Identifier: MIT
//
// Checks realtime_session.h against a real chat template. The standalone
// realtime_session_test.cpp uses a fake ChatML-like renderer; this one renders
// the LFM3 template that ships with llama.cpp (models/templates/LFM3.jinja),
// so a template change that breaks the turn format shows up here.
//
// It links the llama.cpp common library and takes the template path as its
// first argument, so it runs under ctest (-DLLAMA_GRPC_BUILD_TESTS=ON), not in
// backend/cpp/run-unit-tests.sh. The file name does not end in _test.cpp for
// that reason.
#include "realtime_session.h"

#include "chat.h"
#include "json.h"

#include <cstdio>
#include <fstream>
#include <sstream>

namespace rts = llama_grpc::realtime;

namespace {

int failures = 0;

void check(bool ok, const char * what) {
    if (!ok) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        ++failures;
    }
}

void check_eq(const std::string & got, const std::string & want, const char * what) {
    if (got != want) {
        std::fprintf(stderr, "FAIL: %s\n--- got\n%s\n--- want\n%s\n", what, got.c_str(), want.c_str());
        ++failures;
    }
}

const char * kTools = R"([{"type":"function","function":{"name":"get_time","description":"Get the time",
    "parameters":{"type":"object","properties":{"tz":{"type":"string"}},"required":["tz"]}}}])";

std::vector<common_chat_msg> to_common(const std::vector<rts::Msg> & msgs) {
    std::vector<common_chat_msg> out;
    for (const auto & m : msgs) {
        common_chat_msg c;
        c.role    = m.role;
        c.content = m.content;
        out.push_back(c);
    }
    return out;
}

} // namespace

int main(int argc, char ** argv) {
    if (argc < 2) {
        std::fprintf(stderr, "usage: %s <LFM3.jinja>\n", argv[0]);
        return 2;
    }
    std::ifstream f(argv[1]);
    if (!f) {
        std::fprintf(stderr, "cannot open %s\n", argv[1]);
        return 2;
    }
    std::stringstream ss;
    ss << f.rdbuf();
    common_chat_templates_ptr tmpls(common_chat_templates_init(nullptr, ss.str()));

    for (const bool with_tools : {false, true}) {
        std::vector<common_chat_tool> tools;
        if (with_tools) {
            tools = common_chat_tools_parse_oaicompat(common_json::parse(kTools));
        }
        auto base = [&]() {
            common_chat_templates_inputs in;
            in.use_jinja       = true;
            in.enable_thinking = false;
            in.tools           = tools;
            return in;
        };
        rts::RenderFn render = [&](const std::vector<rts::Msg> & msgs, bool gen) {
            auto in                  = base();
            in.messages              = to_common(msgs);
            in.add_generation_prompt = gen;
            return common_chat_templates_apply(tmpls.get(), in).prompt.str();
        };

        rts::TurnText t;
        std::string   err;
        check(rts::derive_turn_text(render, rts::default_instructions(), t, err), "LFM3 template fits the turn format");
        if (!err.empty()) {
            std::fprintf(stderr, "  %s\n", err.c_str());
        }
        check(t.has_tool_turn, "LFM3 renders a tool turn");
        if (with_tools) {
            check(t.first_header.find("<tools>") != std::string::npos || t.first_header.find("get_time") != std::string::npos,
                  "the first header lists the tools");
        }

        // what the slot sees must equal what the template renders for the whole conversation
        const auto sys = rts::Msg{"system", rts::default_instructions(), {}};
        auto       m   = [](const char * role, const char * content) { return rts::Msg{role, content, {}}; };
        const std::string slot_two_turns = t.first_header + "hi" + t.user_footer + t.assistant_header + "hello" + t.assistant_footer +
                                           t.user_header + "bye" + t.user_footer + t.assistant_header;
        check_eq(slot_two_turns, render({sys, m("user", "hi"), m("assistant", "hello"), m("user", "bye")}, true), "two user turns");

        const std::string slot_tool = t.first_header + "hi" + t.user_footer + t.assistant_header + "calling" + t.assistant_footer +
                                      t.tool_header + "12:00" + t.tool_footer + t.assistant_header_after_tool;
        check_eq(slot_tool, render({sys, m("user", "hi"), m("assistant", "calling"), m("tool", "12:00")}, true), "tool turn");
        std::printf("tools=%d\n  first_header=%s\n  tool_header=%s\n  tool_footer=%s\n  assistant_footer=%s\n", with_tools,
                    rts::json(t.first_header).dump().c_str(), rts::json(t.tool_header).dump().c_str(),
                    rts::json(t.tool_footer).dump().c_str(), rts::json(t.assistant_footer).dump().c_str());
    }

    // the tool-call block the model writes parses with the parser the template selects
    {
        common_chat_templates_inputs in;
        in.use_jinja             = true;
        in.enable_thinking       = false;
        in.add_generation_prompt = true;
        in.parallel_tool_calls   = true;
        in.reasoning_format      = COMMON_REASONING_FORMAT_AUTO; // as the server does
        in.tools                 = common_chat_tools_parse_oaicompat(common_json::parse(kTools));
        common_chat_msg user;
        user.role    = "user";
        user.content = "x";
        in.messages.push_back(user);
        common_chat_session session(tmpls.get(), nullptr, in);
        const std::string   text = "<tool_call>\n<function=get_time>\n<parameter=tz>\nUTC\n</parameter>\n</function>\n</tool_call>";
        const auto &         msg  = session.finish(common_chat_input(text));
        check(msg.tool_calls.size() == 1, "one tool call is parsed");
        if (msg.tool_calls.size() == 1) {
            check_eq(msg.tool_calls[0].name, "get_time", "tool name");
            const auto args = rts::json::parse(msg.tool_calls[0].arguments, nullptr, false);
            check(args.is_object() && args.value("tz", "") == "UTC", "tool arguments are a JSON object");
        }
        check(msg.content.empty(), ("no content around a bare call, got: " + rts::json(msg.content).dump()).c_str());

        // the same through the translator, with the text split into pieces and audio inside the block
        rts::ResponseTranslator tr(rts::OutMode::Auto, 24000, [&](const std::string & t) {
            common_chat_session s(tmpls.get(), nullptr, in);
            std::vector<rts::ToolCallMsg> calls;
            for (const auto & tc : s.finish(common_chat_input(t)).tool_calls) {
                calls.push_back({tc.name, tc.arguments, tc.id});
            }
            return calls;
        });
        std::vector<rts::OutEvent> all;
        for (size_t i = 0; i < text.size(); i += 7) {
            auto ev = tr.on_result(text.substr(i, 7), {0.1f}, false, false, 1);
            all.insert(all.end(), ev.begin(), ev.end());
        }
        auto ev = tr.on_result("", {}, true, false, 9);
        all.insert(all.end(), ev.begin(), ev.end());
        size_t calls = 0, transcript = 0;
        for (const auto & e : all) {
            calls += e.event == "response.function_call_arguments.done";
            transcript += e.event == "response.audio_transcript.delta";
        }
        check(calls == 1, "translator emits the parsed call");
        check(transcript == 0, "the call is not in the transcript");
        check(all.back().event == "response.done", "response.done is last");
    }

    if (failures) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    std::puts("realtime_chat_check: all checks passed");
    return 0;
}
