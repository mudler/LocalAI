// SPDX-License-Identifier: MIT
#define STB_IMAGE_IMPLEMENTATION
#include "stb/stb_image.h"
#undef STB_IMAGE_IMPLEMENTATION
#include "decision_images.h"
#include <cassert>
#include <iostream>
#include <fstream>
using namespace localai_decision;
template<class F> void rejects(F f, bool large=false) {
    try { f(); assert(false); } catch (const image_error & e) { assert(e.too_large == large); }
}
int main(int argc, char ** argv) {
    assert(argc==2);
    std::ifstream input(argv[1]);
    json fixtures; input >> fixtures;
    assert(!supports_images(true, false));
    assert(!supports_images(false, true));
    assert(supports_images(true, true));
    auto check=[](json j) { return validate(j, j.dump().size()); };
    check(json{{"state", "text"}});
    for (auto images : {json(), json::array()}) {
        rejects([&]{check(json{{"state",std::string(text_bytes, 'x')},{"images",images}});},true);
    }
    rejects([&]{check(json{{"state",std::string(body_bytes, 'x')}});},true);
    rejects([&]{check(json{{"state",std::string(text_bytes, 'x')}});},true);
    rejects([&]{check(json{{"images", {"https://invalid/image.png"}}});});
    rejects([&]{check(json{{"images", {"data:image/png;base64,AAAA\n"}}});});
    rejects([&]{check(json{{"images", {"data:image/png;base64,AB=="}}});});
    rejects([&]{check(json{{"images", std::vector<std::string>(9,"x")}});},true);
    rejects([&]{check(json{{"images", {std::string(encoded_bytes+1,'x')}}});},true);
    rejects([&]{check(json{{"images", {"data:image/png;base64,"+std::string(12*1024*1024-24,'A')}}});},true);
    for (auto key : {"dimension", "pixels", "jpeg_dimension", "jpeg_pixels"}) rejects([&]{check(json{{"images",{fixtures[key]}}});},true);
    for (auto key : {"bomb", "truncated", "bad_crc", "bad_adler", "jpeg_missing_eoi", "jpeg_truncated_scan", "jpeg_appended_eoi", "jpeg_embedded_missing_eoi"}) rejects([&]{check(json{{"images",{fixtures[key]}}});});
    // Aggregate pixels reject even when each individual image fits.
    rejects([&]{check(json{{"images",{fixtures["aggregate"],fixtures["aggregate"]}}});},true);
    for (auto key : {"red", "blue", "jpeg", "jpeg_progressive", "jpeg_embedded_marker"}) assert(check(json{{"images",{fixtures[key]}}})==1);
    // Valid one-pixel PNG, and MIME mismatch.
    std::string png="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=";
    assert(check(json{{"images",{png}}})==1);
    auto jpeg=png; jpeg.replace(5,9,"image/jpeg");
    rejects([&]{check(json{{"images",{jpeg}}});});
    json chat={{"state",{{"messages",json::array({{{"content",json::array({{{"type","image"},{"source",{{"type","base64"},{"media_type","image/png"},{"data",png.substr(22)}}}}})}}})}}}};
    assert(validate(chat,chat.dump().size())==1);
    assert(chat["state"]["messages"][0]["content"][0]["type"]=="image_url");
    // Domain state is not chat content.
    assert(check(json{{"state",{{"image_url","https://invalid"}}}})==0);
    std::cout << "decision image safety PASS\n";
}
