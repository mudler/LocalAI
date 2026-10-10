// draft_path_test.cpp: plain main(), no framework (see backend/cpp/run-unit-tests.sh).
#include <iostream>
#include "draft_path.h"

static int failures = 0;
#define CHECK(cond) do { if (!(cond)) { ++failures; std::cerr << __FILE__ << ":" << __LINE__ << " FAILED: " #cond "\n"; } } while (0)

using gufo_backend::IsSafeRelativePath;
using gufo_backend::ResolveDraftModel;

int main() {
  CHECK(IsSafeRelativePath("drafter.gguf"));
  CHECK(IsSafeRelativePath("sub/dir/drafter.gguf"));
  CHECK(IsSafeRelativePath("./drafter.gguf"));
  CHECK(IsSafeRelativePath("a..b.gguf"));  // dots inside a name are not a parent step
  CHECK(!IsSafeRelativePath(""));
  CHECK(!IsSafeRelativePath("/etc/passwd"));
  CHECK(!IsSafeRelativePath(".."));
  CHECK(!IsSafeRelativePath("../other/drafter.gguf"));
  CHECK(!IsSafeRelativePath("sub/../../drafter.gguf"));
  CHECK(!IsSafeRelativePath("sub/.."));
  CHECK(!IsSafeRelativePath("sub//../x"));

  const std::string model = "/models/gufo/qwen/model.gguf";
  {
    std::string out, err;
    CHECK(ResolveDraftModel("drafter.gguf", "", model, &out, &err));
    CHECK(out == "/models/gufo/qwen/drafter.gguf");
    CHECK(err.empty());
  }
  {
    // The option wins over ModelOptions.DraftModel.
    std::string out, err;
    CHECK(ResolveDraftModel("drafter.gguf", "/models/other.gguf", model, &out, &err));
    CHECK(out == "/models/gufo/qwen/drafter.gguf");
  }
  {
    std::string out, err;
    CHECK(!ResolveDraftModel("/etc/passwd", "", model, &out, &err));
    CHECK(err.find("draft_model") != std::string::npos);
    CHECK(err.find("relative") != std::string::npos);
  }
  {
    std::string out, err;
    CHECK(!ResolveDraftModel("../../secret.gguf", "", model, &out, &err));
    CHECK(err.find("draft_model") != std::string::npos);
  }
  {
    // LocalAI's draft_model field arrives joined under the models directory.
    std::string out, err;
    CHECK(ResolveDraftModel("", "/models/gufo/qwen/drafter.gguf", model, &out, &err));
    CHECK(out == "/models/gufo/qwen/drafter.gguf");
  }
  {
    std::string out = "stale", err;
    CHECK(ResolveDraftModel("", "", model, &out, &err));
    CHECK(out.empty());
  }
  return failures == 0 ? 0 : 1;
}
