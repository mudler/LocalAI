// Resolves the drafter GGUF path. Standard library only, so draft_path_test.cpp
// runs under backend/cpp/run-unit-tests.sh.
#pragma once

#include <filesystem>
#include <string>

namespace gufo_backend {

// The draft_model option comes from a model YAML, which a gallery or an
// importer can write; confining it beside the model file keeps such a config
// from pointing the backend at an arbitrary host file.
inline bool IsSafeRelativePath(const std::string& path) {
  if (path.empty()) return false;
  const std::filesystem::path p(path);
  if (p.has_root_path()) return false;
  for (const auto& part : p)
    if (part == "..") return false;
  return true;
}

// The draft_model option is resolved beside the model file. Without it,
// LocalAI's own draft_model field (ModelOptions.DraftModel) is used as given:
// LocalAI already joined it under the models directory.
inline bool ResolveDraftModel(const std::string& option, const std::string& model_options_draft,
                              const std::string& model_file, std::string* out, std::string* error) {
  if (option.empty()) {
    *out = model_options_draft;
    return true;
  }
  if (!IsSafeRelativePath(option)) {
    *error = "gufo: draft_model must be a relative path without \"..\", resolved beside the model file: " + option;
    return false;
  }
  *out = (std::filesystem::path(model_file).parent_path() / option).string();
  return true;
}

}  // namespace gufo_backend
