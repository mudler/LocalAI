// Strict base64 decoder (RFC 4648, padding required). Images reach the backend
// as base64 strings in PredictOptions.Images.
#pragma once

#include <array>
#include <cstdint>
#include <string>
#include <vector>

namespace gufo_backend {

inline bool Base64Decode(const std::string& text, std::vector<std::uint8_t>* out) {
  // A 256-entry table keeps multi-MB image payloads at one lookup per byte.
  static const std::array<std::int8_t, 256> table = [] {
    std::array<std::int8_t, 256> t{};
    t.fill(-1);
    const char* alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    for (int i = 0; i < 64; ++i) t[static_cast<unsigned char>(alphabet[i])] = static_cast<std::int8_t>(i);
    return t;
  }();
  std::vector<std::uint8_t> bytes;
  bytes.reserve(text.size() / 4 * 3 + 3);
  std::uint32_t buffer = 0;
  int bits = 0;
  std::size_t pad = 0, count = 0;
  for (const char ch : text) {
    const auto c = static_cast<unsigned char>(ch);
    if (c == ' ' || c == '\n' || c == '\r' || c == '\t') continue;
    if (c == '=') { ++pad; continue; }
    if (pad != 0) return false;                       // data after padding
    const int v = table[c];
    if (v < 0) return false;
    // Only the low 14 bits are ever read back, so mask to keep the buffer bounded.
    buffer = ((buffer << 6) | static_cast<std::uint32_t>(v)) & 0x3FFFu;
    bits += 6;
    ++count;
    if (bits >= 8) {
      bits -= 8;
      bytes.push_back(static_cast<std::uint8_t>((buffer >> bits) & 0xFF));
    }
  }
  if ((count + pad) % 4 != 0 || pad > 2) return false;
  *out = std::move(bytes);
  return true;
}

}  // namespace gufo_backend
