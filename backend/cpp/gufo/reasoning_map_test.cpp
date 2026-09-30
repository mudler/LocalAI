// reasoning_map_test.cpp: plain main(), no framework (see backend/cpp/run-unit-tests.sh).
#include <iostream>
#include "reasoning_map.h"

static int failures = 0;
#define CHECK(cond) do { if (!(cond)) { ++failures; std::cerr << __FILE__ << ":" << __LINE__ << " FAILED: " #cond "\n"; } } while (0)

using gufo_backend::ParseBool;
using gufo_backend::ResolveReasoning;

int main() {
  // ParseBool: case-insensitive, both spellings, unknown stays unset.
  for (const char* v : {"true", "TRUE", "True", "1", "yes", "YES", "on", "On"}) CHECK(ParseBool(v) == true);
  for (const char* v : {"false", "FALSE", "False", "0", "no", "No", "off", "OFF"}) CHECK(ParseBool(v) == false);
  CHECK(!ParseBool("").has_value());
  CHECK(!ParseBool("maybe").has_value());
  CHECK(!ParseBool("2").has_value());

  {
    // LocalAI keeps an operator's reasoning.disable over a request level and
    // sends both; the operator's disable wins and the effort is dropped.
    const auto r = ResolveReasoning(std::string("false"), std::string("high"));
    CHECK(r.thinking == false);
    CHECK(r.effort.empty());
  }
  {
    const auto r = ResolveReasoning(std::string("False"), std::string("High"));
    CHECK(r.thinking == false);
    CHECK(r.effort.empty());
  }
  {
    const auto r = ResolveReasoning(std::string("true"), std::string("High"));
    CHECK(r.thinking == true);
    CHECK(r.effort == "high");
  }
  {
    const auto r = ResolveReasoning(std::string("TRUE"), std::nullopt);
    CHECK(r.thinking == true);
    CHECK(r.effort.empty());
  }
  {
    const auto r = ResolveReasoning(std::nullopt, std::string("low"));
    CHECK(!r.thinking.has_value());
    CHECK(r.effort == "low");
  }
  {
    const auto r = ResolveReasoning(std::nullopt, std::string("None"));
    CHECK(!r.thinking.has_value());
    CHECK(r.effort == "none");
  }
  {
    const auto r = ResolveReasoning(std::nullopt, std::string("off"));
    CHECK(!r.thinking.has_value());
    CHECK(r.effort == "off");
  }
  {
    // Garbage is passed through lower-cased; the engine rejects it by name.
    const auto r = ResolveReasoning(std::nullopt, std::string("Turbo"));
    CHECK(r.effort == "turbo");
  }
  {
    // An unknown enable_thinking value leaves thinking unset.
    const auto r = ResolveReasoning(std::string("maybe"), std::string("medium"));
    CHECK(!r.thinking.has_value());
    CHECK(r.effort == "medium");
  }
  {
    const auto r = ResolveReasoning(std::nullopt, std::nullopt);
    CHECK(!r.thinking.has_value());
    CHECK(r.effort.empty());
  }
  return failures == 0 ? 0 : 1;
}
