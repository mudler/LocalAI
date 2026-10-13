#include <iostream>
#include "gfx_gate.h"

static int failures = 0;
#define CHECK(cond) do { if (!(cond)) { ++failures; std::cerr << __FILE__ << ":" << __LINE__ << " FAILED: " #cond "\n"; } } while (0)

using namespace gufo_backend;

static const char* kStrixNode =
    "cpu_cores_count 0\nsimd_count 40\nmem_banks_count 1\nfamily_id 150\n"
    "vendor_id 4098\ndevice_id 5510\ngfx_target_version 110501\ndrm_render_minor 128\n";
static const char* kCpuNode = "cpu_cores_count 32\nsimd_count 0\ngfx_target_version 0\n";
static const char* kOtherGpu = "simd_count 96\ngfx_target_version 110000\n";

int main() {
  CHECK(ParseGfxTargetVersion(kStrixNode) == 110501);
  CHECK(ParseGfxTargetVersion(kCpuNode) == 0);
  CHECK(ParseGfxTargetVersion("") == 0);
  CHECK(ParseGfxTargetVersion("gfx_target_version notanumber") == 0);
  CHECK(kGfx1151 == 110501);
  CHECK(AnyNodeIs({kCpuNode, kStrixNode}, kGfx1151));
  CHECK(!AnyNodeIs({kCpuNode, kOtherGpu}, kGfx1151));
  CHECK(!AnyNodeIs({}, kGfx1151));
  CHECK(GateMessage().find("gfx1151") != std::string::npos);

  // sysfs formatting variants must still match.
  CHECK(ParseGfxTargetVersion("simd_count 40\r\ngfx_target_version 110501\r\n") == kGfx1151);
  CHECK(ParseGfxTargetVersion("  gfx_target_version\t\t 110501  \n") == kGfx1151);
  CHECK(ParseGfxTargetVersion("simd_count 40\ngfx_target_version 110501") == kGfx1151);
  CHECK(ParseGfxTargetVersion("name some gpu name\ngfx_target_version 110501\n") == kGfx1151);

  // Values that a plain unsigned cast would fold into 110501 must not pass
  // the gate: 2^32 + 110501, a negative that strtoull wraps, trailing junk.
  CHECK(ParseGfxTargetVersion("gfx_target_version 4295077797\n") == 0);
  CHECK(ParseGfxTargetVersion("gfx_target_version -18446744073709441115\n") == 0);
  CHECK(ParseGfxTargetVersion("gfx_target_version 110501abc\n") == 0);
  CHECK(ParseGfxTargetVersion("gfx_target_version -1\n") == 0);
  CHECK(ParseGfxTargetVersion("gfx_target_version 99999999999999999999999\n") == 0);
  CHECK(ParseGfxTargetVersion("gfx_target_version 4294967295\n") == 4294967295u);

  // Only "1" or "true" skip the gate; "0" or an empty value must not.
  CHECK(SkipGfxCheck("1"));
  CHECK(SkipGfxCheck("true"));
  CHECK(!SkipGfxCheck(nullptr));
  CHECK(!SkipGfxCheck(""));
  CHECK(!SkipGfxCheck("0"));
  CHECK(!SkipGfxCheck("false"));
  CHECK(!SkipGfxCheck("yes"));

  // First match wins; KFD never repeats a key, so this only pins the behavior.
  CHECK(ParseGfxTargetVersion("gfx_target_version 110000\ngfx_target_version 110501\n") == 110000);
  return failures == 0 ? 0 : 1;
}
