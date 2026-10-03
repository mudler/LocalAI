// SPDX-License-Identifier: MIT
#pragma once
#include <nlohmann/json.hpp>
#include <stdexcept>
#include <string>
#include <vector>
#include <cstdint>
#include <cstring>
#include "stb/stb_image.h"

// Decision-only limits, mirrored from core/systemone/images.go. The verification
// script checks parity; these must not change ordinary chat or fork backends.
namespace localai_decision {
using json = nlohmann::ordered_json;
constexpr size_t max_images = 8;
constexpr size_t decoded_bytes = 8 << 20;
constexpr size_t encoded_bytes = 12 << 20;
constexpr size_t body_bytes = 16 << 20;
constexpr size_t text_bytes = 64 << 10;
constexpr size_t max_dimension = 4096;
constexpr size_t max_pixels = 16000000;
struct image_error : std::invalid_argument {
    bool too_large;
    image_error(const char * message, bool large=false) : std::invalid_argument(message), too_large(large) {}
};
inline bool supports_images(bool decision, bool vision) { return decision && vision; }
inline void require(bool ok, const char * message, bool large=false) {
    if (!ok) throw image_error(message, large);
}
inline int digit(unsigned char c) {
    if (c >= 'A' && c <= 'Z') return c-'A';
    if (c >= 'a' && c <= 'z') return c-'a'+26;
    if (c >= '0' && c <= '9') return c-'0'+52;
    return c=='+' ? 62 : c=='/' ? 63 : -1;
}
inline uint32_t be32(const unsigned char * p) {
    return uint32_t(p[0])<<24 | uint32_t(p[1])<<16 | uint32_t(p[2])<<8 | p[3];
}
inline uint32_t png_crc(const unsigned char * data, size_t size) {
    uint32_t crc = 0xffffffffu;
    for (size_t i = 0; i < size; ++i) {
        crc ^= data[i];
        for (int bit = 0; bit < 8; ++bit) crc = (crc >> 1) ^ (0xedb88320u & (0u - (crc & 1)));
    }
    return crc ^ 0xffffffffu;
}
inline void validate_url(const std::string & url, size_t & decoded, size_t & pixels) {
    const auto comma = url.find(',');
    const auto header = url.substr(0, comma);
    bool png = header == "data:image/png;base64";
    require(comma != std::string::npos && (png || header == "data:image/jpeg;base64"), "images must be PNG/JPEG base64 data URLs");
    size_t n = url.size()-comma-1;
    require(n > 0 && n%4 == 0, "invalid base64 length");
    const char * data = url.data()+comma+1;
    size_t pad = (data[n-1]=='=') + (data[n-2]=='=');
    size_t size = n/4*3-pad;
    require(size <= decoded_bytes-decoded, "decoded image aggregate exceeds limit", true);
    std::vector<unsigned char> raw;
    raw.reserve(size);
    for (size_t i=0; i<n; i+=4) {
        int a=digit(data[i]), b=digit(data[i+1]);
        int c=data[i+2]=='=' ? 0 : digit(data[i+2]);
        int d=data[i+3]=='=' ? 0 : digit(data[i+3]);
        require(a>=0 && b>=0 && c>=0 && d>=0, "invalid base64 character");
        require((data[i+2]!='=' && data[i+3]!='=') || i+4==n, "invalid base64 padding");
        require(data[i+2]!='=' || (data[i+3]=='=' && (b&15)==0), "invalid base64 padding bits");
        require(data[i+3]!='=' || data[i+2]=='=' || (c&3)==0, "invalid base64 padding bits");
        raw.push_back((a<<2)|(b>>4));
        if (data[i+2]!='=') raw.push_back((b<<4)|(c>>2));
        if (data[i+3]!='=') raw.push_back((c<<6)|d);
    }
    decoded += raw.size();
    require(png ? raw.size()>=24 && std::memcmp(raw.data(),"\x89PNG\r\n\x1a\n",8)==0
                : raw.size()>=3 && raw[0]==255 && raw[1]==216 && raw[2]==255, "image MIME mismatch");
    int w=0,h=0,c=0;
    require(stbi_info_from_memory(raw.data(),raw.size(),&w,&h,&c)!=0 && w>0 && h>0, "invalid image header");
    require(size_t(w)<=max_dimension && size_t(h)<=max_dimension, "image dimensions exceed limit", true);
    size_t count=size_t(w)*size_t(h);
    require(count<=max_pixels-pixels, "image pixel aggregate exceeds limit", true);
    pixels += count;
    if (png) {
        // stb's PNG inflater grows independently of IHDR. Validate IDAT with a
        // fixed output buffer first, preventing small-header decompression bombs.
        // 16-bit RGBA plus Adam7 row filters fit this conservative pixel bound.
        std::vector<unsigned char> idat;
        size_t pos=8;
        bool end=false;
        while (pos+12<=raw.size()) {
            size_t len=be32(raw.data()+pos);
            require(len<=raw.size()-pos-12, "truncated PNG chunk");
            require(png_crc(raw.data()+pos+4,len+4)==be32(raw.data()+pos+8+len), "invalid PNG checksum");
            if (std::memcmp(raw.data()+pos+4,"IDAT",4)==0)
                idat.insert(idat.end(),raw.begin()+pos+8,raw.begin()+pos+8+len);
            if (std::memcmp(raw.data()+pos+4,"IEND",4)==0) { end=true; break; }
            pos+=len+12;
        }
        require(end && !idat.empty(), "incomplete PNG");
        std::vector<char> inflated(9*count+8*size_t(h)+1024);
        require(stbi_zlib_decode_buffer(inflated.data(),inflated.size(),reinterpret_cast<const char *>(idat.data()),idat.size())>=0,
                "invalid or oversized PNG decompression");
    }
    auto * image=stbi_load_from_memory(raw.data(),raw.size(),&w,&h,&c,3);
    require(image!=nullptr, "invalid image pixels");
    stbi_image_free(image);
}
// Normalize only actual chat content parts, as in the canonical Go collector.
// Upstream parse_state understands image_url but not Anthropic source objects.
inline size_t validate(json & body, size_t wire_size) {
    require(wire_size<=body_bytes, "decision request exceeds limit", true);
    std::vector<const std::string *> urls;
    auto add=[&](const json & value) {
        require(value.is_string(), "image URL must be a string");
        require(urls.size()<max_images, "too many decision images", true);
        urls.push_back(&value.get_ref<const std::string &>());
    };
    if (body.contains("images") && !body["images"].is_null()) {
        require(body["images"].is_array(), "images must be an array");
        for (const auto & url : body["images"]) add(url);
    }
    auto state=body.find("state");
    if (state!=body.end()) {
        json * messages=&*state;
        if (state->is_object() && state->contains("messages")) messages=&(*state)["messages"];
        if (messages->is_array()) for (auto & msg : *messages) {
            if (!msg.is_object() || !msg.contains("content") || !msg["content"].is_array()) continue;
            for (auto & part : msg["content"]) {
                if (!part.is_object() || !part.contains("type")) continue;
                if (part["type"]=="image") {
                    require(part.contains("source") && part["source"].is_object(), "invalid image source");
                    auto & s=part["source"];
                    require(s.value("type",std::string())=="base64" && s.contains("media_type") && s["media_type"].is_string() && s.contains("data") && s["data"].is_string(), "invalid image source");
                    require(s["data"].get_ref<const std::string &>().size()<=encoded_bytes && s["media_type"].get_ref<const std::string &>().size()<=32, "image source exceeds limit",true);
                    std::string url="data:"+s["media_type"].get<std::string>()+";base64,"+s["data"].get<std::string>();
                    part=json{{"type","image_url"},{"image_url",{{"url",url}}}};
                }
                if (part["type"]=="image_url") {
                    require(part.contains("image_url"), "missing image URL");
                    auto & u=part["image_url"];
                    if (u.is_object()) { require(u.contains("url"), "missing image URL"); add(u["url"]); }
                    else add(u);
                }
            }
        }
    }
    require(!urls.empty() || wire_size<=text_bytes, "text decision request exceeds limit",true);
    size_t encoded=0,decoded=0,pixels=0;
    for (const auto * u : urls) { require(u->size()<=encoded_bytes-encoded,"encoded image aggregate exceeds limit",true); encoded+=u->size(); }
    for (const auto * u : urls) validate_url(*u,decoded,pixels);
    return urls.size();
}
}
