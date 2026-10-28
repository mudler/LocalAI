// SPDX-License-Identifier: MIT
#pragma once

// Conversation logic of AudioToAudioStream for speech-to-speech models that run
// on llama.cpp's realtime slot (server_realtime_slot, LFM3-Audio).
//
// Everything here is header-only and free of llama.cpp and gRPC types, so it can
// be tested on its own (see realtime_session_test.cpp). grpc-server.cpp supplies
// the pieces that need the model: a chat-template renderer (RenderFn) and a
// tool-call parser (ResponseTranslator::ToolParser).
//
// The functions mirror the websocket conversation upstream keeps in
// tools/server/server-realtime.cpp (derive_turn_text, ensure_turn, commit,
// on_item_create, on_response_create, on_result). Upstream has no tools, so the
// tool turn, the tool-call text filter and the tool-call events are LocalAI's.

#include <nlohmann/json.hpp>

#include <algorithm>
#include <cctype>
#include <cmath>
#include <cstdint>
#include <cstring>
#include <functional>
#include <map>
#include <optional>
#include <random>
#include <string>
#include <vector>

namespace llama_grpc {
namespace realtime {

using json = nlohmann::json;

// System prompt of the LFM3-Audio model card for an interleaved reply. Used when
// the client sends none.
inline const char * default_instructions() {
    return "Respond with interleaved text and audio.";
}

// Voices of the LFM3 "tts" chat template.
inline std::vector<std::string> default_voices() {
    return {"female", "male"};
}

// UTF-8 safe JSON text. Token pieces can end in the middle of a character, and
// nlohmann::json::dump() throws on invalid UTF-8 unless told to replace it.
inline std::string dump_json(const json & j) {
    return j.dump(-1, ' ', false, json::error_handler_t::replace);
}

//
// chat template: the text around a turn
//

struct ToolCallMsg {
    std::string name;
    std::string arguments; // JSON object as text
    std::string id;
};

struct Msg {
    std::string              role; // system, user, assistant, tool
    std::string              content;
    std::vector<ToolCallMsg> tool_calls;
};

// Renders messages with the chat template of the model. The tools of the session
// are captured by the renderer, not passed here.
using RenderFn = std::function<std::string(const std::vector<Msg> & messages, bool add_generation_prompt)>;

struct TurnText {
    std::string first_header;      // system prompt (with the tools) + the header of the first user turn
    std::string user_header;       // header of a later user turn
    std::string user_footer;       // closes a user turn
    std::string assistant_header;  // opens the response
    std::string assistant_footer;  // closes the response, goes in before the next turn
    // tool turn: the result of a tool call. Absent if the template does not render one.
    bool        has_tool_turn = false;
    std::string tool_header;
    std::string tool_footer;
    std::string assistant_header_after_tool; // opens the response that follows a tool turn
};

// Derives the text around the turns from placeholder messages, the same method
// as upstream derive_turn_text(): render a few conversations and cut the
// placeholders out. Returns false, with err set, if the template does not have
// the shape of a turn-based chat.
inline bool derive_turn_text(const RenderFn & render, const std::string & instructions, TurnText & out, std::string & err) {
    const std::string user  = "@@USER@@";
    const std::string reply = "@@REPLY@@";
    const std::string tool  = "@@TOOL@@";
    auto msg = [](const char * role, const std::string & content) {
        Msg m;
        m.role    = role;
        m.content = content;
        return m;
    };
    std::vector<Msg> sys;
    if (!instructions.empty()) {
        sys.push_back(msg("system", instructions));
    }
    try {
        auto with = [&](const std::vector<Msg> & rest, bool add_generation_prompt = false) {
            std::vector<Msg> all = sys;
            all.insert(all.end(), rest.begin(), rest.end());
            return render(all, add_generation_prompt);
        };
        const std::string a = with({msg("user", user)});
        const std::string g = with({msg("user", user)}, true);
        const std::string c = with({msg("user", user), msg("assistant", reply)});
        const std::string d = with({msg("user", user), msg("assistant", reply), msg("user", user)});

        const size_t pa = a.find(user);
        const size_t pc = c.find(reply);
        const size_t pd = d.find(user, c.size());
        if (pa == std::string::npos || pc == std::string::npos || pd == std::string::npos ||
            c.compare(0, a.size(), a) != 0 || d.compare(0, c.size(), c) != 0 || g.compare(0, a.size(), a) != 0 || pc < a.size()) {
            err = "the chat template does not fit the realtime turn format";
            return false;
        }
        TurnText t;
        // the response opens with the generation prompt, a past turn may render differently (e.g. its think block)
        t.first_header     = a.substr(0, pa);
        t.user_footer      = a.substr(pa + user.size());
        t.assistant_header = g.substr(a.size());
        t.assistant_footer = c.substr(pc + reply.size());
        t.user_header      = d.substr(c.size(), pd - c.size());

        // tool turn: a tool message after a finished response
        const std::string e  = with({msg("user", user), msg("assistant", reply), msg("tool", tool)});
        const std::string eg = with({msg("user", user), msg("assistant", reply), msg("tool", tool)}, true);
        const size_t      pt = e.find(tool, c.size());
        if (pt != std::string::npos && e.compare(0, c.size(), c) == 0 && eg.compare(0, e.size(), e) == 0) {
            t.tool_header                 = e.substr(c.size(), pt - c.size());
            t.tool_footer                 = e.substr(pt + tool.size());
            t.assistant_header_after_tool = eg.substr(e.size());
            t.has_tool_turn               = true;
        }
        out = std::move(t);
        return true;
    } catch (const std::exception & ex) {
        err = std::string("the chat template failed: ") + ex.what();
        return false;
    }
}

//
// session parameters
//

enum class OutMode { Auto, Text, Audio, Mixed };

// output_modalities of a response. null: the model switches between text and
// audio itself.
inline bool parse_output_modalities(const json & j, OutMode & mode, std::string & err) {
    if (j.is_null()) {
        mode = OutMode::Auto;
        return true;
    }
    if (!j.is_array() || j.empty()) {
        err = "output_modalities must be a non-empty array";
        return false;
    }
    bool text  = false;
    bool audio = false;
    for (const auto & m : j) {
        if (m == "text") {
            text = true;
        } else if (m == "audio") {
            audio = true;
        } else {
            err = "output_modalities: unknown modality " + m.dump();
            return false;
        }
    }
    if (text && audio) {
        mode = OutMode::Mixed;
    } else {
        mode = audio ? OutMode::Audio : OutMode::Text;
    }
    return true;
}

inline json out_mode_json(OutMode mode) {
    switch (mode) {
        case OutMode::Text:  return json::array({"text"});
        case OutMode::Audio: return json::array({"audio"});
        case OutMode::Mixed: return json::array({"text", "audio"});
        default:             return nullptr;
    }
}

// -1 is "until the context is full", like upstream "inf"
inline bool parse_max_output_tokens(const json & j, int32_t & n) {
    if (j.is_string() && j.get<std::string>() == "inf") {
        n = -1;
        return true;
    }
    if (!j.is_number_integer() || j.get<int64_t>() < 1) {
        return false;
    }
    n = (int32_t) std::min<int64_t>(j.get<int64_t>(), INT32_MAX);
    return true;
}

// Mixed so that a transcript exists. With tools the model chooses when to speak:
// forcing interleaved audio makes it read out the tool call.
inline OutMode default_out_mode(bool has_tools) {
    return has_tools ? OutMode::Auto : OutMode::Mixed;
}

// AudioToAudioConfig.params: free-form strings.
struct SessionParams {
    std::optional<OutMode>   out_mode;
    int32_t                  max_output_tokens = -1;
    std::optional<float>     temperature;
    std::optional<float>     top_p;
    std::optional<int32_t>   top_k;
    std::optional<uint32_t>  seed;
};

// output_modalities is "text", "audio", "text,audio" or a JSON array.
inline bool parse_modalities_string(const std::string & s, OutMode & mode, std::string & err) {
    json arr = json::array();
    if (!s.empty() && s[0] == '[') {
        arr = json::parse(s, nullptr, false);
        if (arr.is_discarded()) {
            err = "output_modalities is not valid JSON";
            return false;
        }
    } else {
        size_t start = 0;
        while (start <= s.size()) {
            size_t end = s.find(',', start);
            if (end == std::string::npos) {
                end = s.size();
            }
            std::string part = s.substr(start, end - start);
            part.erase(std::remove_if(part.begin(), part.end(), [](unsigned char ch) { return std::isspace(ch); }), part.end());
            if (!part.empty()) {
                arr.push_back(part);
            }
            start = end + 1;
        }
    }
    return parse_output_modalities(arr, mode, err);
}

inline bool parse_session_params(const std::map<std::string, std::string> & params, SessionParams & out, std::string & err) {
    SessionParams p;
    try {
        for (const auto & kv : params) {
            const std::string & k = kv.first;
            const std::string & v = kv.second;
            if (k == "output_modalities") {
                OutMode m;
                if (!parse_modalities_string(v, m, err)) {
                    return false;
                }
                p.out_mode = m;
            } else if (k == "max_output_tokens" || k == "max_new_tokens") {
                json j = v == "inf" ? json(v) : json::parse(v, nullptr, false);
                if (j.is_discarded() || !parse_max_output_tokens(j, p.max_output_tokens)) {
                    err = k + " must be a positive integer or \"inf\"";
                    return false;
                }
            } else if (k == "temperature") {
                p.temperature = std::stof(v);
            } else if (k == "top_p") {
                p.top_p = std::stof(v);
            } else if (k == "top_k") {
                p.top_k = (int32_t) std::stol(v);
            } else if (k == "seed") {
                p.seed = (uint32_t) std::stoul(v);
            }
            // other keys are for other backends
        }
    } catch (const std::exception &) {
        err = "a sampling parameter is not a number";
        return false;
    }
    out = p;
    return true;
}

// Options of one response.create. They may sit at the top of the payload or under
// "response", the way the OpenAI event nests them.
struct ResponseOptions {
    OutMode out_mode;
    int32_t max_output_tokens;
};

inline bool parse_response_options(const json & payload, OutMode default_mode, int32_t default_max, ResponseOptions & out, std::string & err) {
    ResponseOptions o{default_mode, default_max};
    json src = payload.is_object() ? payload : json::object();
    if (src.contains("response") && src["response"].is_object()) {
        src = src["response"];
    }
    if (src.contains("output_modalities") && !parse_output_modalities(src["output_modalities"], o.out_mode, err)) {
        return false;
    }
    if (src.contains("max_output_tokens") && !parse_max_output_tokens(src["max_output_tokens"], o.max_output_tokens)) {
        err = "max_output_tokens must be an integer or \"inf\"";
        return false;
    }
    out = o;
    return true;
}

//
// conversation state
//

// Where the conversation is, and the text that has to go into the slot next.
// The caller applies a step to the slot first and records it here after the slot
// accepted it, so a failed control leaves the state as it was.
class ConversationState {
public:
    enum class Turn { None, User, Tool };

    bool started() const { return started_; }
    Turn turn() const { return turn_; }
    bool has_audio() const { return has_audio_; }
    int  n_turns() const { return n_turns_; }

    // the header a user turn needs before its first audio or text; empty if the turn is open
    std::string user_turn_header(const TurnText & t) const {
        if (turn_ == Turn::User) {
            return {};
        }
        return n_turns_ == 0 ? t.first_header : next_header_ + t.user_header;
    }
    bool user_turn_is_open() const { return turn_ == Turn::User; }
    void user_turn_opened() {
        started_ = true;
        turn_    = Turn::User;
        next_header_.clear();
    }

    void audio_appended() { has_audio_ = true; }
    void audio_committed() { has_audio_ = false; }

    // The result of a tool call: closes the response, then the tool turn.
    // Empty err and the text to append, or the reason it cannot be appended now.
    std::string tool_turn_text(const TurnText & t, const std::string & output, std::string & err) const {
        if (!t.has_tool_turn) {
            err = "the chat template has no tool turn";
            return {};
        }
        if (n_turns_ == 0) {
            err = "function_call_output: there is no function call to answer";
            return {};
        }
        if (turn_ == Turn::User) {
            err = "function_call_output: a user turn is open";
            return {};
        }
        // a second result of the same call round continues the open tool turn
        const std::string lead = turn_ == Turn::Tool ? std::string() : next_header_;
        return lead + t.tool_header + output + t.tool_footer;
    }
    void tool_turn_opened() {
        started_ = true;
        turn_    = Turn::Tool;
        next_header_.clear();
    }

    // "" if a response can be created now, or why not
    std::string response_create_error() const {
        if (turn_ == Turn::None) {
            return "response.create: commit audio or create a message first";
        }
        return {};
    }

    // the text that opens the response: close the turn, then the assistant header
    std::string response_prompt(const TurnText & t) const {
        if (turn_ == Turn::Tool) {
            return t.assistant_header_after_tool;
        }
        return t.user_footer + t.assistant_header;
    }

    // the response ends the turn; its footer goes in before the next one
    void response_created(const TurnText & t) {
        next_header_ = t.assistant_footer;
        turn_        = Turn::None;
        n_turns_++;
    }

    // "" if the session can change now. Before the first audio or message
    // everything can change; after it only the response options.
    std::string session_update_error(bool instructions, bool tools, bool voice) const {
        if (!started_) {
            return {};
        }
        if (instructions) {
            return "instructions cannot change after the first audio or message";
        }
        if (tools) {
            return "tools cannot change after the first audio or message";
        }
        if (voice) {
            return "voice cannot change after the first audio or message";
        }
        return {};
    }

    void reset() { *this = ConversationState(); }

private:
    bool        started_   = false; // something went into the slot, the session config is fixed
    Turn        turn_      = Turn::None;
    bool        has_audio_ = false; // the open turn has audio not committed yet
    int         n_turns_   = 0;     // responses created
    std::string next_header_;       // text that goes in before the next turn
};

//
// audio
//

// s16le mono to f32, an odd trailing byte is ignored
inline std::vector<float> pcm_s16le_to_f32(const std::string & bytes) {
    std::vector<float> out(bytes.size() / 2);
    for (size_t i = 0; i < out.size(); i++) {
        int16_t v;
        std::memcpy(&v, bytes.data() + 2 * i, sizeof(v));
        out[i] = v / 32768.0f;
    }
    return out;
}

inline std::string f32_to_pcm_s16le(const std::vector<float> & samples) {
    std::string bytes(samples.size() * sizeof(int16_t), '\0');
    for (size_t i = 0; i < samples.size(); i++) {
        const int16_t v = (int16_t) std::lround(std::max(-1.0f, std::min(1.0f, samples[i])) * 32767.0f);
        std::memcpy(&bytes[2 * i], &v, sizeof(v));
    }
    return bytes;
}

//
// text: tool-call filter and UTF-8
//

// Number of leading bytes of s that end on a character boundary. A token piece
// can stop inside a multi-byte character; the rest comes with the next piece.
inline size_t utf8_complete_prefix(const std::string & s) {
    size_t i = s.size();
    // look back over at most 3 continuation bytes for the lead byte
    size_t back = 0;
    while (i > 0 && back < 4) {
        const unsigned char ch = (unsigned char) s[i - 1];
        if ((ch & 0xC0) == 0x80) {
            i--;
            back++;
            continue;
        }
        size_t need = 1;
        if ((ch & 0xE0) == 0xC0) {
            need = 2;
        } else if ((ch & 0xF0) == 0xE0) {
            need = 3;
        } else if ((ch & 0xF8) == 0xF0) {
            need = 4;
        }
        // lead byte at i - 1 with `back` continuation bytes after it
        return (back + 1 >= need) ? s.size() : i - 1;
    }
    return s.size();
}

// Streaming filter that keeps the tool-call block out of the transcript. The
// model writes a call as <tool_call>...</tool_call>; that text is for the parser,
// not for the user. A tag can arrive split over several pieces, so a suffix that
// might still become a tag is held back. Whitespace around a block is held and
// dropped, so a reply that is only a call has an empty transcript.
class ToolCallFilter {
public:
    // returns the text that can be shown now
    std::string feed(const std::string & piece) {
        buf_ += piece;
        std::string out;
        const std::string open  = "<tool_call>";
        const std::string close = "</tool_call>";
        for (;;) {
            if (in_call_) {
                const size_t p = buf_.find(close);
                if (p != std::string::npos) {
                    buf_.erase(0, p + close.size());
                    in_call_    = false;
                    after_call_ = true;
                    continue;
                }
                // only a possible start of the closing tag has to stay
                buf_.erase(0, buf_.size() - tag_prefix_len(buf_, close));
                break;
            }
            if (after_call_) {
                size_t ws = 0;
                while (ws < buf_.size() && std::isspace((unsigned char) buf_[ws])) {
                    ws++;
                }
                buf_.erase(0, ws);
                if (buf_.empty()) {
                    break;
                }
                after_call_ = false;
            }
            const size_t p = buf_.find(open);
            if (p != std::string::npos) {
                out += rstrip(buf_.substr(0, p));
                buf_.erase(0, p + open.size());
                in_call_  = true;
                saw_call_ = true;
                n_calls_++;
                continue;
            }
            size_t end = buf_.size() - tag_prefix_len(buf_, open);
            while (end > 0 && std::isspace((unsigned char) buf_[end - 1])) {
                end--;
            }
            out += buf_.substr(0, end);
            buf_.erase(0, end);
            break;
        }
        return out;
    }

    // the response ended: what was held back is text after all, unless it is an unfinished call
    std::string flush() {
        std::string out;
        if (!in_call_) {
            out = saw_call_ ? rstrip(buf_) : buf_;
        }
        buf_.clear();
        in_call_ = false;
        return out;
    }

    // inside a tool-call block; audio produced now is the model reading the call out
    bool in_call() const { return in_call_; }
    bool saw_call() const { return saw_call_; }
    int  n_calls() const { return n_calls_; }

private:
    std::string buf_;
    bool        in_call_    = false;
    bool        after_call_ = false;
    bool        saw_call_   = false;
    int         n_calls_    = 0;

    static std::string rstrip(std::string s) {
        while (!s.empty() && std::isspace((unsigned char) s.back())) {
            s.pop_back();
        }
        return s;
    }

    // longest suffix of s that is a proper prefix of tag
    static size_t tag_prefix_len(const std::string & s, const std::string & tag) {
        size_t n = std::min(s.size(), tag.size() - 1);
        for (; n > 0; n--) {
            if (s.compare(s.size() - n, n, tag, 0, n) == 0) {
                return n;
            }
        }
        return 0;
    }
};

//
// events to the client
//

struct OutEvent {
    std::string event;
    std::string pcm;
    int         sample_rate = 0;
    json        meta        = json::object();
};

inline json error_meta(const std::string & type, const std::string & message) {
    return {{"type", type}, {"message", message}};
}

inline json session_meta(int input_rate, int output_rate, const std::vector<std::string> & voices, const std::string & requested_voice) {
    json meta = {{"input_sample_rate", input_rate}, {"output_sample_rate", output_rate}, {"voices", voices}};
    if (!requested_voice.empty()) {
        // the realtime slot of llama.cpp has no voice selection
        meta["voice"]         = requested_voice;
        meta["voice_applied"] = false;
    }
    return meta;
}

inline std::string make_call_id() {
    static thread_local std::mt19937_64 rng{std::random_device{}()};
    static const char hex[] = "0123456789abcdef";
    std::string id = "call_";
    for (int i = 0; i < 24; i++) {
        id += hex[rng() & 15];
    }
    return id;
}

// Turns the results of the slot into the events of the stream, in order:
// text deltas and audio frames as they come, then one function_call_arguments.done
// per call, then response.done.
class ResponseTranslator {
public:
    // the tool calls in the full text of a response
    using ToolParser = std::function<std::vector<ToolCallMsg>(const std::string & text)>;

    ResponseTranslator(OutMode mode, int sample_rate, ToolParser parser = nullptr, std::function<std::string()> id_gen = make_call_id)
        : mode_(mode), sample_rate_(sample_rate), parser_(std::move(parser)), id_gen_(std::move(id_gen)) {}

    std::vector<OutEvent> on_result(const std::string & text, const std::vector<float> & audio, bool response_done, bool cancelled, int n_decoded) {
        std::vector<OutEvent> events;
        if (done_) {
            return events;
        }
        if (!text.empty()) {
            raw_ += text;
            // without tools nothing is a call
            emit_text(parser_ ? filter_.feed(text) : text, events);
        }
        if (!audio.empty()) {
            if (filter_.in_call()) {
                dropped_audio_++;
            } else {
                OutEvent ev;
                ev.event       = "response.audio.delta";
                ev.pcm         = f32_to_pcm_s16le(audio);
                ev.sample_rate = sample_rate_;
                events.push_back(std::move(ev));
            }
        }
        if (!response_done) {
            return events;
        }
        done_ = true;
        if (parser_) {
            emit_text(filter_.flush(), events);
        }
        // a lone lead byte left over cannot become a character any more
        if (!utf8_tail_.empty()) {
            shown_ += utf8_tail_;
            events.push_back(delta_event(utf8_tail_));
            utf8_tail_.clear();
        }
        if (!cancelled && parser_) {
            for (const auto & call : parser_(raw_)) {
                OutEvent ev;
                ev.event = "response.function_call_arguments.done";
                ev.meta  = {{"call_id", call.id.empty() ? id_gen_() : call.id},
                            {"name", call.name},
                            {"arguments", call.arguments.empty() ? std::string("{}") : call.arguments}};
                events.push_back(std::move(ev));
                n_calls_++;
            }
        }
        OutEvent ev;
        ev.event = "response.done";
        ev.meta  = {{"status", cancelled ? "cancelled" : "completed"}, {"usage", {{"output_tokens", n_decoded}}}, {"text", shown_}};
        events.push_back(std::move(ev));
        return events;
    }

    bool done() const { return done_; }
    int  n_calls() const { return n_calls_; }
    size_t dropped_audio_frames() const { return dropped_audio_; }
    const std::string & shown_text() const { return shown_; }

private:
    OutMode                       mode_;
    int                           sample_rate_;
    ToolParser                    parser_;
    std::function<std::string()>  id_gen_;
    ToolCallFilter                filter_;
    std::string                   raw_;       // everything the model wrote, tags included
    std::string                   shown_;     // what went to the transcript
    std::string                   utf8_tail_; // bytes of a character that is not complete yet
    bool                          done_          = false;
    int                           n_calls_       = 0;
    size_t                        dropped_audio_ = 0;

    OutEvent delta_event(const std::string & delta) const {
        OutEvent ev;
        // a text-only response is plain text; otherwise the text is the transcript of the audio
        ev.event = mode_ == OutMode::Text ? "response.text.delta" : "response.audio_transcript.delta";
        ev.meta  = {{"delta", delta}};
        return ev;
    }

    void emit_text(const std::string & visible, std::vector<OutEvent> & events) {
        if (visible.empty()) {
            return;
        }
        utf8_tail_ += visible;
        const size_t n = utf8_complete_prefix(utf8_tail_);
        if (n == 0) {
            return;
        }
        const std::string piece = utf8_tail_.substr(0, n);
        utf8_tail_.erase(0, n);
        shown_ += piece;
        events.push_back(delta_event(piece));
    }
};

} // namespace realtime
} // namespace llama_grpc
