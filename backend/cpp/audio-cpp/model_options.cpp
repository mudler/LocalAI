#include "model_options.h"

#include <cctype>
#include <cerrno>
#include <climits>
#include <cstdlib>

namespace audiocpp_backend {
namespace {

std::string trim(const std::string &value) {
    size_t begin = 0;
    while (begin < value.size() &&
           std::isspace(static_cast<unsigned char>(value[begin])) != 0) {
        ++begin;
    }
    size_t end = value.size();
    while (end > begin &&
           std::isspace(static_cast<unsigned char>(value[end - 1])) != 0) {
        --end;
    }
    return value.substr(begin, end - begin);
}

// Parses a non-negative integer. Returns false on anything else, including
// empty strings, signs, trailing garbage, and values too large for int.
//
// strtol rather than atoi: atoi is undefined behaviour once the digits exceed
// long, and in practice it hands back a wrapped value. That would let
// "device:2147483648" through as -2147483648 and send a negative index to the
// ggml backend selector, from a function whose error text promises the caller a
// non-negative integer.
bool parse_non_negative_int(const std::string &value, int &out) {
    if (value.empty()) {
        return false;
    }
    for (const char ch : value) {
        if (std::isdigit(static_cast<unsigned char>(ch)) == 0) {
            return false;
        }
    }

    errno = 0;
    char *end = nullptr;
    const long parsed = std::strtol(value.c_str(), &end, 10);
    if (errno == ERANGE || end == nullptr || *end != '\0') {
        return false;
    }
    if (parsed < 0 || parsed > INT_MAX) {
        return false;
    }

    out = static_cast<int>(parsed);
    return true;
}

bool starts_with(const std::string &value, const std::string &prefix) {
    return value.size() >= prefix.size() &&
           value.compare(0, prefix.size(), prefix) == 0;
}

bool is_known_backend(const std::string &value) {
    return value == "cpu" || value == "cuda" || value == "hip" ||
           value == "rocm" || value == "vulkan" || value == "metal" ||
           value == "best";
}

} // namespace

ParsedOptions parse_model_options(const std::vector<std::string> &entries) {
    ParsedOptions parsed;

    for (const auto &raw : entries) {
        const std::string entry = trim(raw);
        if (entry.empty()) {
            continue;
        }

        // Split on the FIRST colon: values are often paths that contain more.
        const size_t sep = entry.find(':');
        if (sep == std::string::npos) {
            parsed.error = "audio-cpp: option '" + entry +
                           "' is not in key:value form";
            return parsed;
        }

        const std::string key = trim(entry.substr(0, sep));
        const std::string value = trim(entry.substr(sep + 1));

        if (starts_with(key, "load.")) {
            const std::string inner = key.substr(5);
            if (inner.empty()) {
                parsed.error = "audio-cpp: option '" + entry +
                               "' has an empty load option name";
                return parsed;
            }
            parsed.options.load_options[inner] = value;
            continue;
        }
        if (starts_with(key, "session.")) {
            const std::string inner = key.substr(8);
            if (inner.empty()) {
                parsed.error = "audio-cpp: option '" + entry +
                               "' has an empty session option name";
                return parsed;
            }
            parsed.options.session_options[inner] = value;
            continue;
        }

        if (key == "family") {
            parsed.options.family = value;
        } else if (key == "task") {
            parsed.options.task = value;
        } else if (key == "backend") {
            if (!is_known_backend(value)) {
                parsed.error = "audio-cpp: unknown backend option '" + value +
                               "'. Known backends: cpu, cuda, hip, rocm, "
                               "vulkan, metal, best";
                return parsed;
            }
            parsed.options.backend = value;
        } else if (key == "model_spec_override") {
            parsed.options.model_spec_override = value;
        } else if (key == "device") {
            if (!parse_non_negative_int(value, parsed.options.device)) {
                parsed.error = "audio-cpp: option 'device' needs a non-negative "
                               "integer, got '" + value + "'";
                return parsed;
            }
            parsed.options.device_set = true;
        } else if (key == "threads") {
            if (!parse_non_negative_int(value, parsed.options.threads)) {
                parsed.error = "audio-cpp: option 'threads' needs a non-negative "
                               "integer, got '" + value + "'";
                return parsed;
            }
        } else if (key == "busy_timeout_ms") {
            if (!parse_non_negative_int(value, parsed.options.busy_timeout_ms)) {
                parsed.error = "audio-cpp: option 'busy_timeout_ms' needs a "
                               "non-negative integer, got '" + value + "'";
                return parsed;
            }
        } else if (key == "live_idle_timeout_ms") {
            if (!parse_non_negative_int(value,
                                        parsed.options.live_idle_timeout_ms)) {
                parsed.error = "audio-cpp: option 'live_idle_timeout_ms' needs a "
                               "non-negative integer, got '" + value + "'";
                return parsed;
            }
        } else {
            // Quotes the whole entry, not just the key: an entry like ":value"
            // has an empty key and would otherwise leave nothing to grep for.
            parsed.error = "audio-cpp: unknown option key '" + entry +
                           "'. Known keys: family, task, backend, device, "
                           "threads, model_spec_override, busy_timeout_ms, "
                           "live_idle_timeout_ms, load.<key>, session.<key>";
            return parsed;
        }
    }

    return parsed;
}

} // namespace audiocpp_backend
