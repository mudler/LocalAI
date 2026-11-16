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
#include <AutoRemesher/MixedIntegerLeastSquares>
#include <AutoRemesher/QuadParameterizer>
#include <AutoRemesher/SurfaceMesh>

#include <algorithm>
#include <cmath>
#include <cstdlib>
#include <iostream>
#include <queue>
#include <tbb/blocked_range.h>
#include <tbb/parallel_for.h>

namespace AutoRemesher {
namespace {
    Vector3 unit(const Vector3& v, const Vector3& fallback)
    {
        return v.length() < 1e-12 ? fallback.normalized() : v.normalized();
    }

    int edgeQuarterTurn(const SurfaceMesh& mesh, size_t corner,
        const std::vector<Vector3>& field, const std::vector<Vector3>& normals)
    {
        const size_t opposite = mesh.oppositeCorner(corner);
        if (opposite == SurfaceMesh::npos)
            return 0;
        const size_t f = mesh.cornerFace(corner), g = mesh.cornerFace(opposite);
        if (f > g) {
            const int r = edgeQuarterTurn(mesh, opposite, field, normals);
            return (4 - r) % 4;
        }
        size_t v0 = mesh.cornerVertex(corner), v1 = mesh.cornerVertex(mesh.nextCorner(corner));
        if (v1 < v0)
            std::swap(v0, v1);
        const Vector3 e = unit(mesh.position(v1) - mesh.position(v0), Vector3(1, 0, 0));
        const Vector3 y0 = unit(Vector3::crossProduct(normals[f], e), Vector3(0, 1, 0));
        const Vector3 yg = unit(Vector3::crossProduct(normals[g], e), Vector3(0, 1, 0));
        const double a0 = std::atan2(Vector3::dotProduct(field[f], y0), Vector3::dotProduct(field[f], e));
        int best = 0;
        double bestError = 1e100;
        Vector3 candidate = field[g];
        for (int r = 0; r < 4; ++r) {
            const double ag = std::atan2(Vector3::dotProduct(candidate, yg), Vector3::dotProduct(candidate, e));
            double d = std::fabs(a0 - ag);
            while (d > M_PI)
                d = std::fabs(d - 2.0 * M_PI);
            if (d < bestError) {
                bestError = d;
                best = r;
            }
            candidate = Vector3::crossProduct(normals[g], candidate);
        }
        return best;
    }

    enum EdgeConstraint { ConstraintNone = 0,
        ConstraintU = 1,
        ConstraintV = 2 };
    int edgeConstraint(const SurfaceMesh& mesh, size_t c, const std::vector<Vector3>& field,
        const std::vector<Vector3>& normals, double hardEdgeDegrees)
    {
        if (mesh.oppositeCorner(c) != SurfaceMesh::npos && std::fabs(mesh.normalAngle(c)) * 180.0 / M_PI < hardEdgeDegrees)
            return ConstraintNone;
        const size_t f = mesh.cornerFace(c);
        const Vector3 edge = unit(mesh.edgeVector(c), Vector3(1, 0, 0));
        const Vector3 b = unit(field[f], edge);
        const Vector3 br = unit(Vector3::crossProduct(normals[f], b), Vector3(0, 1, 0));
        const bool alongB = std::acos(std::max(-1.0, std::min(1.0, std::fabs(Vector3::dotProduct(edge, b))))) < 10.0 * M_PI / 180.0;
        const bool alongBr = std::acos(std::max(-1.0, std::min(1.0, std::fabs(Vector3::dotProduct(edge, br))))) < 10.0 * M_PI / 180.0;
        if (alongB == alongBr)
            return ConstraintNone;
        return alongB ? ConstraintV : ConstraintU;
    }

    struct CoverContext {
        const SurfaceMesh& mesh;
        const std::vector<Vector3>& field;
        const std::vector<Vector3>& normals;
        const std::vector<int>& rotation;
        const std::vector<char>& seam;
        const std::vector<signed char>& cornerConstraints;
        const std::vector<double>& scalingU;
        const std::vector<double>& scalingV;
        const std::vector<double>* faceScaling;
        double scale;
    };

    void initializeFieldAndNormals(const SurfaceMesh& mesh, const std::vector<Vector3>* guidance,
        std::vector<Vector3>* normals, std::vector<Vector3>* field)
    {
        const bool hasGuidance = guidance && guidance->size() == mesh.faceCount();
        normals->assign(mesh.faceCount(), Vector3());
        field->resize(mesh.faceCount());
        tbb::parallel_for(tbb::blocked_range<size_t>(0, mesh.faceCount()), [&](const tbb::blocked_range<size_t>& range) {
            for (size_t faceIndex = range.begin(); faceIndex != range.end(); ++faceIndex) {
                (*normals)[faceIndex] = unit(mesh.faceNormal(faceIndex), Vector3(0, 0, 1));
                Vector3 tangentAxis(1, 0, 0);
                if (std::fabs(Vector3::dotProduct(tangentAxis, (*normals)[faceIndex])) > .8)
                    tangentAxis = Vector3(0, 1, 0);
                Vector3 fieldDirection = hasGuidance ? (*guidance)[faceIndex] : tangentAxis;
                if (!hasGuidance)
                    fieldDirection = fieldDirection - (*normals)[faceIndex] * Vector3::dotProduct(fieldDirection, (*normals)[faceIndex]);
                (*field)[faceIndex] = unit(fieldDirection, mesh.edgeVector(3 * faceIndex));
            }
        });
    }

    void smoothCrossField(const SurfaceMesh& mesh, const std::vector<Vector3>& normals,
        double hardEdgeDegrees, std::vector<Vector3>* field)
    {
        std::vector<double> alpha(2 * mesh.faceCount(), 0.0);
        std::vector<char> locked(mesh.faceCount(), 0);
        tbb::parallel_for(tbb::blocked_range<size_t>(0, mesh.faceCount()), [&](const tbb::blocked_range<size_t>& range) {
            for (size_t faceIndex = range.begin(); faceIndex != range.end(); ++faceIndex) {
                alpha[2 * faceIndex] = 1.0;
                for (size_t localCorner = 0; localCorner < 3; ++localCorner) {
                    const size_t cornerIndex = 3 * faceIndex + localCorner, oppositeCornerIndex = mesh.oppositeCorner(cornerIndex);
                    if (oppositeCornerIndex != SurfaceMesh::npos && std::fabs(mesh.normalAngle(cornerIndex)) * 180.0 / M_PI < hardEdgeDegrees)
                        continue;
                    const Vector3 edge = unit(mesh.edgeVector(cornerIndex), Vector3(1, 0, 0));
                    const Vector3 fieldDirection = (*field)[faceIndex];
                    const Vector3 perpendicular = unit(Vector3::crossProduct(normals[faceIndex], fieldDirection), Vector3(0, 1, 0));
                    const double fieldAngle = std::atan2(Vector3::dotProduct(edge, perpendicular), Vector3::dotProduct(edge, fieldDirection));
                    alpha[2 * faceIndex] = std::cos(4.0 * fieldAngle);
                    alpha[2 * faceIndex + 1] = std::sin(4.0 * fieldAngle);
                    locked[faceIndex] = 1;
                }
            }
        });
        for (int iteration = 0; iteration < 40; ++iteration) {
            std::vector<double> next = alpha;
            tbb::parallel_for(tbb::blocked_range<size_t>(0, mesh.faceCount()), [&](const tbb::blocked_range<size_t>& range) {
                for (size_t f = range.begin(); f != range.end(); ++f)
                    if (!locked[f]) {
                        double x = alpha[2 * f], y = alpha[2 * f + 1];
                        const Vector3 bf = (*field)[f];
                        const Vector3 btf = unit(Vector3::crossProduct(normals[f], bf), Vector3(0, 1, 0));
                        for (size_t l = 0; l < 3; ++l) {
                            const size_t oc = mesh.oppositeCorner(3 * f + l);
                            if (oc == SurfaceMesh::npos)
                                continue;
                            const size_t g = mesh.cornerFace(oc);
                            Vector3 bg = (*field)[g];
                            bg = unit(bg - normals[f] * Vector3::dotProduct(bg, normals[f]), bf);
                            const double d = std::atan2(Vector3::dotProduct(bg, btf), Vector3::dotProduct(bg, bf));
                            const double cs = std::cos(4.0 * d), sn = std::sin(4.0 * d);
                            x += cs * alpha[2 * g] - sn * alpha[2 * g + 1];
                            y += sn * alpha[2 * g] + cs * alpha[2 * g + 1];
                        }
                        const double length = std::hypot(x, y);
                        if (length > 1e-12) {
                            next[2 * f] = x / length;
                            next[2 * f + 1] = y / length;
                        }
                    }
            });
            alpha.swap(next);
        }
        tbb::parallel_for(tbb::blocked_range<size_t>(0, mesh.faceCount()), [&](const tbb::blocked_range<size_t>& range) {
            for (size_t faceIndex = range.begin(); faceIndex != range.end(); ++faceIndex) {
                const double fieldAngle = .25 * std::atan2(alpha[2 * faceIndex + 1], alpha[2 * faceIndex]);
                const Vector3 fieldDirection = (*field)[faceIndex];
                const Vector3 perpendicular = unit(Vector3::crossProduct(normals[faceIndex], fieldDirection), Vector3(0, 1, 0));
                (*field)[faceIndex] = unit(fieldDirection * std::cos(fieldAngle) + perpendicular * std::sin(fieldAngle), fieldDirection);
            }
        });
    }

    void brushFieldAlongSpanningTree(const SurfaceMesh& mesh, const std::vector<Vector3>& normals,
        std::vector<Vector3>* field)
    {
        std::vector<char> seen(mesh.faceCount(), 0);
        if (mesh.faceCount() != 0) {
            std::queue<size_t> q;
            q.push(0);
            seen[0] = 1;
            while (!q.empty()) {
                size_t f = q.front();
                q.pop();
                for (size_t l = 0; l < 3; ++l) {
                    size_t c = 3 * f + l, oc = mesh.oppositeCorner(c);
                    if (oc == SurfaceMesh::npos)
                        continue;
                    size_t g = mesh.cornerFace(oc);
                    if (!seen[g]) {
                        int turns = edgeQuarterTurn(mesh, c, *field, normals);
                        Vector3 brushed = (*field)[g];
                        for (int k = 0; k < turns; ++k)
                            brushed = Vector3::crossProduct(normals[g], brushed);
                        (*field)[g] = unit(brushed, mesh.edgeVector(3 * g));
                        seen[g] = 1;
                        q.push(g);
                    }
                }
            }
        }
    }

    std::vector<int> computeCornerRotations(const SurfaceMesh& mesh,
        const std::vector<Vector3>& field, const std::vector<Vector3>& normals)
    {
        const size_t corners = mesh.cornerCount();
        std::vector<int> rotation(corners, 0);
        for (size_t c = 0; c < corners; ++c) {
            const size_t oc = mesh.oppositeCorner(c);
            if (oc == SurfaceMesh::npos)
                continue;
            rotation[c] = edgeQuarterTurn(mesh, c, field, normals);
        }
        return rotation;
    }

    std::vector<signed char> computeCornerConstraints(const SurfaceMesh& mesh,
        const std::vector<Vector3>& field, const std::vector<Vector3>& normals, double hardEdgeDegrees)
    {
        const size_t corners = mesh.cornerCount();
        std::vector<signed char> cornerConstraints(corners, ConstraintNone);
        tbb::parallel_for(tbb::blocked_range<size_t>(0, corners), [&](const tbb::blocked_range<size_t>& range) {
            for (size_t c = range.begin(); c != range.end(); ++c)
                cornerConstraints[c] = static_cast<signed char>(edgeConstraint(mesh, c, field, normals, hardEdgeDegrees));
        });
        return cornerConstraints;
    }

    void applyDirectionalSwaps(const SurfaceMesh& mesh, const std::vector<Vector3>& fieldBeforeBrush,
        const std::vector<Vector3>& field, const std::vector<Vector3>& normals,
        std::vector<double>* scalingU, std::vector<double>* scalingV)
    {
        size_t directionalSwaps = 0;
        for (size_t f = 0; f < mesh.faceCount(); ++f) {
            const Vector3 before = unit(fieldBeforeBrush[f], field[f]);
            const Vector3 after = unit(field[f], before);
            const Vector3 perpendicular = unit(Vector3::crossProduct(normals[f], after), before);
            if (std::fabs(Vector3::dotProduct(before, after)) < std::fabs(Vector3::dotProduct(before, perpendicular)))
                std::swap((*scalingU)[f], (*scalingV)[f]), ++directionalSwaps;
        }
    }

    std::vector<char> computeSeam(const SurfaceMesh& mesh, const std::vector<int>& rotation)
    {
        const size_t corners = mesh.cornerCount();
        std::vector<char> insideBall(corners, 0), ballSeen(mesh.faceCount(), 0);
        if (mesh.faceCount() != 0) {
            std::queue<size_t> q;
            q.push(0);
            ballSeen[0] = 1;
            while (!q.empty()) {
                const size_t f = q.front();
                q.pop();
                for (size_t l = 0; l < 3; ++l) {
                    const size_t c = 3 * f + l, oc = mesh.oppositeCorner(c);
                    if (oc == SurfaceMesh::npos || rotation[c] != 0)
                        continue;
                    const size_t g = mesh.cornerFace(oc);
                    if (!ballSeen[g]) {
                        ballSeen[g] = 1;
                        q.push(g);
                        insideBall[c] = insideBall[oc] = 1;
                    }
                }
            }
        }
        std::vector<char> seam(corners, 0);
        for (size_t c = 0; c < corners; ++c)
            seam[c] = (mesh.oppositeCorner(c) == SurfaceMesh::npos || !insideBall[c]);
        std::vector<size_t> borderDegree(mesh.vertexCount(), 0);
        for (size_t c = 0; c < corners; ++c)
            if (seam[c])
                ++borderDegree[mesh.cornerVertex(c)];
        bool changed = true;
        while (changed) {
            changed = false;
            for (size_t c = 0; c < corners; ++c) {
                const size_t oc = mesh.oppositeCorner(c);
                if (oc == SurfaceMesh::npos || !seam[c] || rotation[c] != 0)
                    continue;
                const size_t v0 = mesh.cornerVertex(c);
                if (borderDegree[v0] != 1)
                    continue;
                const size_t v1 = mesh.cornerVertex(mesh.nextCorner(c));
                seam[c] = seam[oc] = 0;
                insideBall[c] = insideBall[oc] = 1;
                if (borderDegree[v0] > 0)
                    --borderDegree[v0];
                if (borderDegree[v1] > 0)
                    --borderDegree[v1];
                changed = true;
            }
        }
        return seam;
    }

    void addRotationConstraints(MixedIntegerLeastSquares& s, size_t ax, size_t bx, int r, double sign)
    {
        r = (r % 4 + 4) % 4;
        if (r == 0) {
            s.addConstraint(ax, 1, bx, sign);
            s.addConstraint(ax + 1, 1, bx + 1, sign);
        } else if (r == 1) {
            s.addConstraint(ax, 1, bx + 1, sign);
            s.addConstraint(ax + 1, 1, bx, -sign);
        } else if (r == 2) {
            s.addConstraint(ax, 1, bx, -sign);
            s.addConstraint(ax + 1, 1, bx + 1, -sign);
        } else {
            s.addConstraint(ax, 1, bx + 1, -sign);
            s.addConstraint(ax + 1, 1, bx, sign);
        }
    }

    bool solveQuadCover(const CoverContext& ctx, std::vector<double>* values)
    {
        const SurfaceMesh& mesh = ctx.mesh;
        const std::vector<Vector3>& field = ctx.field;
        const std::vector<Vector3>& normals = ctx.normals;
        const std::vector<int>& rotation = ctx.rotation;
        const std::vector<char>& seam = ctx.seam;
        const std::vector<signed char>& cornerConstraints = ctx.cornerConstraints;
        const std::vector<double>& activeScalingU = ctx.scalingU;
        const std::vector<double>& activeScalingV = ctx.scalingV;
        const std::vector<double>* faceScaling = ctx.faceScaling;
        const double scale = ctx.scale;
        const size_t corners = mesh.cornerCount();
        const size_t uvVariables = 2 * corners;
        const size_t variables = 2 * uvVariables;
        MixedIntegerLeastSquares s(variables);
        for (size_t t = 0; t < 2 * corners; ++t)
            s.setVariablePeriod(uvVariables + t, 2);
        for (size_t f = 0; f < mesh.faceCount(); ++f) {
            const Vector3 u = field[f], v = unit(Vector3::crossProduct(normals[f], u), mesh.edgeVector(3 * f));
            const double faceScale = faceScaling && faceScaling->size() == mesh.faceCount()
                ? std::max(1e-12, (*faceScaling)[f])
                : 1.0;
            const double directionalU = std::max(1e-12, activeScalingU[f]);
            const double directionalV = std::max(1e-12, activeScalingV[f]);
            const double su = scale * faceScale * directionalU;
            const double sv = scale * faceScale * directionalV;
            for (size_t l = 0; l < 3; ++l) {
                size_t c = 3 * f + l, n = mesh.nextCorner(c);
                Vector3 e = mesh.edgeVector(c);
                const double weight = su * sv;
                s.addEnergy(2 * n, 1, 2 * c, -1, Vector3::dotProduct(u, e) / su, weight);
                s.addEnergy(2 * n + 1, 1, 2 * c + 1, -1, Vector3::dotProduct(v, e) / sv, weight);
            }
        }
        s.addConstraint(0, 1);
        s.addConstraint(1, 1);
        size_t hardCoordinateCount = 0;
        for (size_t c = 0; c < corners; ++c) {
            size_t oc = mesh.oppositeCorner(c);
            if (oc == SurfaceMesh::npos)
                continue;
            const size_t tc = uvVariables + 2 * c;
            const size_t toc = uvVariables + 2 * oc;
            int r = rotation[c];
            if (seam[c]) {
                addRotationConstraints(s, tc, toc, r, 1.0);
            } else {
                s.addConstraint(tc, 1);
                s.addConstraint(tc + 1, 1);
            }
        }
        for (size_t c = 0; c < corners; ++c) {
            const size_t oc = mesh.oppositeCorner(c);
            if (oc == SurfaceMesh::npos)
                continue;
            const size_t other = mesh.nextCorner(oc);
            const size_t tc = uvVariables + 2 * c;
            const int r = (rotation[c] % 4 + 4) % 4;
            if (r == 0) {
                s.addConstraint(2 * c, 1, 2 * other, -1, tc, -1);
                s.addConstraint(2 * c + 1, 1, 2 * other + 1, -1, tc + 1, -1);
            } else if (r == 1) {
                s.addConstraint(2 * c, 1, 2 * other + 1, -1, tc, -1);
                s.addConstraint(2 * c + 1, 1, 2 * other, 1, tc + 1, -1);
            } else if (r == 2) {
                s.addConstraint(2 * c, 1, 2 * other, 1, tc, -1);
                s.addConstraint(2 * c + 1, 1, 2 * other + 1, 1, tc + 1, -1);
            } else {
                s.addConstraint(2 * c, 1, 2 * other + 1, 1, tc, -1);
                s.addConstraint(2 * c + 1, 1, 2 * other, -1, tc + 1, -1);
            }
        }
        for (size_t vertex = 0; vertex < mesh.vertexCount(); ++vertex) {
            const auto& incident = mesh.cornersAroundVertex(vertex);
            if (incident.empty())
                continue;
            size_t start = incident.front(), c = start;
            int accumulated = 0;
            bool closed = true;
            std::vector<std::pair<size_t, int>> wheel;
            do {
                if (mesh.oppositeCorner(c) == SurfaceMesh::npos) {
                    closed = false;
                    break;
                }
                wheel.push_back({ c, accumulated });
                accumulated = (accumulated + rotation[c]) % 4;
                c = mesh.nextCorner(mesh.oppositeCorner(c));
            } while (c != start && wheel.size() <= incident.size() + 1);
            if (!closed || c != start || accumulated != 0)
                continue;
            for (int coord = 0; coord < 2; ++coord) {
                std::vector<std::pair<size_t, double>> row;
                for (const auto& item : wheel) {
                    const size_t t = uvVariables + 2 * item.first;
                    const int r = item.second;
                    if (coord == 0) {
                        if (r == 0)
                            row.push_back({ t, 1 });
                        else if (r == 1)
                            row.push_back({ t + 1, 1 });
                        else if (r == 2)
                            row.push_back({ t, -1 });
                        else
                            row.push_back({ t + 1, -1 });
                    } else {
                        if (r == 0)
                            row.push_back({ t + 1, 1 });
                        else if (r == 1)
                            row.push_back({ t, -1 });
                        else if (r == 2)
                            row.push_back({ t + 1, -1 });
                        else
                            row.push_back({ t, 1 });
                    }
                }
                s.addConstraint(row);
            }
        }
        for (size_t c = 0; c < corners; ++c) {
            const size_t n = mesh.nextCorner(c);
            const int constraint = cornerConstraints[c];
            if (constraint == ConstraintV) {
                s.setVariablePeriod(2 * c + 1, 1);
                s.setVariablePeriod(2 * n + 1, 1);
                hardCoordinateCount += 2;
                s.addConstraint(2 * c + 1, 1, 2 * n + 1, -1);
            } else if (constraint == ConstraintU) {
                s.setVariablePeriod(2 * c, 1);
                s.setVariablePeriod(2 * n, 1);
                hardCoordinateCount += 2;
                s.addConstraint(2 * c, 1, 2 * n, -1);
            }
        }
        s.finalizeConstraints();
        for (size_t iteration = 0; iteration < 100; ++iteration) {
            if (!s.solveIteration())
                return false;
            if (s.converged())
                break;
        }
        values->resize(variables);
        for (size_t i = 0; i < variables; ++i)
            (*values)[i] = s.value(i);
        return s.converged();
    }

    void buildResultUv(const SurfaceMesh& mesh, const std::vector<int>& rotation,
        const std::vector<double>& allValues,
        size_t uvVariables, QuadParameterizer::Result* result)
    {
        std::vector<double> uv(allValues.begin(), allValues.begin() + uvVariables);
        for (double& coordinate : uv) {
            const double integer = std::round(coordinate);
            if (std::fabs(coordinate - integer) < 0.05)
                coordinate = integer;
        }
        result->singularVertices.clear();
        for (size_t vertex = 0; vertex < mesh.vertexCount(); ++vertex) {
            const auto& fan = mesh.cornersAroundVertex(vertex);
            int sum = 0;
            bool boundary = false;
            for (size_t c : fan) {
                sum = (sum + rotation[c]) % 4;
                if (mesh.oppositeCorner(c) == SurfaceMesh::npos)
                    boundary = true;
            }
            if (!boundary && sum != 0)
                result->singularVertices.push_back(vertex);
        }
        result->triangleUvs.assign(mesh.faceCount(), std::vector<Vector2>(3));
        for (size_t f = 0; f < mesh.faceCount(); ++f)
            for (size_t l = 0; l < 3; ++l) {
                size_t c = 3 * f + l;
                result->triangleUvs[f][l] = Vector2(uv[2 * c], uv[2 * c + 1]);
            }
    }

}

bool QuadParameterizer::parameterize(const std::vector<Vector3>& vertices,
    const std::vector<std::vector<size_t>>& triangles,
    const std::vector<Vector3>* guidance, double scaling,
    double hardEdgeDegrees, Result* result,
    const std::vector<double>* faceScaling,
    const std::vector<double>* faceScalingU,
    const std::vector<double>* faceScalingV)
{
    if (vertices.empty() || triangles.empty() || scaling <= 0.0)
        return false;
    SurfaceMesh mesh(vertices, triangles);
    if (mesh.faceCount() != triangles.size())
        return false;
    const size_t corners = mesh.cornerCount();
    const size_t uvVariables = 2 * corners;
    const double scale = std::max(1e-12, scaling * mesh.averageEdgeLength());

    std::vector<Vector3> normals;
    initializeFieldAndNormals(mesh, guidance, &normals, &result->field);
    const std::vector<Vector3> fieldBeforeBrush = result->field;

    std::vector<double> activeScalingU(mesh.faceCount(), 1.0), activeScalingV(mesh.faceCount(), 1.0);
    const bool trackDirectionalScale = faceScalingU && faceScalingV
        && faceScalingU->size() == mesh.faceCount() && faceScalingV->size() == mesh.faceCount();
    if (trackDirectionalScale) {
        activeScalingU = *faceScalingU;
        activeScalingV = *faceScalingV;
    }

    if (!(guidance && guidance->size() == mesh.faceCount()))
        smoothCrossField(mesh, normals, hardEdgeDegrees, &result->field);
    brushFieldAlongSpanningTree(mesh, normals, &result->field);

    const std::vector<int> rotation = computeCornerRotations(mesh, result->field, normals);
    result->cornerRotations = rotation;
    const std::vector<signed char> cornerConstraints = computeCornerConstraints(mesh, result->field,
        normals, hardEdgeDegrees);
    if (trackDirectionalScale)
        applyDirectionalSwaps(mesh, fieldBeforeBrush, result->field, normals,
            &activeScalingU, &activeScalingV);
    const std::vector<char> seam = computeSeam(mesh, rotation);

    const CoverContext ctx { mesh, result->field, normals, rotation, seam, cornerConstraints,
        activeScalingU, activeScalingV, faceScaling, scale };

    std::vector<double> allValues;
    if (!solveQuadCover(ctx, &allValues))
        return false;

    buildResultUv(mesh, rotation, allValues, uvVariables, result);
    return true;
}
}
