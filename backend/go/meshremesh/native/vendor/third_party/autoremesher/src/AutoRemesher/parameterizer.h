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
#ifndef AUTO_REMESHER_PARAMETERIZER_H
#define AUTO_REMESHER_PARAMETERIZER_H
#include <AutoRemesher/Vector2>
#include <AutoRemesher/Vector3>
#include <map>
#include <vector>

namespace AutoRemesher {

class Parameterizer {
public:
    Parameterizer(const std::vector<Vector3>* vertices,
        const std::vector<std::vector<size_t>>* triangles,
        const std::vector<Vector3>* triangleFieldVectors)
        : m_vertices(vertices)
        , m_triangles(triangles)
        , m_triangleFieldVectors(triangleFieldVectors)
    {
    }

    ~Parameterizer()
    {
        delete m_triangleUvs;
    }

    const std::vector<std::vector<Vector2>>& originalTriangleUvs() const
    {
        return m_originalTriangleUvs;
    }

    std::vector<std::vector<Vector2>>* takeTriangleUvs()
    {
        std::vector<std::vector<Vector2>>* triangleUvs = m_triangleUvs;
        m_triangleUvs = nullptr;
        return triangleUvs;
    }

    const std::vector<Vector3>& singularVertexPositions() const
    {
        return m_singularVertexPositions;
    }

    const std::vector<size_t>& singularVertexIndices() const
    {
        return m_singularVertexIndices;
    }

    void setScaling(double scaling)
    {
        m_scaling = scaling;
    }

    void setGradientAdaptivity(double adaptivity)
    {
        m_adaptivity = adaptivity;
    }

    void setSharpEdgeDegrees(double degrees)
    {
        m_sharpEdgeDegrees = degrees;
    }

    void setAnisotropy(double anisotropy)
    {
        m_anisotropy = anisotropy;
    }

    void setSingularitySimplification(bool simplify)
    {
        m_singularitySimplification = simplify;
    }

    void setMaximumSingularityPairDistance(size_t faceHops)
    {
        m_maximumSingularityPairDistance = faceHops;
    }

    bool parameterize();

private:
    const std::vector<Vector3>* m_vertices = nullptr;
    const std::vector<std::vector<size_t>>* m_triangles = nullptr;
    const std::vector<Vector3>* m_triangleFieldVectors = nullptr;
    std::vector<std::vector<Vector2>>* m_triangleUvs = nullptr;
    std::vector<Vector3> m_singularVertexPositions;
    std::vector<size_t> m_singularVertexIndices;
    std::vector<std::vector<Vector2>> m_originalTriangleUvs;
    double m_scaling = 1.0;
    double m_adaptivity = 0.5;
    double m_sharpEdgeDegrees = 90.0;
    double m_anisotropy = 1.0;
    double m_maxAspectRatio = 2.3;
    bool m_singularitySimplification = true;
    size_t m_maximumSingularityPairDistance = 6;

    std::vector<double> computeFaceScalingField(const std::vector<Vector3>& vertices,
        const std::vector<std::vector<size_t>>& triangles,
        const std::vector<Vector3>& vertexNormals,
        const std::map<size_t, std::vector<size_t>>& faceAroundVertexMap) const;
};

}

#endif
