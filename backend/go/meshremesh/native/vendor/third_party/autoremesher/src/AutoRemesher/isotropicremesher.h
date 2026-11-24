/*
 *  Copyright (c) 2020 Jeremy HU <jeremy-at-dust3d dot org>. All rights reserved. 
 *
 *  Permission is hereby granted, free of charge, to any person obtaining a copy
 *  of this software and associated documentation files (the "Software"), to deal
 *  in the Software without restriction, including without limitation the rights
 *  to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 *  copies of the Software, and to permit persons to whom the Software is
 *  furnished to do so, subject to the following conditions:

 *  The above copyright notice and this permission notice shall be included in all
 *  copies or substantial portions of the Software.

 *  THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 *  IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 *  FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 *  AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 *  LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 *  OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 *  SOFTWARE.
 */
#ifndef AUTO_REMESHER_ISOTROPIC_REMESHER_H
#define AUTO_REMESHER_ISOTROPIC_REMESHER_H
#include <AutoRemesher/Vector3>
#include <unordered_set>

namespace AutoRemesher {

class IsotropicRemesher {
public:
    IsotropicRemesher(const std::vector<Vector3>& vertices,
        const std::vector<std::vector<size_t>>& triangles)
        : m_vertices(vertices)
        , m_triangles(triangles)
    {
    }

    void setConstraintVertices(const std::unordered_set<size_t>* constraintVertices)
    {
        m_constraintVertices = constraintVertices;
    }

    void setVertexTargetEdgeLengths(const std::vector<double>* targetLengths)
    {
        m_vertexTargetEdgeLengths = targetLengths;
    }

    void setTargetEdgeLength(double edgeLength)
    {
        m_targetEdgeLength = edgeLength;
    }

    void setSharpEdgeDegrees(double degrees)
    {
        m_sharpEdgeDegrees = degrees;
    }

    void setSmoothNormalDegrees(double degrees)
    {
        m_smoothNormalDegrees = degrees;
    }

    const std::vector<Vector3>& remeshedVertices()
    {
        return m_remeshedVertices;
    }

    const std::vector<std::vector<size_t>>& remeshedTriangles()
    {
        return m_remeshedTriangles;
    }

    bool remesh();

    void debugExportObj(const char* filename);

private:
    std::vector<Vector3> m_vertices;
    std::vector<std::vector<size_t>> m_triangles;
    const std::unordered_set<size_t>* m_constraintVertices = nullptr;
    const std::vector<double>* m_vertexTargetEdgeLengths = nullptr;
    double m_targetEdgeLength = 0;
    double m_sharpEdgeDegrees = 60;
    double m_smoothNormalDegrees = 0.0;
    int m_remeshIterations = 3;
    std::vector<Vector3> m_remeshedVertices;
    std::vector<std::vector<size_t>> m_remeshedTriangles;
};

}

#endif
