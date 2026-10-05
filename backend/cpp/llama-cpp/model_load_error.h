// SPDX-License-Identifier: MIT

#pragma once

#include <string>

namespace localai {

inline std::string model_load_error_with_hint(const std::string& error) {
  const std::string mismatch = "wrong number of tensors; expected ";
  const std::string got = ", got ";
  const std::string::size_type mismatch_pos = error.find(mismatch);
  if (mismatch_pos == std::string::npos ||
      error.find(got, mismatch_pos + mismatch.size()) == std::string::npos) {
    return error;
  }

  return error +
         " Hint: the model may be incompatible with this llama.cpp backend "
         "or the GGUF file may be corrupt. Try a newer compatible backend "
         "and verify or re-download the model file.";
}

}  // namespace localai
