// SPDX-License-Identifier: MIT

#pragma once

#include <cstdlib>
#include <functional>
#include <string>
#include <utility>
#include <vector>

#ifndef _WIN32
#include <unistd.h>
extern "C" char ** environ;
#endif

namespace llama_grpc {

struct passthrough_gpu_layers_state {
    int main;
    int draft;
};

inline passthrough_gpu_layers_state prepare_passthrough_gpu_layers(
    int & main_gpu_layers,
    int & draft_gpu_layers) {
    const passthrough_gpu_layers_state saved{
        main_gpu_layers,
        draft_gpu_layers,
    };
    main_gpu_layers = -1;
    draft_gpu_layers = -1;
    return saved;
}

inline void restore_passthrough_gpu_layers(
    int & main_gpu_layers,
    int & draft_gpu_layers,
    passthrough_gpu_layers_state saved,
    bool main_overridden = false,
    bool draft_overridden = false) {
    if (!main_overridden && main_gpu_layers == -1) {
        main_gpu_layers = saved.main;
    }
    if (!draft_overridden && draft_gpu_layers == -1) {
        draft_gpu_layers = saved.draft;
    }
}

// Upstream's parser reads more than the argument list: every LLAMA_ARG_*
// environment variable and the llama.cpp config files (/etc/llama.cpp and
// $XDG_CONFIG_HOME/llama.cpp, or ~/.config/llama.cpp) are applied on top of the
// params LocalAI has already resolved. That would let an unrelated variable in
// the process environment change a model's settings, and only for models that
// carry a raw flag. This guard hides the variables, and points the user-level
// config directory at a path that does not exist, while the parser runs. The
// system-wide /etc/llama.cpp/config.ini cannot be hidden this way.
// It is a no-op on Windows.
class scoped_upstream_env_scrub {
public:
    scoped_upstream_env_scrub() {
#ifndef _WIN32
        std::vector<std::string> names;
        for (char ** e = environ; e != nullptr && *e != nullptr; ++e) {
            const std::string entry = *e;
            if (entry.rfind("LLAMA_ARG_", 0) == 0) {
                names.push_back(entry.substr(0, entry.find('=')));
            }
        }
        for (const auto & name : names) {
            const char * v = std::getenv(name.c_str());
            saved_.emplace_back(name, v ? std::string(v) : std::string());
            unsetenv(name.c_str());
        }
        const char * xdg = std::getenv("XDG_CONFIG_HOME");
        had_xdg_ = xdg != nullptr;
        if (xdg) {
            xdg_ = xdg;
        }
        setenv("XDG_CONFIG_HOME", "/nonexistent-localai-no-llama-config", 1);
#endif
    }
    ~scoped_upstream_env_scrub() {
#ifndef _WIN32
        for (const auto & kv : saved_) {
            setenv(kv.first.c_str(), kv.second.c_str(), 1);
        }
        if (had_xdg_) {
            setenv("XDG_CONFIG_HOME", xdg_.c_str(), 1);
        } else {
            unsetenv("XDG_CONFIG_HOME");
        }
#endif
    }
    scoped_upstream_env_scrub(const scoped_upstream_env_scrub &) = delete;
    scoped_upstream_env_scrub & operator=(const scoped_upstream_env_scrub &) = delete;

private:
    std::vector<std::pair<std::string, std::string>> saved_;
    bool had_xdg_ = false;
    std::string xdg_;
};

// One raw upstream flag: its name and, when it takes one, its value.
using passthrough_flag = std::vector<std::string>;

// keep_parsable_flags returns the flags that `parses` accepts one by one and
// appends the rejected ones to `dropped`. It is for the case where the whole
// list failed to parse, so one bad flag does not discard the good ones.
inline std::vector<passthrough_flag> keep_parsable_flags(
    const std::vector<passthrough_flag> & flags,
    const std::function<bool(const passthrough_flag &)> & parses,
    std::vector<passthrough_flag> & dropped) {
    std::vector<passthrough_flag> kept;
    for (const auto & f : flags) {
        if (parses(f)) {
            kept.push_back(f);
        } else {
            dropped.push_back(f);
        }
    }
    return kept;
}

} // namespace llama_grpc
