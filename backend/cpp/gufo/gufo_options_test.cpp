// gufo_options_test.cpp: plain main(), no framework (see backend/cpp/run-unit-tests.sh).
#include <iostream>
#include "gufo_options.h"

static int failures = 0;
#define CHECK(cond) do { if (!(cond)) { ++failures; std::cerr << __FILE__ << ":" << __LINE__ << " FAILED: " #cond "\n"; } } while (0)

using gufo_backend::ParseOptions;

int main() {
  {
    auto r = ParseOptions({});
    CHECK(r.ok());
    CHECK(r.options.speculative.empty());
    CHECK(r.options.max_buffered_output_bytes == 8388608);  // raised from gufo's 64 KiB
    CHECK(r.options.max_buffered_output_bytes_total == 33554432);
    CHECK(r.options.sessions == 1);
  }
  {
    auto r = ParseOptions({"speculative:dflash2", "draft_model:Qwen3.8-27B-DFlash2-Q4_K_M.gguf", "draft_tokens:5"});
    CHECK(r.ok());
    CHECK(r.options.speculative == "dflash2");
    CHECK(r.options.draft_model == "Qwen3.8-27B-DFlash2-Q4_K_M.gguf");
    CHECK(r.options.draft_tokens == 5);
  }
  {
    auto r = ParseOptions({"speculative:turbo"});
    CHECK(!r.ok());
    CHECK(r.error.find("speculative") != std::string::npos);
  }
  {
    auto r = ParseOptions({"prallel:2"});
    CHECK(!r.ok());
    CHECK(r.error.find("prallel") != std::string::npos);  // a typo is named, not ignored
  }
  {
    CHECK(!ParseOptions({"draft_tokens:0"}).ok());          // must be positive
    CHECK(!ParseOptions({"draft_tokens:abc"}).ok());
    CHECK(!ParseOptions({"sessions:0"}).ok());
    CHECK(!ParseOptions({"think:maybe"}).ok());
    CHECK(!ParseOptions({"nocolon"}).ok());
  }
  {
    auto r = ParseOptions({"think:false", "preserve_thinking:true", "reasoning_effort:high"});
    CHECK(r.ok());
    CHECK(r.options.think.has_value() && !*r.options.think);
    CHECK(r.options.preserve_thinking.has_value() && *r.options.preserve_thinking);
    CHECK(r.options.reasoning_effort == "high");
  }
  {
    auto r = ParseOptions({"cache_disk:/var/cache/gufo", "cache_disk_bytes:1073741824", "request_timeout_ms:30000"});
    CHECK(r.ok());
    CHECK(r.options.cache_disk == "/var/cache/gufo");
    CHECK(r.options.cache_disk_bytes == 1073741824ULL);
    CHECK(r.options.request_timeout_ms == 30000);
  }
  {
    auto r = ParseOptions({"reasoning_effort:extreme"});
    CHECK(!r.ok());                                          // minimal|low|medium|high|xhigh|max
  }
  {
    // The MTP head is a separate GGUF in gufo, so mtp needs draft_model like dflash2 and dspark.
    auto r = ParseOptions({"speculative:mtp"});
    CHECK(!r.ok());
    CHECK(r.error.find("draft_model") != std::string::npos);
  }
  {
    auto r = ParseOptions({"speculative:mtp", "draft_model:x.gguf"});
    CHECK(r.ok());
    CHECK(r.options.speculative == "mtp");
    CHECK(r.options.draft_model == "x.gguf");
  }
  {
    CHECK(ParseOptions({"speculative:off"}).ok());
    CHECK(!ParseOptions({"speculative:dflash2"}).ok());
  }
  return failures == 0 ? 0 : 1;
}
