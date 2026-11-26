#pragma once

#include <algorithm>
#include <cctype>
#include <optional>
#include <string>

namespace ik_grpc {

// parse_flash_attn maps the flash_attention value of a model config to the
// switch of ik_llama.cpp, which is on or off and has no auto mode. LocalAI
// sends "auto" when the config does not set the key, and ik_llama.cpp's own
// -fa flag reads "auto" as on, so it is on here too. Accepting only "true" and
// "1", as this backend did, turned flash attention off for every model that
// left the key unset. An empty or unknown value returns nullopt: the caller
// keeps the default of ik_llama.cpp, which is on.
inline std::optional<bool> parse_flash_attn(std::string value) {
    std::transform(value.begin(), value.end(), value.begin(),
                   [](unsigned char c) { return static_cast<char>(std::tolower(c)); });
    if (value == "auto" || value == "on" || value == "enabled" || value == "true" || value == "1") {
        return true;
    }
    if (value == "off" || value == "disabled" || value == "false" || value == "0") {
        return false;
    }
    return std::nullopt;
}

} // namespace ik_grpc
