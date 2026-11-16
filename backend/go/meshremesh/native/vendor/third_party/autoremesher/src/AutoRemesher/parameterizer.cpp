/*
 *  Copyright (c) 2026 Jeremy HU <jeremy-at-dust3d dot org>. All rights reserved.
 *
 *  Permission is hereby granted, free of charge, to any person obtaining a copy
 *  of this software and associated documentation files (the "Software"), to deal
 *  in the Software without restriction, including without limitation the rights
 *  to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 *  copies of the Software, and to permit persons to whom the Software is
 *  furnished to do so, subject to the following conditions:
 *
 *  The above copyright notice and this permission notice shall be included in all
 *  copies or substantial portions of the Software.
 *
 *  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 *  IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 *  FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 *  AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 *  LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 *  OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 *  SOFTWARE.
 */
#include "../../t2_ar_log.h"
#include <AutoRemesher/FrameField>
#include <AutoRemesher/Parameterizer>
#include <AutoRemesher/QuadParameterizer>
#include <AutoRemesher/SingularitySimplifier>
#include <AutoRemesher/SurfaceMesh>
#include <algorithm>
#include <cmath>
#include <iostream>
#include <limits>
#include <map>
#include <set>
#include <tbb/blocked_range.h>
#include <tbb/combinable.h>
#include <tbb/parallel_for.h>

namespace AutoRemesher {

namespace {

    inline double quadraticForm(const double* T, const Vector3& t)
    {
        return T[0] * t.x() * t.x() + T[2] * t.y() * t.y() + T[5] * t.z() * t.z()
            + 2.0 * (T[1] * t.x() * t.y() + T[3] * t.x() * t.z() + T[4] * t.y() * t.z());
    }

    void computeFaceAnisotropyField(const SurfaceMesh& mesh,
        const std::vector<Vector3>& field, double anisotropy, double maxAspectRatio,
        std::vector<double>* scalingU, std::vector<double>* scalingV)
    {
        scalingU->assign(mesh.faceCount(), 1.0);
        scalingV->assign(mesh.faceCount(), 1.0);
        if (anisotropy <= 0.0 || maxAspectRatio <= 1.0 || field.size() != mesh.faceCount())
            return;
        std::vector<double> tensor(mesh.vertexCount() * 6, 0.0);
        std::vector<std::vector<size_t>> neighbors(mesh.vertexCount());
        for (size_t c = 0; c < mesh.cornerCount(); ++c) {
            const size_t mate = mesh.oppositeCorner(c);
            if (mate == SurfaceMesh::npos || mate < c)
                continue;
            const Vector3 edge = mesh.edgeVector(c);
            const double length = edge.length();
            if (length <= 0.0)
                continue;
            const Vector3 d = edge / length;
            const double weight = length * mesh.normalAngle(c);
            const double contribution[6] = { weight * d.x() * d.x(), weight * d.x() * d.y(), weight * d.y() * d.y(), weight * d.x() * d.z(), weight * d.y() * d.z(), weight * d.z() * d.z() };
            const size_t a = mesh.cornerVertex(c), b = mesh.cornerVertex(mesh.nextCorner(c));
            for (size_t i = 0; i < 6; ++i) {
                tensor[6 * a + i] += contribution[i];
                tensor[6 * b + i] += contribution[i];
            }
        }
        for (size_t c = 0; c < mesh.cornerCount(); ++c) {
            const size_t a = mesh.cornerVertex(c), b = mesh.cornerVertex(mesh.nextCorner(c));
            neighbors[a].push_back(b);
            neighbors[b].push_back(a);
        }
        std::vector<double> smoothed(tensor.size());
        for (size_t pass = 0; pass < 12; ++pass) {
            for (size_t v = 0; v < mesh.vertexCount(); ++v) {
                if (neighbors[v].empty()) {
                    for (size_t i = 0; i < 6; ++i)
                        smoothed[6 * v + i] = tensor[6 * v + i];
                    continue;
                }
                for (size_t i = 0; i < 6; ++i) {
                    double average = 0;
                    for (size_t n : neighbors[v])
                        average += tensor[6 * n + i];
                    smoothed[6 * v + i] = .5 * tensor[6 * v + i] + .5 * average / neighbors[v].size();
                }
            }
            tensor.swap(smoothed);
        }
        std::vector<double> alongU(mesh.faceCount()), alongV(mesh.faceCount());
        double total = 0;
        for (size_t f = 0; f < mesh.faceCount(); ++f) {
            double T[6] = {};
            for (size_t l = 0; l < 3; ++l) {
                const size_t v = mesh.cornerVertex(3 * f + l);
                for (size_t i = 0; i < 6; ++i)
                    T[i] += tensor[6 * v + i];
            }
            const Vector3 n = mesh.faceNormal(f), b = field[f].normalized(), bt = Vector3::crossProduct(n, b);
            alongU[f] = std::fabs(quadraticForm(T, bt));
            alongV[f] = std::fabs(quadraticForm(T, b));
            total += alongU[f] + alongV[f];
        }
        if (total <= 0)
            return;
        const double floor = .001 * total / (2.0 * mesh.faceCount()), maxRho = std::sqrt(maxAspectRatio);
        for (size_t f = 0; f < mesh.faceCount(); ++f) {
            double rho = std::pow((alongV[f] + floor) / (alongU[f] + floor), .25);
            rho = std::pow(rho, anisotropy);
            rho = std::max(1.0 / maxRho, std::min(maxRho, rho));
            (*scalingU)[f] = rho;
            (*scalingV)[f] = 1.0 / rho;
        }
    }

}

std::vector<double> Parameterizer::computeFaceScalingField(const std::vector<Vector3>& vertices,
    const std::vector<std::vector<size_t>>& triangles,
    const std::vector<Vector3>& vertexNormals,
    const std::map<size_t, std::vector<size_t>>& faceAroundVertexMap) const
{
    std::vector<double> faceScaling(triangles.size(), 1.0);
    if (m_adaptivity <= 0.0 || vertices.empty())
        return faceScaling;

    std::vector<double> vertexCurvature(vertices.size(), 0.0);
    tbb::parallel_for(tbb::blocked_range<size_t>(0, vertices.size()),
        [&](const tbb::blocked_range<size_t>& range) {
            for (size_t v = range.begin(); v != range.end(); ++v) {
                auto findFaces = faceAroundVertexMap.find(v);
                if (findFaces == faceAroundVertexMap.end())
                    continue;
                const auto& normalV = vertexNormals[v];
                double maxCurvature = 0.0;
                for (const auto& faceIndex : findFaces->second) {
                    for (const auto& u : triangles[faceIndex]) {
                        if (u == v)
                            continue;
                        double distance = (vertices[u] - vertices[v]).length();
                        if (distance <= 0.0)
                            continue;
                        double cosAngle = Vector3::dotProduct(normalV, vertexNormals[u]);
                        if (cosAngle > 1.0)
                            cosAngle = 1.0;
                        else if (cosAngle < -1.0)
                            cosAngle = -1.0;
                        double curvature = std::acos(cosAngle) / distance;
                        if (curvature > maxCurvature)
                            maxCurvature = curvature;
                    }
                }
                vertexCurvature[v] = maxCurvature;
            }
        });

    double sumCurvature = 0.0;
    for (const auto& it : vertexCurvature)
        sumCurvature += it;
    double averageCurvature = sumCurvature / vertexCurvature.size();
    if (averageCurvature <= 0.0)
        return faceScaling;

    const double minRatio = 0.3;
    const double maxRatio = 3.0;
    tbb::parallel_for(tbb::blocked_range<size_t>(0, triangles.size()),
        [&](const tbb::blocked_range<size_t>& range) {
            for (size_t i = range.begin(); i != range.end(); ++i) {
                const auto& triangle = triangles[i];
                double faceCurvature = 0.0;
                for (const auto& v : triangle)
                    faceCurvature += vertexCurvature[v];
                faceCurvature /= triangle.size();
                double normalized = faceCurvature / averageCurvature;
                if (normalized < 1e-3)
                    normalized = 1e-3;
                double multiplier = std::pow(normalized, -m_adaptivity);
                if (multiplier < minRatio)
                    multiplier = minRatio;
                else if (multiplier > maxRatio)
                    multiplier = maxRatio;
                faceScaling[i] = multiplier;
            }
        });

    return faceScaling;
}

bool Parameterizer::parameterize()
{
#if AUTO_REMESHER_DEV
    {
        FILE* fp = fopen("debug-input-for-parameterization.obj", "wb");
        for (size_t i = 0; i < m_vertices->size(); ++i) {
            const auto& vertex = (*m_vertices)[i];
            fprintf(fp, "v %f %f %f\n", vertex[0], vertex[1], vertex[2]);
        }
        for (size_t i = 0; i < m_triangles->size(); ++i) {
            const auto& indices = (*m_triangles)[i];
            fprintf(fp, "f %zu %zu %zu\n", indices[0] + 1, indices[1] + 1, indices[2] + 1);
        }
        fclose(fp);
    }
#endif

    std::vector<Vector3> vertexNormals(m_vertices->size());
    {
        std::vector<Vector3> faceNormals(m_triangles->size());
        tbb::parallel_for(tbb::blocked_range<size_t>(0, m_triangles->size()),
            [&](const tbb::blocked_range<size_t>& range) {
                for (size_t i = range.begin(); i != range.end(); ++i) {
                    const auto& it = (*m_triangles)[i];
                    faceNormals[i] = Vector3::normal(
                        (*m_vertices)[it[0]], (*m_vertices)[it[1]], (*m_vertices)[it[2]]);
                }
            });
        for (size_t i = 0; i < m_triangles->size(); ++i) {
            const auto& it = (*m_triangles)[i];
            vertexNormals[it[0]] += faceNormals[i];
            vertexNormals[it[1]] += faceNormals[i];
            vertexNormals[it[2]] += faceNormals[i];
        }
        tbb::parallel_for(tbb::blocked_range<size_t>(0, vertexNormals.size()),
            [&](const tbb::blocked_range<size_t>& range) {
                for (size_t i = range.begin(); i != range.end(); ++i)
                    vertexNormals[i].normalize();
            });
    }

    std::map<size_t, std::vector<size_t>> faceAroundVertexMap;
    for (size_t i = 0; i < m_triangles->size(); ++i) {
        const auto& it = (*m_triangles)[i];
        faceAroundVertexMap[it[0]].push_back(i);
        faceAroundVertexMap[it[1]].push_back(i);
        faceAroundVertexMap[it[2]].push_back(i);
    }

    std::vector<double> faceScalingField = computeFaceScalingField(*m_vertices,
        *m_triangles, vertexNormals, faceAroundVertexMap);

    // The parameterization pipeline uses the triangle/corner mesh;
    // no attribute-backed interchange mesh is constructed.
    SurfaceMesh topology(*m_vertices, *m_triangles);
    if (topology.faceCount() != m_triangles->size()) {
        T2_AR_LOG << "Topology rejected a non-triangle face" << std::endl;
        return false;
    }

    // Topology, field, and quad cover form the complete active path.
    std::vector<Vector3> field;
    if (nullptr != m_triangleFieldVectors) {
        field = *m_triangleFieldVectors;
    } else if (!FrameField::create(topology, m_sharpEdgeDegrees,
                   &field)) {
        T2_AR_LOG << "Frame field solve failed" << std::endl;
        return false;
    }
    if (field.size() != topology.faceCount()) {
        T2_AR_LOG << "Frame field has the wrong face count" << std::endl;
        return false;
    }

    SingularitySimplifier simplifier(topology, &field);
    if (m_singularitySimplification) {
        simplifier.setSharpEdgeDegrees(m_sharpEdgeDegrees);
        simplifier.setMaximumPairDistance(m_maximumSingularityPairDistance);
        simplifier.simplify();
    }

    std::vector<double> faceScalingU(m_triangles->size(), 1.0);
    std::vector<double> faceScalingV(m_triangles->size(), 1.0);
    if (m_anisotropy > 0.0) {
        computeFaceAnisotropyField(topology, field,
            m_anisotropy, m_maxAspectRatio, &faceScalingU, &faceScalingV);
    }
    QuadParameterizer::Result cover;
    if (!QuadParameterizer::parameterize(*m_vertices, *m_triangles,
            &field, m_scaling, m_sharpEdgeDegrees, &cover,
            &faceScalingField, &faceScalingU, &faceScalingV)) {
        T2_AR_LOG << "Quad cover solve failed" << std::endl;
        return false;
    }
    m_originalTriangleUvs = cover.triangleUvs;
    delete m_triangleUvs;
    m_triangleUvs = new std::vector<std::vector<Vector2>>(cover.triangleUvs);
    m_singularVertexPositions.clear();
    m_singularVertexIndices.clear();
    for (const size_t v : cover.singularVertices) {
        if (v >= m_vertices->size())
            continue;
        m_singularVertexPositions.push_back((*m_vertices)[v]);
        m_singularVertexIndices.push_back(v);
    }
    if (simplifier.cancelledPairCount() > 0) {
        T2_AR_LOG << "Simplified cross field singularities: "
                  << simplifier.singularityCountBefore() << " -> "
                  << simplifier.singularityCountAfter() << " ("
                  << simplifier.cancelledPairCount() << " pair(s) cancelled)"
                  << std::endl;
    }
    return true;
}

}
