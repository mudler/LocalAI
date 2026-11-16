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
#ifndef AUTO_REMESHER_SURFACE_MESH_H
#define AUTO_REMESHER_SURFACE_MESH_H

#include <AutoRemesher/Vector2>
#include <AutoRemesher/Vector3>
#include <array>
#include <cstddef>
#include <limits>
#include <vector>

namespace AutoRemesher {

class SurfaceMesh {
public:
    static constexpr size_t npos = std::numeric_limits<size_t>::max();

    SurfaceMesh(const std::vector<Vector3>& positions,
        const std::vector<std::vector<size_t>>& triangles);

    size_t vertexCount() const { return m_positions.size(); }
    size_t faceCount() const { return m_triangles.size(); }
    size_t cornerCount() const { return 3 * m_triangles.size(); }
    size_t cornerFace(size_t corner) const { return corner / 3; }
    size_t cornerLocal(size_t corner) const { return corner % 3; }
    size_t cornerVertex(size_t corner) const { return m_triangles[corner / 3][corner % 3]; }
    size_t nextCorner(size_t corner) const { return 3 * (corner / 3) + (corner + 1) % 3; }
    size_t previousCorner(size_t corner) const { return 3 * (corner / 3) + (corner + 2) % 3; }
    size_t oppositeCorner(size_t corner) const { return m_oppositeCorners[corner]; }
    size_t adjacentFace(size_t corner) const;
    bool isBoundaryCorner(size_t corner) const { return oppositeCorner(corner) == npos; }

    const Vector3& position(size_t vertex) const { return m_positions[vertex]; }
    const std::array<size_t, 3>& triangle(size_t face) const { return m_triangles[face]; }
    const std::vector<size_t>& cornersAroundVertex(size_t vertex) const { return m_cornersAroundVertex[vertex]; }
    Vector3 edgeVector(size_t corner) const;
    Vector3 faceNormal(size_t face) const;
    double normalAngle(size_t corner) const;
    double averageEdgeLength() const;

private:
    std::vector<Vector3> m_positions;
    std::vector<std::array<size_t, 3>> m_triangles;
    std::vector<size_t> m_oppositeCorners;
    std::vector<std::vector<size_t>> m_cornersAroundVertex;
};

}

#endif
