#pragma once
#include <functional>
#include <string>
#include <vector>

namespace ds4cpp {

struct ParserEvent {
    enum Type { CONTENT, REASONING, TOOL_START, TOOL_ARGS, TOOL_END };
    Type type;
    std::string text;        // CONTENT, REASONING, TOOL_ARGS
    std::string tool_name;   // TOOL_START
    std::string tool_id;     // TOOL_START (caller-assigned)
    int index = 0;           // TOOL_START / TOOL_ARGS / TOOL_END
};

// Streaming parser. Stateless across instances; one per Predict call.
class DsmlParser {
public:
    DsmlParser();

    // Feed a chunk of raw model-emitted text. Appends classified events to
    // `out`. May buffer the tail of `chunk` internally if it looks like a
    // marker prefix.
    void Feed(const std::string &chunk, std::vector<ParserEvent> &out);

    // Flush any remaining buffered text as CONTENT (called at generation end).
    void Flush(std::vector<ParserEvent> &out);

    // True when the parser is inside a DSML structural position - that is,
    // tags/markers between tool-call boundaries where the model is expected
    // to emit protocol bytes verbatim. Mirrors ds4_server.c's "force
    // temperature=0 unless dsml_decode_state_uses_payload_sampling" rule:
    //
    //   TEXT / THINK                  -> false (user sampling applies)
    //   PARAM_VALUE                   -> false (payload uses user sampling)
    //   TOOL_CALLS / INVOKE           -> true  (structural; force greedy)
    //
    // Callers should use this BEFORE the next sample() call to pick the
    // effective temperature; the parser's state reflects what's already
    // been consumed, so it predicts the next token's classification.
    bool IsInDsmlStructural() const;

private:
    enum class State { TEXT, THINK, TOOL_CALLS, INVOKE, PARAM_VALUE };
    State state_ = State::TEXT;
    std::string buf_;
    std::string current_tool_name_;
    int tool_index_ = -1;
    // While parsing a parameter value:
    std::string param_name_;
    bool param_is_string_ = true;
    std::string param_value_;
    // Incrementally-built arguments JSON for the active tool call.
    std::string args_json_so_far_;
    bool args_emitted_open_brace_ = false;
    int args_param_count_ = 0;

    // Try to consume one structural marker starting at buf_[0]. Returns true
    // and advances state if a complete marker was consumed; false if the
    // buffer is ambiguous (could be a marker prefix).
    bool TryConsumeMarker(std::vector<ParserEvent> &out);

    // Drain plain text from buf_ as far as we're sure it's not a marker prefix.
    // Emits CONTENT or REASONING depending on current state.
    void DrainPlain(std::vector<ParserEvent> &out);

    // Emit the next chunk of arguments JSON to the consumer.
    void EmitArgsChunk(const std::string &chunk, std::vector<ParserEvent> &out);
    void FinishCurrentToolCall(std::vector<ParserEvent> &out);
};

// Generate a random tool call ID (e.g. "call_AbCdEf"). Used by the gRPC layer
// when assigning IDs to streamed tool calls.
std::string RandomToolId();

} // namespace ds4cpp
