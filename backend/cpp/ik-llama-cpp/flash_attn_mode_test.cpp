#include "flash_attn_mode.h"

#include <cstdio>

static int failures = 0;

static void expect(const char * value, std::optional<bool> want) {
    if (ik_grpc::parse_flash_attn(value) != want) {
        std::fprintf(stderr, "parse_flash_attn(\"%s\") gave the wrong mode\n", value);
        failures++;
    }
}

int main() {
    // LocalAI's default must not turn flash attention off.
    expect("auto", true);
    expect("on", true);
    expect("enabled", true);
    expect("true", true);
    expect("1", true);
    expect("ON", true);
    expect("off", false);
    expect("disabled", false);
    expect("false", false);
    expect("0", false);
    expect("", std::nullopt);
    expect("sometimes", std::nullopt);
    return failures == 0 ? 0 : 1;
}
