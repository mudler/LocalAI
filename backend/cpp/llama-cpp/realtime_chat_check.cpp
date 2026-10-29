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
#include <memory>
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
        c.role         = m.role;
        c.content      = m.content;
        c.tool_call_id = m.tool_call_id;
        for (const auto & tc : m.tool_calls) {
            c.tool_calls.push_back({tc.name, tc.arguments, tc.id});
        }
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
        check(rts::derive_turn_text(render, rts::default_instructions(), with_tools ? "get_time" : "", t, err), "LFM3 template fits the turn format");
        if (!err.empty()) {
            std::fprintf(stderr, "  %s\n", err.c_str());
        }
        check(t.has_tool_turn == with_tools, "LFM3 renders a tool turn when the session has tools");
        if (with_tools) {
            check(t.first_header.find("<tools>") != std::string::npos || t.first_header.find("get_time") != std::string::npos,
                  "the first header lists the tools");
        }

        // what the slot sees must equal what the template renders for the whole conversation
        const auto sys = rts::Msg{"system", rts::default_instructions(), {}, ""};
        auto       m   = [](const char * role, const char * content) { return rts::Msg{role, content, {}, ""}; };
        const std::string slot_two_turns = t.first_header + "hi" + t.user_footer + t.assistant_header + "hello" + t.assistant_footer +
                                           t.user_header + "bye" + t.user_footer + t.assistant_header;
        check_eq(slot_two_turns, render({sys, m("user", "hi"), m("assistant", "hello"), m("user", "bye")}, true), "two user turns");

        if (with_tools) {
            // the response is the call the model wrote, the result of the tool follows
            const std::string call = "<tool_call>\n<function=get_time>\n</function>\n</tool_call>";
            const std::string slot_tool = t.first_header + "hi" + t.user_footer + t.assistant_header + call + t.assistant_footer +
                                          t.tool_header + "12:00" + t.tool_footer + t.assistant_header_after_tool;
            rts::Msg assistant{"assistant", "", {{"get_time", "{}", "call_0"}}, ""};
            rts::Msg result{"tool", "12:00", {}, "call_0"};
            check_eq(slot_tool, render({sys, m("user", "hi"), assistant, result}, true), "tool turn");
        }
        std::printf("tools=%d\n  first_header=%s\n  tool_header=%s\n  tool_footer=%s\n  assistant_footer=%s\n", with_tools,
                    rts::json(t.first_header).dump().c_str(), rts::json(t.tool_header).dump().c_str(),
                    rts::json(t.tool_footer).dump().c_str(), rts::json(t.assistant_footer).dump().c_str());
    }

    // the tool-call block the model writes goes through the grammar and the incremental parser of the template
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

        {
            common_chat_session session(tmpls.get(), nullptr, in);
            common_params_sampling sampling;
            session.apply_sampling(sampling);
            check(!sampling.grammar.grammar.empty(), "the template gives a grammar for the tools");
            check(!sampling.preserved_tokens.empty() || !sampling.grammar_triggers.empty(), "the template names the tokens of a call");
        }

        const std::string call = "<tool_call>\n<function=get_time>\n<parameter=tz>\nUTC\n</parameter>\n</function>\n</tool_call>";
        const std::string text = "One moment.\n" + call;

        // the splitter of grpc-server.cpp, on the session API only: the same diffs, fed piece by piece
        auto make_splitter = [&]() -> rts::ResponseTranslator::Splitter {
            auto session = std::make_shared<common_chat_session>(tmpls.get(), nullptr, in);
            auto prev    = std::make_shared<common_chat_msg>();
            return [session, prev](const std::string & piece, bool final) {
                rts::SplitResult out;
                const auto & msg = final ? session->finish(common_chat_input(piece)) : session->feed(common_chat_input(piece));
                for (const auto & d : common_chat_msg_diff::compute_diffs(*prev, msg)) {
                    out.content += d.content_delta;
                    if (d.tool_call_index != std::string::npos) {
                        out.calls.push_back({d.tool_call_index, d.tool_call_delta.id, d.tool_call_delta.name, d.tool_call_delta.arguments});
                    }
                }
                *prev = msg;
                return out;
            };
        };

        for (const size_t step : {size_t(7), size_t(1), text.size()}) {
            rts::ResponseTranslator tr(rts::OutMode::Auto, 24000, make_splitter());
            std::vector<rts::OutEvent> all;
            for (size_t i = 0; i < text.size(); i += step) {
                auto ev = tr.on_result(text.substr(i, step), {0.1f}, false, false, 1);
                all.insert(all.end(), ev.begin(), ev.end());
            }
            auto ev = tr.on_result("", {}, true, false, 9);
            all.insert(all.end(), ev.begin(), ev.end());

            std::string transcript, name, args;
            size_t      calls = 0;
            for (const auto & e : all) {
                if (e.event == "response.audio_transcript.delta") {
                    transcript += e.meta["delta"].get<std::string>();
                } else if (e.event == "response.function_call_arguments.done") {
                    calls++;
                    name = e.meta["name"].get<std::string>();
                    args = e.meta["arguments"].get<std::string>();
                }
            }
            const std::string label = "step " + std::to_string(step) + ": ";
            check(calls == 1, (label + "one tool call").c_str());
            check_eq(name, "get_time", (label + "tool name").c_str());
            const auto parsed = rts::json::parse(args, nullptr, false);
            check(parsed.is_object() && parsed.value("tz", "") == "UTC", (label + "tool arguments are a JSON object").c_str());
            check_eq(transcript, "One moment.", (label + "the call is not in the transcript").c_str());
            size_t audio_frames = 0;
            for (const auto & e : all) {
                if (e.event == "response.audio.delta") {
                    audio_frames++;
                }
            }
            check(audio_frames > 0, (label + "audio is sent while the call is written").c_str());
            check(all.back().event == "response.done", (label + "response.done is last").c_str());
            check(all.back().meta["raw_text"].get<std::string>() == text, (label + "response.done carries the raw text").c_str());
        }

        // a reply without a call is only content
        {
            rts::ResponseTranslator tr(rts::OutMode::Auto, 24000, make_splitter());
            auto ev = tr.on_result("It is noon.", {}, false, false, 1);
            auto fin = tr.on_result("", {}, true, false, 2);
            ev.insert(ev.end(), fin.begin(), fin.end());
            std::string transcript;
            for (const auto & e : ev) {
                check(e.event != "response.function_call_arguments.done", "no call in a plain reply");
                if (e.event == "response.audio_transcript.delta") {
                    transcript += e.meta["delta"].get<std::string>();
                }
            }
            check_eq(transcript, "It is noon.", "a plain reply is the transcript");
        }
    }

    if (failures) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    std::puts("realtime_chat_check: all checks passed");
    return 0;
}
