#include <iostream>
#include "base64.h"

static int failures = 0;
#define CHECK(cond) do { if (!(cond)) { ++failures; std::cerr << __FILE__ << ":" << __LINE__ << " FAILED: " #cond "\n"; } } while (0)

static std::string Str(const std::vector<std::uint8_t>& v) { return std::string(v.begin(), v.end()); }

// Reference encoder, only here to produce round-trip inputs for the decoder.
static std::string Encode(const std::vector<std::uint8_t>& in) {
  static const char* kAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  std::string s;
  std::size_t i = 0;
  for (; i + 2 < in.size(); i += 3) {
    const std::uint32_t v = (in[i] << 16) | (in[i + 1] << 8) | in[i + 2];
    s += kAlphabet[(v >> 18) & 63]; s += kAlphabet[(v >> 12) & 63];
    s += kAlphabet[(v >> 6) & 63];  s += kAlphabet[v & 63];
  }
  if (in.size() - i == 1) {
    const std::uint32_t v = in[i] << 16;
    s += kAlphabet[(v >> 18) & 63]; s += kAlphabet[(v >> 12) & 63]; s += "==";
  } else if (in.size() - i == 2) {
    const std::uint32_t v = (in[i] << 16) | (in[i + 1] << 8);
    s += kAlphabet[(v >> 18) & 63]; s += kAlphabet[(v >> 12) & 63];
    s += kAlphabet[(v >> 6) & 63];  s += '=';
  }
  return s;
}

int main() {
  std::vector<std::uint8_t> out;
  CHECK(gufo_backend::Base64Decode("aGVsbG8=", &out) && Str(out) == "hello");
  CHECK(gufo_backend::Base64Decode("aGVsbG8h", &out) && Str(out) == "hello!");
  CHECK(gufo_backend::Base64Decode("aGVs\nbG8=", &out) && Str(out) == "hello");  // whitespace ignored
  CHECK(gufo_backend::Base64Decode("", &out) && out.empty());
  CHECK(!gufo_backend::Base64Decode("aGVsbG8", &out));      // missing padding
  CHECK(!gufo_backend::Base64Decode("aGV$bG8=", &out));     // bad character
  CHECK(!gufo_backend::Base64Decode("a", &out));

  // Padding rules.
  CHECK(gufo_backend::Base64Decode("aGk=", &out) && Str(out) == "hi");
  CHECK(gufo_backend::Base64Decode("aA==", &out) && Str(out) == "h");
  CHECK(!gufo_backend::Base64Decode("aGVsbG8==", &out));    // one pad too many
  CHECK(!gufo_backend::Base64Decode("a===", &out));
  CHECK(!gufo_backend::Base64Decode("====", &out));
  CHECK(!gufo_backend::Base64Decode("=", &out));
  CHECK(!gufo_backend::Base64Decode("aG=V", &out));         // data after padding
  CHECK(!gufo_backend::Base64Decode("aA==aA==", &out));     // padding mid-stream
  CHECK(!gufo_backend::Base64Decode("aGVsbG8=\x80", &out)); // non-ASCII byte
  CHECK(!gufo_backend::Base64Decode("aGVs-G8_", &out));     // base64url is not accepted
  CHECK(gufo_backend::Base64Decode(" aGVs\r\n\tbG8= \n", &out) && Str(out) == "hello");

  // A failed decode leaves the previous output untouched.
  out = {1, 2, 3};
  CHECK(!gufo_backend::Base64Decode("a", &out) && out.size() == 3);

  // Every byte value, and every tail length, survives a round trip.
  std::vector<std::uint8_t> all(256);
  for (int i = 0; i < 256; ++i) all[i] = static_cast<std::uint8_t>(i);
  for (std::size_t n = 0; n <= all.size(); ++n) {
    const std::vector<std::uint8_t> in(all.begin(), all.begin() + n);
    CHECK(gufo_backend::Base64Decode(Encode(in), &out) && out == in);
  }

  // Images are several MB: a 4 MiB pseudo-random round trip must decode exactly.
  std::vector<std::uint8_t> big(4u << 20);
  std::uint32_t state = 2463534242u;
  for (auto& b : big) { state ^= state << 13; state ^= state >> 17; state ^= state << 5; b = static_cast<std::uint8_t>(state); }
  big.push_back(7);  // odd length exercises the padded tail
  CHECK(gufo_backend::Base64Decode(Encode(big), &out) && out == big);

  return failures == 0 ? 0 : 1;
}
