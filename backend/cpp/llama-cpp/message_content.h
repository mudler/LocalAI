#pragma once

#include <string>
#include <vector>

#include <nlohmann/json.hpp>

namespace llama_grpc {

// Normalizes a proto message's content string into the JSON value used when
// reconstructing OpenAI-format messages for the tokenizer (jinja) template.
//
// Shared by the streaming (PredictStream) and non-streaming (Predict) message
// reconstruction paths so the two cannot drift.
//
// LocalAI's Go layer (schema.Messages.ToProto) always sends content as a plain
// text string; multimodal media travels in separate proto fields, never inside
// content. So user/system/developer content is *only ever* opaque text and must
// NOT be JSON-sniffed: a prompt that merely looks like JSON (e.g. an ingredient
// list ["1/4 cup sugar", ...]) would otherwise be reinterpreted as structured
// content parts and rejected by oaicompat_chat_params_parse with
// "unsupported content[].type" (https://github.com/mudler/LocalAI/issues/10524).
// (developer is OpenAI's modern system alias - same "human-authored text" nature.)
//
// For assistant/tool messages we still collapse a literal JSON null/object
// (tool-call bookkeeping) to a string, but we never turn a plain string into an
// array/scalar. The array defense is therefore role-independent (arrays/scalars
// fall through for every role); the role gate only governs the null/object case.
inline nlohmann::ordered_json normalize_message_content(const std::string& role,
                                                        const std::string& content) {
    nlohmann::ordered_json content_val = content;
    if (role != "user" && role != "system" && role != "developer") {
        try {
            nlohmann::ordered_json parsed = nlohmann::ordered_json::parse(content);
            if (parsed.is_null()) {
                content_val = "";
            } else if (parsed.is_object()) {
                content_val = parsed.dump();
            }
            // arrays / scalars: keep the original plain-text string as-is
        } catch (const nlohmann::ordered_json::parse_error&) {
            // Not JSON, already the plain string
        }
    }
    return content_val;
}

// Final safety pass applied to each reconstructed OpenAI message right before it
// is handed to oaicompat_chat_params_parse (jinja templating). Jinja templates
// assume content is a string: a literal null breaks slicing such as
// message.content[:N] (#7324), and a tool message with array content is rejected
// (#7528). A multimodal user message legitimately carries a typed-part array
// ({type:text}, {type:image_url}, ...), which must be left intact. Shared by the
// streaming and non-streaming paths so this invariant cannot drift between them.
inline void normalize_template_message(nlohmann::ordered_json& msg) {
    if (!msg.contains("content")) {
        msg["content"] = ""; // templates expect the field to exist
        return;
    }
    nlohmann::ordered_json& content = msg["content"];
    const std::string role = (msg.contains("role") && msg["role"].is_string())
                                 ? msg["role"].get<std::string>()
                                 : std::string();
    if (content.is_null()) {
        content = ""; // #7324: null would crash content[:N] slicing
    } else if (role == "tool" && content.is_array()) {
        content = content.dump(); // #7528: tool messages must have string content
    } else if (!content.is_string() && !content.is_array()) {
        if (content.is_object()) {
            content = content.dump(); // tool-call bookkeeping object -> string
        } else {
            content = ""; // other scalar (number/bool) -> empty
        }
    }
    // string, or a non-tool (multimodal) typed-part array: leave untouched
}

// One proto message's data, flattened to plain types so the reconstruction logic
// can be shared and unit-tested without protobuf. The streaming and non-streaming
// predict paths both populate this from proto::Message + the request's media.
struct ReconstructedMessageInput {
    std::string role;
    std::string content;            // proto.Message.content (always a plain string)
    std::string name;
    std::string tool_call_id;
    std::string reasoning_content;
    std::string tool_calls;         // tool_calls as a JSON string, or empty
    bool is_last_user_msg = false;  // attach request media to this message
    std::vector<std::string> images; // base64 (jpeg)
    std::vector<std::string> audios; // base64 (wav)
    std::vector<std::string> videos; // base64
};

// Appends the request's media as OpenAI typed content parts. Imperative (not
// brace-init) to avoid nlohmann's object-vs-array initializer-list ambiguity.
inline void append_media_parts(nlohmann::ordered_json& content_array,
                               const std::vector<std::string>& images,
                               const std::vector<std::string>& audios,
                               const std::vector<std::string>& videos) {
    for (const auto& img : images) {
        nlohmann::ordered_json image_chunk;
        image_chunk["type"] = "image_url";
        nlohmann::ordered_json image_url;
        image_url["url"] = "data:image/jpeg;base64," + img;
        image_chunk["image_url"] = image_url;
        content_array.push_back(image_chunk);
    }
    for (const auto& aud : audios) {
        nlohmann::ordered_json audio_chunk;
        audio_chunk["type"] = "input_audio";
        nlohmann::ordered_json input_audio;
        input_audio["data"] = aud;
        input_audio["format"] = "wav"; // default; could be made configurable
        audio_chunk["input_audio"] = input_audio;
        content_array.push_back(audio_chunk);
    }
    for (const auto& vid : videos) {
        nlohmann::ordered_json video_chunk;
        video_chunk["type"] = "input_video";
        nlohmann::ordered_json input_video;
        input_video["data"] = vid;
        video_chunk["input_video"] = input_video;
        content_array.push_back(video_chunk);
    }
}

// Reconstructs a single OpenAI-format message (the object fed to
// oaicompat_chat_params_parse) from a proto message. Shared by PredictStream and
// Predict so the content/multimodal/tool_calls handling cannot drift between the
// two stream modes (it previously lived as two ~150-line copies with a redundant
// Predict-only tool_calls->" " branch). Guarantees content is always a string or
// a typed-part array, never null/missing.
inline nlohmann::ordered_json build_reconstructed_message(const ReconstructedMessageInput& in) {
    nlohmann::ordered_json msg_json;
    msg_json["role"] = in.role;
    const bool has_media = !in.images.empty() || !in.audios.empty() || !in.videos.empty();

    if (!in.content.empty()) {
        nlohmann::ordered_json content_val = normalize_message_content(in.role, in.content);
        if (content_val.is_string() && in.is_last_user_msg && has_media) {
            // Last user message + media: build a typed-part array (text first).
            nlohmann::ordered_json content_array = nlohmann::ordered_json::array();
            nlohmann::ordered_json text_part;
            text_part["type"] = "text";
            text_part["text"] = content_val.get<std::string>();
            content_array.push_back(text_part);
            append_media_parts(content_array, in.images, in.audios, in.videos);
            msg_json["content"] = content_array;
        } else if (content_val.is_null()) {
            msg_json["content"] = "";
        } else {
            msg_json["content"] = content_val;
        }
    } else if (in.is_last_user_msg && has_media) {
        // No text but media on the last user message: media-only typed array.
        nlohmann::ordered_json content_array = nlohmann::ordered_json::array();
        append_media_parts(content_array, in.images, in.audios, in.videos);
        msg_json["content"] = content_array;
    } else {
        // Empty content (any role, incl. tool/assistant): templates need a string.
        msg_json["content"] = "";
    }

    if (!in.name.empty()) {
        msg_json["name"] = in.name;
    }
    if (!in.tool_call_id.empty()) {
        msg_json["tool_call_id"] = in.tool_call_id;
    }
    if (!in.reasoning_content.empty()) {
        msg_json["reasoning_content"] = in.reasoning_content;
    }
    if (!in.tool_calls.empty()) {
        try {
            nlohmann::ordered_json tool_calls = nlohmann::ordered_json::parse(in.tool_calls);
            msg_json["tool_calls"] = tool_calls;
            // tool_calls + empty/blank content: use " " not "", because llama.cpp's
            // common_chat_msgs_to_json_oaicompat turns "" into null, which breaks
            // templates that slice message.content[:tool_start_length] (#7324).
            if (!msg_json.contains("content") ||
                (msg_json["content"].is_string() && msg_json["content"].get<std::string>().empty())) {
                msg_json["content"] = " ";
            }
        } catch (const nlohmann::ordered_json::parse_error&) {
            // Malformed tool_calls JSON: leave content as-is (prior behavior).
        }
    }

    return msg_json;
}

}  // namespace llama_grpc
