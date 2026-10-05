// SPDX-License-Identifier: MIT
#include <kimodo/kimodo_capi.h>
#include "../sources/kimodo.cpp/src/skeleton.hpp"
#include "../sources/kimodo.cpp/src/llm_tokenizer.hpp"

#include <cstdlib>
#include <cstdio>
#include <cstring>
#include <filesystem>

#ifdef KIMODO_HAVE_GGML_VULKAN
#include <ggml-vulkan.h>
#endif

namespace {
const kimodo::detail::skeleton_spec *skeleton(int joints) {
  switch (joints) {
    case 22: return kimodo::detail::find_skeleton("smplx22");
    case 30: return kimodo::detail::find_skeleton("soma30");
    case 34: return kimodo::detail::find_skeleton("g1skel34");
    default: return nullptr;
  }
}
}

extern "C" {
// Use the encoder's tokenizer implementation and vocabulary, including BOS.
// Keep it loaded so accounting does not reload the vocabulary on every call.
KIMODO_API void *localai_kimodo_tokenizer_load(const char *source, char *error, int capacity) {
  try {
    auto path = std::filesystem::path(source);
    if (!std::filesystem::is_directory(path)) path = path.parent_path();
    auto tokenizer = kimodo::detail::llm_tokenizer::load((path / "tokenizer.gguf").string());
    if (!tokenizer) {
      if (error && capacity > 0) std::snprintf(error, capacity, "%s", tokenizer.error().c_str());
      return nullptr;
    }
    return tokenizer->release();
  } catch (const std::exception &e) {
    if (error && capacity > 0) std::snprintf(error, capacity, "%s", e.what());
    return nullptr;
  } catch (...) {
    if (error && capacity > 0) std::snprintf(error, capacity, "unknown tokenizer error");
    return nullptr;
  }
}

KIMODO_API void localai_kimodo_tokenizer_free(void *tokenizer) {
  delete static_cast<kimodo::detail::llm_tokenizer *>(tokenizer);
}

KIMODO_API int localai_kimodo_prompt_tokens(void *tokenizer, const char *prompt) {
  if (!tokenizer || !prompt) return -1;
  try {
    auto ids = static_cast<kimodo::detail::llm_tokenizer *>(tokenizer)->encode(prompt);
    if (!ids || ids->size() < 2 || ids->size() > 512) return -1;
    return static_cast<int>(ids->size());
  } catch (...) {
    return -1;
  }
}

// The upstream runtime currently reads environment variables instead of its
// C API runtime_options. Set the native environment before creating a session.
KIMODO_API int localai_kimodo_configure(const char *device, int threads, int chunk) {
  if (!device || threads < 1 || chunk < 1 || chunk > 32) return -1;
  if (std::strcmp(device, "cpu") && std::strcmp(device, "vulkan") && std::strcmp(device, "auto")) return -1;
  if (std::strcmp(device, "vulkan") == 0) {
#ifdef KIMODO_HAVE_GGML_VULKAN
    try {
      if (ggml_backend_vk_get_device_count() == 0) return -2;
    } catch (...) {
      // GGML can throw when no working ICD is installed. Never unwind into Go.
      return -2;
    }
#else
    return -2;
#endif
  }
  char thread_value[32], chunk_value[32];
  std::snprintf(thread_value, sizeof(thread_value), "%d", threads);
  std::snprintf(chunk_value, sizeof(chunk_value), "%d", chunk);
  if (setenv("KIMODO_BACKEND", device, 1) != 0 ||
      setenv("KIMODO_THREADS", thread_value, 1) != 0 ||
      setenv("KIMODO_TEXT_LAYER_CHUNK", chunk_value, 1) != 0) return -1;
  return 0;
}

KIMODO_API const char *localai_kimodo_joint_name(int joints, int joint) {
  const auto *spec = skeleton(joints);
  return spec && joint >= 0 && joint < joints ? spec->names[joint].data() : nullptr;
}

KIMODO_API int localai_kimodo_joint_parent(int joints, int joint) {
  const auto *spec = skeleton(joints);
  return spec && joint >= 0 && joint < joints ? spec->parents[joint] : -2;
}

KIMODO_API const float *localai_kimodo_joint_offset(int joints, int joint) {
  const auto *spec = skeleton(joints);
  return spec && joint >= 0 && joint < joints ? spec->offsets[joint].data() : nullptr;
}
}
