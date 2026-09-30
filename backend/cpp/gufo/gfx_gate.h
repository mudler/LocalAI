// gufo only ships kernels for gfx1151 (AMD Strix Halo), and LocalAI cannot
// tell that GPU from any other AMD GPU, so the backend refuses at load time.
// KFD publishes gfx_target_version per topology node as
// major * 10000 + minor * 100 + stepping (gfx1151 -> 110501, CPU nodes -> 0).
// The value 110501 is checked on the Strix host in the hardware task.
#pragma once

#include <limits>
#include <sstream>
#include <string>
#include <vector>

namespace gufo_backend {

constexpr unsigned kGfx1151 = 110501;

inline unsigned ParseGfxTargetVersion(const std::string& kfd_properties) {
  std::istringstream in(kfd_properties);
  std::string line;
  while (std::getline(in, line)) {
    std::istringstream fields(line);
    std::string key, value;
    if (!(fields >> key >> value) || key != "gfx_target_version") continue;
    // Validate the digits by hand: stream extraction into an unsigned accepts
    // a leading '-' and stops at trailing junk, and a cast to unsigned folds
    // 2^32 + 110501 into 110501, all of which would be false gfx1151 matches.
    if (value.size() > 10) return 0;
    unsigned long long parsed = 0;
    for (char c : value) {
      if (c < '0' || c > '9') return 0;
      parsed = parsed * 10 + static_cast<unsigned>(c - '0');
    }
    if (parsed > std::numeric_limits<unsigned>::max()) return 0;
    return static_cast<unsigned>(parsed);
  }
  return 0;
}

inline bool AnyNodeIs(const std::vector<std::string>& node_properties, unsigned want) {
  for (const std::string& props : node_properties)
    if (ParseGfxTargetVersion(props) == want) return true;
  return false;
}

inline std::string GateMessage() {
  return "gufo requires an AMD gfx1151 (Strix Halo) GPU, and none was found in /sys/class/kfd. "
         "Set GUFO_SKIP_GFX_CHECK=1 to bypass this check on an unusual setup.";
}

}  // namespace gufo_backend
