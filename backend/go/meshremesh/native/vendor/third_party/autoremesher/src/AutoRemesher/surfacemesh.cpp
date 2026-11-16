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
#include <AutoRemesher/SurfaceMesh>

#include <algorithm>
#include <cmath>

namespace AutoRemesher {

// C++11 constexpr static data members still need a definition when odr-used.
constexpr size_t SurfaceMesh::npos;

SurfaceMesh::SurfaceMesh(const std::vector<Vector3>& positions,
    const std::vector<std::vector<size_t>>& triangles)
    : m_positions(positions)
    , m_oppositeCorners(3 * triangles.size(), npos)
    , m_cornersAroundVertex(positions.size())
{
    m_triangles.reserve(triangles.size());
    for (const auto& triangle : triangles) {
        if (triangle.size() != 3)
            continue;
        m_triangles.push_back({ triangle[0], triangle[1], triangle[2] });
    }
    m_oppositeCorners.assign(3 * m_triangles.size(), npos);
    std::vector<size_t> nextAroundVertex(cornerCount(), npos);
    std::vector<size_t> vertexCorner(vertexCount(), npos);
    for (size_t f = 0; f < faceCount(); ++f) {
        for (size_t local = 0; local < 3; ++local) {
            const size_t c = 3 * f + local;
            const size_t vertex = cornerVertex(c);
            if (vertex >= vertexCount())
                continue;
            m_cornersAroundVertex[vertex].push_back(c);
            nextAroundVertex[c] = vertexCorner[vertex];
            vertexCorner[vertex] = c;
        }
    }
    for (size_t f1 = 0; f1 < faceCount(); ++f1) {
        for (size_t local = 0; local < 3; ++local) {
            const size_t c1 = 3 * f1 + local;
            if (m_oppositeCorners[c1] != npos)
                continue;
            const size_t v2 = cornerVertex(nextCorner(c1));
            for (size_t c2 = nextAroundVertex[c1]; c2 != npos;
                c2 = nextAroundVertex[c2]) {
                if (c2 == c1)
                    continue;
                const size_t c3 = previousCorner(c2);
                if (cornerVertex(c3) != v2 || m_oppositeCorners[c3] != npos)
                    continue;
                m_oppositeCorners[c1] = c3;
                m_oppositeCorners[c3] = c1;
                break;
            }
        }
    }

    const auto detach = [&](size_t c) {
        if (c == npos || c >= m_oppositeCorners.size())
            return;
        const size_t mate = m_oppositeCorners[c];
        m_oppositeCorners[c] = npos;
        if (mate != npos && mate < m_oppositeCorners.size() && m_oppositeCorners[mate] == c)
            m_oppositeCorners[mate] = npos;
    };
    for (size_t c = 0; c < cornerCount(); ++c) {
        const size_t f2 = adjacentFace(c);
        if (f2 == npos)
            continue;
        const size_t f1 = cornerFace(c);
        size_t c2 = npos;
        for (size_t local = 0; local < 3; ++local) {
            const size_t candidate = 3 * f2 + local;
            if (adjacentFace(candidate) == f1) {
                c2 = candidate;
                break;
            }
        }
        if (c2 == npos) {
            detach(c);
            continue;
        }
        if (cornerVertex(c) != cornerVertex(nextCorner(c2))) {
            detach(c);
            detach(c2);
        }
    }
}

size_t SurfaceMesh::adjacentFace(size_t corner) const
{
    const size_t opposite = oppositeCorner(corner);
    return opposite == npos ? npos : cornerFace(opposite);
}

Vector3 SurfaceMesh::edgeVector(size_t corner) const
{
    return position(cornerVertex(nextCorner(corner))) - position(cornerVertex(corner));
}

Vector3 SurfaceMesh::faceNormal(size_t face) const
{
    const auto& faceVertices = triangle(face);
    return Vector3::normal(position(faceVertices[0]), position(faceVertices[1]),
        position(faceVertices[2]));
}

double SurfaceMesh::normalAngle(size_t corner) const
{
    const size_t neighbour = adjacentFace(corner);
    if (neighbour == npos)
        return M_PI;
    const Vector3 first = faceNormal(cornerFace(corner));
    const Vector3 second = faceNormal(neighbour);
    double cosine = Vector3::dotProduct(first, second);
    cosine = std::max(-1.0, std::min(1.0, cosine));
    const double sign = Vector3::dotProduct(Vector3::crossProduct(first, second),
                            edgeVector(corner))
            > 0.0
        ? -1.0
        : 1.0;
    return sign * std::acos(cosine);
}

double SurfaceMesh::averageEdgeLength() const
{
    double total = 0.0;
    size_t count = 0;
    for (size_t c = 0; c < cornerCount(); ++c) {
        const size_t opposite = oppositeCorner(c);
        if (opposite != npos && opposite < c)
            continue;
        total += edgeVector(c).length();
        ++count;
    }
    return count == 0 ? 0.0 : total / count;
}

}
