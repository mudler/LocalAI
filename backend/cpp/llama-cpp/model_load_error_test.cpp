// SPDX-License-Identifier: MIT

#include "model_load_error.h"

#include <cassert>
#include <string>

int main() {
  const std::string issue_error =
      "llama_model_load: error loading model: done_getting_tensors: wrong number of tensors; expected 2131, got 720; "
      "llama_model_load_from_file_impl: failed to load model";
  const std::string issue_result = localai::model_load_error_with_hint(issue_error);
  assert(issue_result.compare(0, issue_error.size(), issue_error) == 0);
  assert(issue_result.find("incompatible") != std::string::npos);
  assert(issue_result.find("corrupt") != std::string::npos);

  const std::string generic_error =
      "wrong number of tensors; expected 42, got 17";
  const std::string generic_result = localai::model_load_error_with_hint(generic_error);
  assert(generic_result.compare(0, generic_error.size(), generic_error) == 0);
  assert(generic_result.size() > generic_error.size());

  const std::string unrelated_error = "failed to open GGUF file";
  assert(localai::model_load_error_with_hint(unrelated_error) == unrelated_error);
}
