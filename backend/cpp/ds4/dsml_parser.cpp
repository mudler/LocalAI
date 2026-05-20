#include "dsml_parser.h"

#include <algorithm>
#include <cstdio>
#include <cstring>
#include <chrono>
#include <random>
#include <string>
#include <vector>

namespace ds4cpp {

namespace {

constexpr const char *kThinkOpen      = "<think>";
constexpr const char *kThinkClose     = "</think>";
constexpr const char *kToolsOpen      = "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>";   // <｜DSML｜tool_calls>
constexpr const char *kToolsClose     = "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "tool_calls>"; // </｜DSML｜tool_calls>
constexpr const char *kInvokeOpenPfx  = "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke name=\""; // <｜DSML｜invoke name="
constexpr const char *kInvokeClose    = "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "invoke>";       // </｜DSML｜invoke>
constexpr const char *kParamOpenPfx   = "<\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter name=\""; // <｜DSML｜parameter name="
constexpr const char *kParamClose     = "</\xef\xbd\x9c" "DSML\xef\xbd\x9c" "parameter>";       // </｜DSML｜parameter>

// All structural markers the parser might encounter - used to detect "buf
// might be a partial marker, don't drain yet" conditions.
const std::vector<std::string> &all_markers() {
    static const std::vector<std::string> v = {
        kThinkOpen, kThinkClose,
        kToolsOpen, kToolsClose,
        kInvokeOpenPfx, kInvokeClose,
        kParamOpenPfx, kParamClose,
    };
    return v;
}

// Returns true if `buf` could be a *prefix* of any marker (i.e., we should
// wait for more text before draining as plain content). The marker-prefix
// loop handles fixed markers exactly. For markers with variable-length
// internal data (kInvokeOpenPfx, kParamOpenPfx have an open quote, then the
// tool/param name, then a closing quote and `>`), we also wait while buf
// starts with `<` and has not yet seen a `>`: the leading `<` could be the
// start of one of those open markers, or a literal that we can confirm only
// once we know what follows. Anything after the first `>` arrives is either
// consumed by TryConsumeMarker or emitted as a literal `<` by the caller.
bool looks_like_prefix(const std::string &buf) {
    for (const auto &m : all_markers()) {
        if (m.size() > buf.size() && m.compare(0, buf.size(), buf) == 0) return true;
    }
    if (!buf.empty() && buf[0] == '<' && buf.find('>') == std::string::npos) {
        return true;
    }
    return false;
}

bool consume_literal(std::string &buf, const std::string &lit) {
    if (buf.compare(0, lit.size(), lit) == 0) {
        buf.erase(0, lit.size());
        return true;
    }
    return false;
}

// Find the next '<' in buf starting at offset; returns std::string::npos if none.
size_t next_tag(const std::string &buf, size_t off = 0) {
    return buf.find('<', off);
}

std::string json_escape(const std::string &in) {
    std::string out;
    out.reserve(in.size() + 2);
    for (char c : in) {
        switch (c) {
            case '"':  out += "\\\""; break;
            case '\\': out += "\\\\"; break;
            case '\b': out += "\\b"; break;
            case '\f': out += "\\f"; break;
            case '\n': out += "\\n"; break;
            case '\r': out += "\\r"; break;
            case '\t': out += "\\t"; break;
            default:
                if (static_cast<unsigned char>(c) < 0x20) {
                    char tmp[8];
                    std::snprintf(tmp, sizeof(tmp), "\\u%04x", c);
                    out += tmp;
                } else {
                    out += c;
                }
        }
    }
    return out;
}

} // namespace

DsmlParser::DsmlParser() = default;

bool DsmlParser::IsInDsmlStructural() const {
    switch (state_) {
        case State::TOOL_CALLS:
        case State::INVOKE:
            return true;
        case State::PARAM_VALUE:  // payload bytes; user sampling applies
        case State::TEXT:
        case State::THINK:
            return false;
    }
    return false;
}

void DsmlParser::EmitArgsChunk(const std::string &chunk, std::vector<ParserEvent> &out) {
    if (chunk.empty()) return;
    ParserEvent e;
    e.type = ParserEvent::TOOL_ARGS;
    e.text = chunk;
    e.index = tool_index_;
    out.push_back(std::move(e));
}

void DsmlParser::FinishCurrentToolCall(std::vector<ParserEvent> &out) {
    if (tool_index_ < 0) return;
    // Close the JSON object that was opened on the first parameter.
    if (args_emitted_open_brace_) {
        EmitArgsChunk("}", out);
    } else {
        EmitArgsChunk("{}", out);
    }
    ParserEvent e;
    e.type = ParserEvent::TOOL_END;
    e.index = tool_index_;
    out.push_back(std::move(e));
    current_tool_name_.clear();
    args_emitted_open_brace_ = false;
    args_param_count_ = 0;
}

bool DsmlParser::TryConsumeMarker(std::vector<ParserEvent> &out) {
    switch (state_) {
    case State::TEXT: {
        if (consume_literal(buf_, kThinkOpen))   { state_ = State::THINK;       return true; }
        if (consume_literal(buf_, kToolsOpen))   { state_ = State::TOOL_CALLS;  return true; }
        return false;
    }
    case State::THINK: {
        if (consume_literal(buf_, kThinkClose))  { state_ = State::TEXT;        return true; }
        return false;
    }
    case State::TOOL_CALLS: {
        if (consume_literal(buf_, kToolsClose))  { state_ = State::TEXT;        return true; }
        // <｜DSML｜invoke name="X">
        if (buf_.compare(0, std::strlen(kInvokeOpenPfx), kInvokeOpenPfx) == 0) {
            size_t close_q = buf_.find('"', std::strlen(kInvokeOpenPfx));
            if (close_q == std::string::npos) return false; // need more bytes
            size_t close_gt = buf_.find('>', close_q);
            if (close_gt == std::string::npos) return false;
            current_tool_name_ = buf_.substr(std::strlen(kInvokeOpenPfx),
                                             close_q - std::strlen(kInvokeOpenPfx));
            tool_index_++;
            buf_.erase(0, close_gt + 1);
            ParserEvent e;
            e.type = ParserEvent::TOOL_START;
            e.tool_name = current_tool_name_;
            e.tool_id   = RandomToolId();
            e.index     = tool_index_;
            out.push_back(std::move(e));
            args_emitted_open_brace_ = false;
            args_param_count_ = 0;
            state_ = State::INVOKE;
            return true;
        }
        return false;
    }
    case State::INVOKE: {
        if (consume_literal(buf_, kInvokeClose)) {
            FinishCurrentToolCall(out);
            state_ = State::TOOL_CALLS;
            return true;
        }
        // <｜DSML｜parameter name="K" string="true|false">
        if (buf_.compare(0, std::strlen(kParamOpenPfx), kParamOpenPfx) == 0) {
            size_t close_q = buf_.find('"', std::strlen(kParamOpenPfx));
            if (close_q == std::string::npos) return false;
            size_t string_attr = buf_.find("string=\"", close_q);
            if (string_attr == std::string::npos) return false;
            size_t string_q = buf_.find('"', string_attr + 8);
            if (string_q == std::string::npos) return false;
            size_t close_gt = buf_.find('>', string_q);
            if (close_gt == std::string::npos) return false;
            param_name_ = buf_.substr(std::strlen(kParamOpenPfx),
                                      close_q - std::strlen(kParamOpenPfx));
            std::string string_val = buf_.substr(string_attr + 8,
                                                 string_q - (string_attr + 8));
            param_is_string_ = (string_val == "true");
            param_value_.clear();
            buf_.erase(0, close_gt + 1);
            // Emit args JSON opener / separator.
            std::string opener;
            if (!args_emitted_open_brace_) { opener = "{"; args_emitted_open_brace_ = true; }
            else                            { opener = ","; }
            opener += "\"" + json_escape(param_name_) + "\":";
            if (param_is_string_) opener += "\"";
            EmitArgsChunk(opener, out);
            args_param_count_++;
            state_ = State::PARAM_VALUE;
            return true;
        }
        return false;
    }
    case State::PARAM_VALUE: {
        if (consume_literal(buf_, kParamClose)) {
            if (param_is_string_) EmitArgsChunk("\"", out);
            state_ = State::INVOKE;
            return true;
        }
        return false;
    }
    }
    return false;
}

void DsmlParser::DrainPlain(std::vector<ParserEvent> &out) {
    // Drain everything up to the next '<' that *might* start a marker.
    // Anything before the next '<' is safe to emit; the '<...' tail stays buffered.
    while (!buf_.empty()) {
        size_t lt = next_tag(buf_, 0);
        if (lt == std::string::npos) {
            // No tag at all - emit (or accumulate) the whole buffer.
            ParserEvent e;
            if (state_ == State::PARAM_VALUE) {
                std::string esc = param_is_string_ ? json_escape(buf_) : buf_;
                EmitArgsChunk(esc, out);
            } else if (state_ == State::THINK) {
                e.type = ParserEvent::REASONING;
                e.text = buf_;
                out.push_back(std::move(e));
            } else if (state_ == State::TEXT) {
                e.type = ParserEvent::CONTENT;
                e.text = buf_;
                out.push_back(std::move(e));
            }
            // Inside INVOKE / TOOL_CALLS with no marker, raw bytes are
            // structural whitespace - discard.
            buf_.clear();
            return;
        }
        if (lt > 0) {
            std::string chunk = buf_.substr(0, lt);
            buf_.erase(0, lt);
            ParserEvent e;
            if (state_ == State::PARAM_VALUE) {
                std::string esc = param_is_string_ ? json_escape(chunk) : chunk;
                EmitArgsChunk(esc, out);
            } else if (state_ == State::THINK) {
                e.type = ParserEvent::REASONING;
                e.text = chunk;
                out.push_back(std::move(e));
            } else if (state_ == State::TEXT) {
                e.type = ParserEvent::CONTENT;
                e.text = chunk;
                out.push_back(std::move(e));
            }
        }
        // buf_[0] == '<' - try consuming a marker. If we consumed one, loop again.
        if (!TryConsumeMarker(out)) {
            // Could be a partial marker - wait for more bytes.
            if (looks_like_prefix(buf_)) return;
            // Otherwise this '<' is a literal - emit one char and continue.
            std::string one(1, buf_[0]);
            buf_.erase(0, 1);
            ParserEvent e;
            if (state_ == State::PARAM_VALUE) {
                std::string esc = param_is_string_ ? json_escape(one) : one;
                EmitArgsChunk(esc, out);
            } else if (state_ == State::THINK) {
                e.type = ParserEvent::REASONING;
                e.text = one;
                out.push_back(std::move(e));
            } else if (state_ == State::TEXT) {
                e.type = ParserEvent::CONTENT;
                e.text = one;
                out.push_back(std::move(e));
            }
        }
    }
}

void DsmlParser::Feed(const std::string &chunk, std::vector<ParserEvent> &out) {
    buf_ += chunk;
    DrainPlain(out);
}

void DsmlParser::Flush(std::vector<ParserEvent> &out) {
    // At flush time we no longer wait for marker completion - drain everything
    // (the trailing bytes won't grow). Mirror DrainPlain's state-aware
    // classification: PARAM_VALUE bytes become TOOL_ARGS, THINK bytes become
    // REASONING, TEXT bytes become CONTENT, and INVOKE/TOOL_CALLS bytes are
    // structural whitespace (discarded).
    auto emit_plain = [&](const std::string &chunk) {
        if (chunk.empty()) return;
        if (state_ == State::PARAM_VALUE) {
            std::string esc = param_is_string_ ? json_escape(chunk) : chunk;
            EmitArgsChunk(esc, out);
            return;
        }
        if (state_ == State::THINK) {
            ParserEvent e;
            e.type = ParserEvent::REASONING;
            e.text = chunk;
            out.push_back(std::move(e));
            return;
        }
        if (state_ == State::TEXT) {
            ParserEvent e;
            e.type = ParserEvent::CONTENT;
            e.text = chunk;
            out.push_back(std::move(e));
            return;
        }
        // INVOKE / TOOL_CALLS: structural whitespace, discard.
    };
    while (!buf_.empty()) {
        size_t lt = next_tag(buf_, 0);
        if (lt == std::string::npos) {
            emit_plain(buf_);
            buf_.clear();
            return;
        }
        if (lt > 0) {
            std::string chunk = buf_.substr(0, lt);
            buf_.erase(0, lt);
            emit_plain(chunk);
        }
        if (!TryConsumeMarker(out)) {
            // Definitely a literal '<' now (no chance of more bytes arriving).
            std::string one(1, buf_[0]);
            buf_.erase(0, 1);
            emit_plain(one);
        }
    }
    // If we ended mid-tool-call (model truncated), close it cleanly.
    if (state_ == State::INVOKE || state_ == State::PARAM_VALUE) {
        if (state_ == State::PARAM_VALUE && param_is_string_) EmitArgsChunk("\"", out);
        FinishCurrentToolCall(out);
        state_ = State::TEXT;
    }
}

std::string RandomToolId() {
    static thread_local std::mt19937_64 rng{
        static_cast<uint64_t>(std::chrono::system_clock::now().time_since_epoch().count())};
    const char *alphabet =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
    std::string out = "call_";
    for (int i = 0; i < 16; ++i) {
        out += alphabet[rng() % 62];
    }
    return out;
}

} // namespace ds4cpp
