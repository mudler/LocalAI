// vibevoice.cpp ships its purego-friendly ABI in vibevoice_capi.h.
// This translation unit is intentionally tiny: pulling in the header
// (and linking libvibevoice PRIVATE in CMake) is enough to make the
// vv_capi_* symbols visible from the produced MODULE library.
//
// We do install a ggml log redirect so backend logs land on the gRPC
// server's stderr — same pattern as backend/go/qwen3-tts-cpp/cpp/.

#include "govibevoicecpp.h"

#include "ggml.h"
#include "ggml-backend.h"

#include <cstdio>

namespace {

void govibevoice_log_cb(enum ggml_log_level level, const char* msg, void* /*ud*/) {
    if (!msg) return;
    const char* tag = "?????";
    switch (level) {
    case GGML_LOG_LEVEL_DEBUG: tag = "DEBUG"; break;
    case GGML_LOG_LEVEL_INFO:  tag = "INFO";  break;
    case GGML_LOG_LEVEL_WARN:  tag = "WARN";  break;
    case GGML_LOG_LEVEL_ERROR: tag = "ERROR"; break;
    default: break;
    }
    std::fprintf(stderr, "[%-5s] %s", tag, msg);
    std::fflush(stderr);
}

struct LogInstaller {
    LogInstaller() {
        ggml_log_set(govibevoice_log_cb, nullptr);
        ggml_backend_load_all();
    }
};

LogInstaller g_install;

}  // namespace
