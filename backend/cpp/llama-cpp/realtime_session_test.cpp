// SPDX-License-Identifier: MIT
#include "realtime_session.h"

#include <algorithm>
#include <cstdio>
#include <stdexcept>

namespace rt = llama_grpc::realtime;

namespace {

int failures = 0;

void check(bool condition, const char * what) {
    if (!condition) {
        std::fprintf(stderr, "FAIL: %s\n", what);
        ++failures;
    }
}

template <typename A, typename B>
void check_eq(const A & got, const B & want, const char * what) {
    if (!(got == want)) {
        std::fprintf(stderr, "FAIL: %s\n  got:  %s\n  want: %s\n", what, nlohmann::json(got).dump().c_str(), nlohmann::json(want).dump().c_str());
        ++failures;
    }
}

// A ChatML-like template. With tools, the system turn lists them in <tools>.
rt::RenderFn fake_renderer(const std::string & tools) {
    return [tools](const std::vector<rt::Msg> & msgs, bool gen) {
        std::string out;
        bool has_sys = !msgs.empty() && msgs[0].role == "system";
        if (!has_sys && !tools.empty()) {
            out += "<|im_start|>system\n<tools>" + tools + "</tools><|im_end|>\n";
        }
        for (const auto & m : msgs) {
            if (m.role == "system") {
                out += "<|im_start|>system\n" + m.content;
                if (!tools.empty()) {
                    out += "\n<tools>" + tools + "</tools>";
                }
                out += "<|im_end|>\n";
            } else if (m.role == "tool") {
                out += "<|im_start|>tool\n<tool_response>\n" + m.content + "\n</tool_response><|im_end|>\n";
            } else {
                out += "<|im_start|>" + m.role + "\n" + m.content + "<|im_end|>\n";
            }
        }
        if (gen) {
            out += "<|im_start|>assistant\n";
        }
        return out;
    };
}

rt::TurnText derive(const std::string & instructions, const std::string & tools) {
    rt::TurnText t;
    std::string  err;
    check(rt::derive_turn_text(fake_renderer(tools), instructions, t, err), "derive_turn_text succeeds");
    return t;
}

void test_turn_text() {
    const auto t = derive("Be brief.", "");
    check_eq(t.first_header, std::string("<|im_start|>system\nBe brief.<|im_end|>\n<|im_start|>user\n"), "first header");
    check_eq(t.user_header, std::string("<|im_start|>user\n"), "user header");
    check_eq(t.user_footer, std::string("<|im_end|>\n"), "user footer");
    check_eq(t.assistant_header, std::string("<|im_start|>assistant\n"), "assistant header");
    check_eq(t.assistant_footer, std::string("<|im_end|>\n"), "assistant footer");
    check(t.has_tool_turn, "a tool turn is derived");
    check_eq(t.tool_header, std::string("<|im_start|>tool\n<tool_response>\n"), "tool header");
    check_eq(t.tool_footer, std::string("\n</tool_response><|im_end|>\n"), "tool footer");
    check_eq(t.assistant_header_after_tool, std::string("<|im_start|>assistant\n"), "assistant header after a tool turn");
}

void test_turn_text_with_tools() {
    const std::string tools = R"([{"name":"get_time"}])";
    const auto        t     = derive("Be brief.", tools);
    check(t.first_header.find("<tools>" + tools + "</tools>") != std::string::npos, "the system turn lists the tools");
    check_eq(t.first_header.compare(t.first_header.size() - 17, 17, "<|im_start|>user\n"), 0, "the first header ends with the user header");
    // tools only change the system turn
    check_eq(t.user_header, derive("Be brief.", "").user_header, "tools do not change the user header");
}

void test_turn_text_without_instructions() {
    const auto t = derive("", R"([{"name":"f"}])");
    check(t.first_header.find("<tools>") != std::string::npos, "the template adds the tools turn itself");
}

void test_turn_text_rejects_bad_template() {
    rt::RenderFn broken = [](const std::vector<rt::Msg> &, bool) { return std::string("no placeholders"); };
    rt::TurnText t;
    std::string  err;
    check(!rt::derive_turn_text(broken, "x", t, err), "a template that drops the content is rejected");
    check(!err.empty(), "the reason is reported");

    rt::RenderFn throwing = [](const std::vector<rt::Msg> &, bool) -> std::string { throw std::runtime_error("boom"); };
    err.clear();
    check(!rt::derive_turn_text(throwing, "x", t, err), "a throwing template is rejected");
    check(err.find("boom") != std::string::npos, "the exception text is reported");
}

void test_no_tool_turn() {
    // a template that cannot render a tool message after a plain reply
    rt::RenderFn base = fake_renderer("");
    rt::RenderFn no_tool = [base](const std::vector<rt::Msg> & msgs, bool gen) {
        std::vector<rt::Msg> kept;
        for (const auto & m : msgs) {
            if (m.role != "tool") {
                kept.push_back(m);
            }
        }
        return base(kept, gen);
    };
    rt::TurnText t;
    std::string  err;
    check(rt::derive_turn_text(no_tool, "x", t, err), "derivation still works");
    check(!t.has_tool_turn, "no tool turn is reported");
}

void test_modalities() {
    rt::OutMode m = rt::OutMode::Text;
    std::string err;
    check(rt::parse_output_modalities(nullptr, m, err) && m == rt::OutMode::Auto, "null is auto");
    check(rt::parse_output_modalities(nlohmann::json::array({"text", "audio"}), m, err) && m == rt::OutMode::Mixed, "text and audio is mixed");
    check(rt::parse_output_modalities(nlohmann::json::array({"audio"}), m, err) && m == rt::OutMode::Audio, "audio");
    check(rt::parse_output_modalities(nlohmann::json::array({"text"}), m, err) && m == rt::OutMode::Text, "text");
    check(!rt::parse_output_modalities(nlohmann::json::array(), m, err), "an empty array is rejected");
    check(!rt::parse_output_modalities(nlohmann::json::array({"video"}), m, err), "an unknown modality is rejected");
    check(!rt::parse_output_modalities("text", m, err), "a bare string is rejected");

    check_eq(rt::default_out_mode(false) == rt::OutMode::Mixed, true, "no tools: interleaved");
    check_eq(rt::default_out_mode(true) == rt::OutMode::Auto, true, "tools: the model chooses");

    int32_t n = 0;
    check(rt::parse_max_output_tokens(200, n) && n == 200, "max_output_tokens integer");
    check(rt::parse_max_output_tokens("inf", n) && n == -1, "max_output_tokens inf");
    check(!rt::parse_max_output_tokens(0, n), "max_output_tokens 0 is rejected");
    check(!rt::parse_max_output_tokens("many", n), "max_output_tokens string is rejected");
}

void test_session_params() {
    rt::SessionParams p;
    std::string       err;
    check(rt::parse_session_params({{"output_modalities", "text, audio"}, {"max_new_tokens", "64"}, {"temperature", "0.5"},
                                    {"top_k", "40"}, {"seed", "7"}, {"unrelated", "x"}}, p, err), "params parse");
    check(p.out_mode && *p.out_mode == rt::OutMode::Mixed, "comma list of modalities");
    check_eq(p.max_output_tokens, 64, "max_new_tokens alias");
    check(p.temperature && *p.temperature == 0.5f, "temperature");
    check(p.top_k && *p.top_k == 40, "top_k");
    check(p.seed && *p.seed == 7u, "seed");
    check(!p.top_p, "top_p stays unset");

    check(rt::parse_session_params({{"output_modalities", "[\"audio\"]"}}, p, err) && *p.out_mode == rt::OutMode::Audio, "JSON array of modalities");
    check(!rt::parse_session_params({{"output_modalities", "video"}}, p, err), "bad modality");
    check(!rt::parse_session_params({{"temperature", "hot"}}, p, err), "bad number");
    check(!rt::parse_session_params({{"max_output_tokens", "-3"}}, p, err), "bad token cap");
}

void test_response_options() {
    rt::ResponseOptions o{};
    std::string         err;
    check(rt::parse_response_options(nlohmann::json::parse(R"({"output_modalities":["text"],"max_output_tokens":200})"), rt::OutMode::Mixed, -1, o, err), "flat payload");
    check(o.out_mode == rt::OutMode::Text && o.max_output_tokens == 200, "flat payload values");
    check(rt::parse_response_options(nlohmann::json::parse(R"({"response":{"max_output_tokens":9}})"), rt::OutMode::Mixed, -1, o, err), "nested payload");
    check(o.out_mode == rt::OutMode::Mixed && o.max_output_tokens == 9, "nested payload keeps the session mode");
    check(rt::parse_response_options(nlohmann::json(), rt::OutMode::Auto, 5, o, err) && o.max_output_tokens == 5, "empty payload keeps the defaults");
    check(!rt::parse_response_options(nlohmann::json::parse(R"({"output_modalities":["x"]})"), rt::OutMode::Mixed, -1, o, err), "bad modality");
    check(!rt::parse_response_options(nlohmann::json::parse(R"({"max_output_tokens":0})"), rt::OutMode::Mixed, -1, o, err), "bad cap");
}

void test_state_machine() {
    const auto        t = derive("sys", "");
    rt::ConversationState s;
    std::string       err;

    check(!s.response_create_error().empty(), "response.create without an open turn is rejected");
    check_eq(s.user_turn_header(t), t.first_header, "the first turn opens with the first header");
    s.user_turn_opened();
    check(s.started(), "a turn fixes the session");
    check_eq(s.user_turn_header(t), std::string(), "an open turn needs no header");
    check(s.response_create_error().empty(), "response.create is allowed with an open turn");
    check_eq(s.response_prompt(t), t.user_footer + t.assistant_header, "prompt after a user turn closes it");

    s.response_created(t);
    check_eq(s.n_turns(), 1, "one response");
    check(!s.response_create_error().empty(), "a second response.create without new input is rejected");
    check_eq(s.user_turn_header(t), t.assistant_footer + t.user_header, "the next turn closes the response first");

    // tool round
    const std::string text = s.tool_turn_text(t, "12:00", err);
    check_eq(text, t.assistant_footer + t.tool_header + "12:00" + t.tool_footer, "tool turn text");
    s.tool_turn_opened();
    check(s.turn() == rt::ConversationState::Turn::Tool, "tool turn is open");
    check_eq(s.tool_turn_text(t, "second", err), t.tool_header + "second" + t.tool_footer, "a second result continues the tool turn");
    check_eq(s.response_prompt(t), t.assistant_header_after_tool, "prompt after a tool turn is the assistant header only");
    check(s.response_create_error().empty(), "response.create is allowed after a tool result");
    s.response_created(t);
    check_eq(s.n_turns(), 2, "two responses");

    // audio after a tool turn re-opens a user turn with the user header only
    rt::ConversationState s2;
    s2.user_turn_opened();
    s2.response_created(t);
    (void) s2.tool_turn_text(t, "x", err);
    s2.tool_turn_opened();
    check_eq(s2.user_turn_header(t), t.user_header, "a user turn after a tool turn needs only the user header");
}

void test_state_tool_errors() {
    const auto        t = derive("sys", "");
    rt::ConversationState s;
    std::string       err;
    check(s.tool_turn_text(t, "x", err).empty() && !err.empty(), "no function call to answer before a response");
    s.user_turn_opened();
    err.clear();
    check(s.tool_turn_text(t, "x", err).empty() && !err.empty(), "no tool result inside a user turn");
    rt::TurnText none = t;
    none.has_tool_turn = false;
    rt::ConversationState s2;
    s2.user_turn_opened();
    s2.response_created(none);
    err.clear();
    check(s2.tool_turn_text(none, "x", err).empty() && !err.empty(), "no tool result without a tool turn in the template");
}

void test_session_update_rules() {
    rt::ConversationState s;
    check(s.session_update_error(true, true, true).empty(), "everything can change before the first turn");
    s.user_turn_opened();
    check(!s.session_update_error(true, false, false).empty(), "instructions are fixed after the first turn");
    check(!s.session_update_error(false, true, false).empty(), "tools are fixed after the first turn");
    check(!s.session_update_error(false, false, true).empty(), "voice is fixed after the first turn");
    check(s.session_update_error(false, false, false).empty(), "response options can still change");
    s.reset();
    check(!s.started() && s.n_turns() == 0, "reset forgets the conversation");
    check(s.session_update_error(true, true, true).empty(), "reset unlocks the session");
}

void test_pcm() {
    std::string bytes(6, '\0');
    int16_t     vals[3] = {0, 16384, -32768};
    std::memcpy(&bytes[0], vals, 6);
    auto f = rt::pcm_s16le_to_f32(bytes);
    check_eq(f.size(), (size_t) 3, "three samples");
    check(f[1] == 0.5f && f[2] == -1.0f, "scaled to [-1, 1)");
    check_eq(rt::pcm_s16le_to_f32(bytes + "x").size(), (size_t) 3, "an odd byte is ignored");

    const std::string back = rt::f32_to_pcm_s16le({0.0f, 2.0f, -2.0f});
    int16_t           out[3];
    std::memcpy(out, back.data(), 6);
    check(out[0] == 0 && out[1] == 32767 && out[2] == -32767, "clipping");
}

void test_utf8() {
    check_eq(rt::utf8_complete_prefix("abc"), (size_t) 3, "ascii");
    check_eq(rt::utf8_complete_prefix("a\xE2\x82"), (size_t) 1, "euro sign cut after 2 bytes");
    check_eq(rt::utf8_complete_prefix("a\xE2\x82\xAC"), (size_t) 4, "euro sign whole");
    check_eq(rt::utf8_complete_prefix("\xF0\x9F\x98"), (size_t) 0, "emoji cut after 3 bytes");
    check_eq(rt::utf8_complete_prefix("\xC3"), (size_t) 0, "lone lead byte");
    check_eq(rt::utf8_complete_prefix(""), (size_t) 0, "empty");
}

std::string feed_all(rt::ToolCallFilter & f, const std::vector<std::string> & pieces) {
    std::string out;
    for (const auto & p : pieces) {
        out += f.feed(p);
    }
    return out;
}

std::string run_filter(rt::ToolCallFilter & f, const std::vector<std::string> & pieces) {
    std::string out = feed_all(f, pieces);
    out += f.flush();
    return out;
}

void test_filter_plain() {
    rt::ToolCallFilter f;
    check_eq(run_filter(f, {"Hello", " world", "."}), std::string("Hello world."), "text without a tag passes through");
    check(!f.saw_call() && !f.in_call(), "no call seen");
}

void test_filter_whole_call() {
    rt::ToolCallFilter f;
    const std::string out = run_filter(f, {"Let me check.\n<tool_call>\n<function=get_time>\n</function>\n</tool_call>"});
    check_eq(out, std::string("Let me check."), "the block and the whitespace around it are dropped");
    check(f.saw_call() && !f.in_call(), "call seen and closed");
    check_eq(f.n_calls(), 1, "one call");
}

void test_filter_split_tags() {
    rt::ToolCallFilter f;
    std::string        out;
    for (const char * p : {"Sure", ". <tool", "_call>", "\n<function=f>", "</func", "tion>\n</tool", "_call>", " done"}) {
        out += f.feed(p);
        if (std::string(p) == "\n<function=f>") {
            check(f.in_call(), "inside the block");
        }
    }
    out += f.flush();
    check_eq(out, std::string("Sure.done"), "tags split over pieces");
    check(!f.in_call(), "closed after the end tag");
}

void test_filter_tag_one_char_at_a_time() {
    rt::ToolCallFilter f;
    const std::string  text = "a<tool_call>x</tool_call>b";
    std::string        out;
    for (char ch : text) {
        out += f.feed(std::string(1, ch));
    }
    out += f.flush();
    check_eq(out, std::string("ab"), "one byte at a time");
}

void test_filter_two_calls() {
    rt::ToolCallFilter f;
    const std::string  out = run_filter(f, {"<tool_call>a</tool_call>\n\n<tool_call>b</tool_call>\n"});
    check_eq(out, std::string(), "a reply of calls only has an empty transcript");
    check_eq(f.n_calls(), 2, "two calls");
}

void test_filter_text_after_call() {
    rt::ToolCallFilter f;
    check_eq(run_filter(f, {"<tool_call>a</tool_call>\n\nBye"}), std::string("Bye"), "text after a call loses the blank lines");
}

void test_filter_lookalikes() {
    rt::ToolCallFilter f;
    check_eq(run_filter(f, {"1 < 2 and <tool is not a tag"}), std::string("1 < 2 and <tool is not a tag"), "lookalikes stay text");
    rt::ToolCallFilter g;
    check_eq(run_filter(g, {"end <tool"}), std::string("end <tool"), "a partial tag at the end is text after all");
}

void test_filter_unfinished_call() {
    rt::ToolCallFilter f;
    check_eq(run_filter(f, {"ok <tool_call>\n<function=f>\n<param"}), std::string("ok"), "an unfinished call is not shown");
    check(!f.in_call(), "flush leaves the block");
}

void test_filter_whitespace_kept_in_text() {
    rt::ToolCallFilter f;
    check_eq(run_filter(f, {"a", " ", "b", "\n"}), std::string("a b\n"), "inner and trailing whitespace of plain text is kept");
}

// the full text a fake slot produces, as the results of the slot
struct Step {
    std::string        text;
    std::vector<float> audio;
};

std::vector<rt::OutEvent> run(rt::ResponseTranslator & tr, const std::vector<Step> & steps, bool cancelled = false, int n_decoded = 7) {
    std::vector<rt::OutEvent> all;
    for (const auto & s : steps) {
        for (auto & e : tr.on_result(s.text, s.audio, false, false, n_decoded)) {
            all.push_back(std::move(e));
        }
    }
    for (auto & e : tr.on_result("", {}, true, cancelled, n_decoded)) {
        all.push_back(std::move(e));
    }
    return all;
}

std::vector<std::string> names(const std::vector<rt::OutEvent> & ev) {
    std::vector<std::string> n;
    for (const auto & e : ev) {
        n.push_back(e.event);
    }
    return n;
}

void test_translator_audio_and_text() {
    rt::ResponseTranslator tr(rt::OutMode::Mixed, 24000);
    const auto ev = run(tr, {{"Hi", {}}, {"", {0.5f, -0.5f}}, {" there", {0.25f}}});
    const std::vector<std::string> want = {"response.audio_transcript.delta", "response.audio.delta", "response.audio_transcript.delta",
                                           "response.audio.delta", "response.done"};
    check_eq(names(ev), want, "events follow the order of the results, response.done last");
    check_eq(ev[0].meta["delta"].get<std::string>(), std::string("Hi"), "first transcript delta");
    check_eq(ev[1].sample_rate, 24000, "audio carries its rate");
    check_eq(ev[1].pcm.size(), (size_t) 4, "two s16 samples");
    const auto & done = ev.back().meta;
    check_eq(done["status"].get<std::string>(), std::string("completed"), "completed");
    check_eq(done["text"].get<std::string>(), std::string("Hi there"), "full text");
    check_eq(done["usage"]["output_tokens"].get<int>(), 7, "usage");
}

void test_translator_text_only() {
    rt::ResponseTranslator tr(rt::OutMode::Text, 24000);
    const auto ev = run(tr, {{"Yes", {}}});
    check_eq(ev[0].event, std::string("response.text.delta"), "text-only responses use text deltas");
}

void test_translator_cancel() {
    rt::ResponseTranslator tr(rt::OutMode::Mixed, 24000, [](const std::string &) {
        return std::vector<rt::ToolCallMsg>{{"f", "{}", ""}};
    });
    const auto ev = run(tr, {{"Hel", {0.1f}}}, /*cancelled=*/true, 3);
    check_eq(ev.back().event, std::string("response.done"), "a cancelled response ends with response.done");
    check_eq(ev.back().meta["status"].get<std::string>(), std::string("cancelled"), "status cancelled");
    check_eq(ev.back().meta["text"].get<std::string>(), std::string("Hel"), "the decoded part is reported");
    for (const auto & e : ev) {
        check(e.event != "response.function_call_arguments.done", "a cancelled response makes no tool call");
    }
    check(tr.on_result("late", {0.1f}, false, false, 9).empty(), "nothing after response.done");
}

void test_translator_tool_call() {
    std::string seen;
    rt::ResponseTranslator tr(rt::OutMode::Auto, 24000, [&seen](const std::string & text) {
        seen = text;
        return std::vector<rt::ToolCallMsg>{{"get_time", R"({"tz":"UTC"})", ""}, {"get_date", "", "call_fixed"}};
    }, [] { return std::string("call_gen"); });
    const std::string call = "<tool_call>\n<function=get_time>\n<parameter=tz>\nUTC\n</parameter>\n</function>\n</tool_call>";
    const auto        ev   = run(tr, {{"One moment.", {0.1f}}, {"<tool", {}}, {"_call>\n<function=get_time>", {0.2f}}, {"\n</function>\n</tool_call>", {0.3f}}, {"", {0.4f}}});
    // the transcript never shows the tag, the audio inside the block is dropped
    for (const auto & e : ev) {
        if (e.event == "response.audio_transcript.delta") {
            check(e.meta["delta"].get<std::string>().find('<') == std::string::npos, "no tag in the transcript");
        }
    }
    check(tr.dropped_audio_frames() >= 1, "audio inside the block was dropped");
    check(seen.find("<tool_call>") != std::string::npos, "the parser sees the full text, tags included");

    const auto n = names(ev);
    const auto done_pos = std::find(n.begin(), n.end(), "response.done") - n.begin();
    const auto call_pos = std::find(n.begin(), n.end(), "response.function_call_arguments.done") - n.begin();
    check(call_pos < done_pos, "tool calls come before response.done");
    check_eq((size_t) std::count(n.begin(), n.end(), "response.function_call_arguments.done"), (size_t) 2, "one event per call");
    {
        const auto & a = ev[call_pos].meta;
        check_eq(a["name"].get<std::string>(), std::string("get_time"), "call name");
        check_eq(a["arguments"].get<std::string>(), std::string(R"({"tz":"UTC"})"), "arguments are a JSON string");
        check_eq(a["call_id"].get<std::string>(), std::string("call_gen"), "generated call id");
        const auto & b = ev[call_pos + 1].meta;
        check_eq(b["call_id"].get<std::string>(), std::string("call_fixed"), "a call id from the parser is kept");
        check_eq(b["arguments"].get<std::string>(), std::string("{}"), "empty arguments become an empty object");
    }
    check_eq(ev.back().meta["text"].get<std::string>(), std::string("One moment."), "response.done text has no tool call");
    (void) call;
}

void test_translator_utf8_split() {
    rt::ResponseTranslator tr(rt::OutMode::Mixed, 24000);
    const auto ev = run(tr, {{"5 \xE2\x82", {}}, {"\xAC", {}}});
    std::string joined;
    for (const auto & e : ev) {
        if (e.event == "response.audio_transcript.delta") {
            joined += e.meta["delta"].get<std::string>();
            // every delta must be valid UTF-8 on its own
            check_eq(rt::utf8_complete_prefix(e.meta["delta"].get<std::string>()), e.meta["delta"].get<std::string>().size(), "delta ends on a boundary");
        }
    }
    check_eq(joined, std::string("5 \xE2\x82\xAC"), "the split character is rejoined");
    // the JSON of every event can be dumped
    for (const auto & e : ev) {
        (void) rt::dump_json(e.meta);
    }
}

void test_translator_utf8_dangling() {
    rt::ResponseTranslator tr(rt::OutMode::Mixed, 24000);
    const auto ev = run(tr, {{"x\xE2", {}}});
    check_eq(ev.back().event, std::string("response.done"), "ends");
    // must not throw
    (void) rt::dump_json(ev.back().meta);
    (void) rt::dump_json(ev[ev.size() - 2].meta);
}

void test_meta() {
    const auto m = rt::session_meta(16000, 24000, rt::default_voices(), "");
    check_eq(m["input_sample_rate"].get<int>(), 16000, "input rate");
    check_eq(m["output_sample_rate"].get<int>(), 24000, "output rate");
    check_eq(m["voices"], nlohmann::json::array({"female", "male"}), "voices");
    check(!m.contains("voice"), "no voice requested");
    const auto v = rt::session_meta(16000, 24000, rt::default_voices(), "male");
    check(v["voice"] == "male" && v["voice_applied"] == false, "a requested voice is reported as not applied");
    const auto e = rt::error_meta("conversation_empty", "msg");
    check(e["type"] == "conversation_empty" && e["message"] == "msg", "error meta");
    check(!e.contains("response_scoped"), "an error is not response scoped by default");
    check(rt::error_meta("conversation_empty", "msg", true)["response_scoped"] == true, "a response scoped error says so");
    check(rt::make_call_id().rfind("call_", 0) == 0, "call id prefix");
    check(rt::make_call_id() != rt::make_call_id(), "call ids differ");
}

} // namespace

int main() {
    test_turn_text();
    test_turn_text_with_tools();
    test_turn_text_without_instructions();
    test_turn_text_rejects_bad_template();
    test_no_tool_turn();
    test_modalities();
    test_session_params();
    test_response_options();
    test_state_machine();
    test_state_tool_errors();
    test_session_update_rules();
    test_pcm();
    test_utf8();
    test_filter_plain();
    test_filter_whole_call();
    test_filter_split_tags();
    test_filter_tag_one_char_at_a_time();
    test_filter_two_calls();
    test_filter_text_after_call();
    test_filter_lookalikes();
    test_filter_unfinished_call();
    test_filter_whitespace_kept_in_text();
    test_translator_audio_and_text();
    test_translator_text_only();
    test_translator_cancel();
    test_translator_tool_call();
    test_translator_utf8_split();
    test_translator_utf8_dangling();
    test_meta();
    if (failures != 0) {
        std::fprintf(stderr, "%d check(s) failed\n", failures);
        return 1;
    }
    std::puts("realtime_session_test: all checks passed");
    return 0;
}
