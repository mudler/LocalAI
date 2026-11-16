// SPDX-License-Identifier: MIT
#include "trellis2.h"
#include "vendor/mesh_export.h"
#include <nlohmann/json.hpp>
#include <cmath>
#include <cstdio>
#include <memory>
#include <stdexcept>
#include <string>

struct mesh_result { std::vector<uint8_t> glb; std::string stats; };

extern "C" TRELLIS2_API uint32_t mr_abi_version() { return 1; }
extern "C" TRELLIS2_API void mr_result_free(mesh_result *result) { delete result; }
extern "C" TRELLIS2_API uint64_t mr_result_size(const mesh_result *result) { return result ? result->glb.size() : 0; }
extern "C" TRELLIS2_API const uint8_t *mr_result_data(const mesh_result *result) { return result ? result->glb.data() : nullptr; }
extern "C" TRELLIS2_API const char *mr_result_stats(const mesh_result *result) { return result ? result->stats.c_str() : ""; }

extern "C" TRELLIS2_API mesh_result *mr_process(const float *vertices, int32_t nv,
    const int32_t *triangles, int32_t nt, const float *pbr, const char *parameters,
    char *error, uint32_t capacity) {
    try {
        if (!vertices || !triangles || nv < 3 || nv > 10000000 || nt < 1 || nt > 20000000)
            throw std::runtime_error("invalid or oversized mesh");
        for (int64_t i = 0; i < int64_t(nv)*3; ++i)
            if (!std::isfinite(vertices[i])) throw std::runtime_error("non-finite vertex");
        for (int64_t i = 0; i < int64_t(nt)*3; ++i)
            if (triangles[i] < 0 || triangles[i] >= nv) throw std::runtime_error("invalid triangle index");
        if (pbr) for (int64_t i = 0; i < int64_t(nv)*6; ++i)
            if (!std::isfinite(pbr[i])) throw std::runtime_error("non-finite material value");
        const auto params = nlohmann::json::parse(parameters);
        const auto mode = params.value("mode", "wrap_quad");
        if (mode != "wrap" && mode != "quad" && mode != "wrap_quad") throw std::runtime_error("invalid remesh mode");
        const auto alpha = std::stof(params.value("alpha_ratio", "0.005"));
        const auto target = std::stoi(params.value("target_quads", "20000"));
        const auto adaptivity = std::stof(params.value("adaptivity", "1"));
        const auto texture = std::stoi(params.value("texture_size", "2048"));
        if (!std::isfinite(alpha) || alpha < 0.0035f || alpha > 0.025f || target < 100 || target > 100000 ||
            !std::isfinite(adaptivity) || adaptivity < 0 || adaptivity > 1 || texture < 256 || texture > 4096)
            throw std::runtime_error("remesh parameter out of range");
        t2glb::MeshExportOptions options;
        options.components = t2glb::ComponentFilter::KeepAll;
        options.texture_size = texture;
        t2glb::PreparedMesh prepared;
        std::string reason;
        std::fprintf(stderr, "[meshremesh] preparing %d vertices, %d triangles\n", nv, nt);
        if (!t2glb::prepare_mesh(vertices, nv, triangles, nt, pbr, options, prepared, reason)) throw std::runtime_error(reason);
        const auto source = prepared;
        if (mode != "quad") {
            std::fprintf(stderr, "[meshremesh] wrapping surface\n");
            t2glb::PreparedMesh wrapped;
            if (!t2glb::prepare_print_mesh(prepared.verts.data(), int(prepared.verts.size()/3), prepared.tris.data(),
                int(prepared.tris.size()/3), nullptr, options, alpha, alpha/30, wrapped, reason)) throw std::runtime_error(reason);
            prepared = std::move(wrapped);
        }
        t2glb::QuadMeshStats stats;
        if (mode != "wrap") {
            std::fprintf(stderr, "[meshremesh] extracting quad-dominant topology\n");
            t2glb::PreparedMesh quad;
            if (!t2glb::prepare_quad_mesh(prepared.verts.data(), int(prepared.verts.size()/3), prepared.tris.data(),
                int(prepared.tris.size()/3), nullptr, options, target, adaptivity, quad, &stats, reason)) throw std::runtime_error(reason);
            prepared = std::move(quad);
        }
        auto result = std::make_unique<mesh_result>();
        std::fprintf(stderr, "[meshremesh] projecting materials and exporting GLB\n");
        if (!source.pbr.empty()) {
            if (!t2glb::mesh_to_projected_glb(prepared.verts.data(), int(prepared.verts.size()/3), prepared.tris.data(),
                int(prepared.tris.size()/3), source.verts.data(), int(source.verts.size()/3), source.tris.data(),
                int(source.tris.size()/3), source.pbr.data(), options, result->glb, reason)) throw std::runtime_error(reason);
        } else if (!t2glb::mesh_to_glb(prepared.verts.data(), int(prepared.verts.size()/3), prepared.tris.data(),
            int(prepared.tris.size()/3), nullptr, options, result->glb, reason)) throw std::runtime_error(reason);
        result->stats = nlohmann::json{{"input_triangles", nt}, {"output_triangles", prepared.tris.size()/3},
            {"quads_before_triangulation", stats.quads}, {"boundary_edges", stats.boundary_edges},
            {"area_retained", stats.area_retained}, {"mode", mode}, {"quad_output_is_triangulated", mode != "wrap"}}.dump();
        return result.release();
    } catch (const std::exception &exception) {
        if (capacity) std::snprintf(error, capacity, "%s", exception.what());
        return nullptr;
    }
}
