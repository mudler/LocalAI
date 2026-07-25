#include "model_options.h"

#include <cctype>
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
// empty strings, signs, and trailing garbage.
bool parse_non_negative_int(const std::string &value, int &out) {
    if (value.empty()) {
        return false;
    }
    for (const char ch : value) {
        if (std::isdigit(static_cast<unsigned char>(ch)) == 0) {
            return false;
        }
    }
    out = std::atoi(value.c_str());
    return true;
}

bool starts_with(const std::string &value, const std::string &prefix) {
    return value.size() >= prefix.size() &&
           value.compare(0, prefix.size(), prefix) == 0;
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
            parsed.options.backend = value;
        } else if (key == "model_spec_override") {
            parsed.options.model_spec_override = value;
        } else if (key == "device") {
            if (!parse_non_negative_int(value, parsed.options.device)) {
                parsed.error = "audio-cpp: option 'device' needs a non-negative "
                               "integer, got '" + value + "'";
                return parsed;
            }
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
        } else {
            parsed.error = "audio-cpp: unknown option key '" + key +
                           "'. Known keys: family, task, backend, device, "
                           "threads, model_spec_override, busy_timeout_ms, "
                           "load.<key>, session.<key>";
            return parsed;
        }
    }

    return parsed;
}

} // namespace audiocpp_backend
