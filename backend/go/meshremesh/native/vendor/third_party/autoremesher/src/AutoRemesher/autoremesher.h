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
#ifndef AUTO_REMESHER_AUTO_REMESHER_H
#define AUTO_REMESHER_AUTO_REMESHER_H
#include <AutoRemesher/Vector3>
#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <utility>
#include <vector>

namespace AutoRemesher {

class IsotropicRemesher;

enum class ModelType {
    Organic,
    HardSurface
};

typedef void (*AutoRemesherProgressHandler)(void* tag, float progress, const char* status);

class AutoRemesher {
public:
    AutoRemesher(const std::vector<Vector3>& vertices,
        const std::vector<std::vector<size_t>>& triangles)
        : m_vertices(vertices)
        , m_triangles(triangles)
    {
    }

    void setTargetTriangleCount(size_t targetTriangleCount)
    {
        m_targetTriangleCount = targetTriangleCount;
    }

    void setScaling(double scaling)
    {
        m_scaling = scaling;
    }

    void setProgressHandler(AutoRemesherProgressHandler progressHandler)
    {
        m_progressHandler = progressHandler;
    }

    void setTag(void* tag)
    {
        m_tag = tag;
    }

    void setModelType(ModelType modelType)
    {
        m_modelType = modelType;
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

    void setSmoothNormalDegrees(double degrees)
    {
        m_smoothNormalDegrees = degrees;
    }

    const std::vector<Vector3>& remeshedVertices()
    {
        return m_remeshedVertices;
    }

    const std::vector<std::vector<size_t>>& remeshedQuads()
    {
        return m_remeshedQuads;
    }

    const std::vector<Vector3>& isotropicVertices()
    {
        return m_isotropicVertices;
    }

    const std::vector<std::vector<size_t>>& isotropicTriangles()
    {
        return m_isotropicTriangles;
    }

    const std::vector<uint8_t>& isotropicExtractedConnectionMoved()
    {
        return m_isotropicExtractedConnectionMoved;
    }

    const std::vector<std::vector<Vector2>>& isotropicOriginalTriangleUvs()
    {
        return m_isotropicOriginalTriangleUvs;
    }

    const std::vector<std::vector<Vector2>>& isotropicTriangleUvs()
    {
        return m_isotropicTriangleUvs;
    }

    const std::vector<Vector3>& isotropicSingularVertices()
    {
        return m_isotropicSingularVertices;
    }

    const std::vector<std::pair<Vector3, Vector3>>& isotropicExtractedConnections()
    {
        return m_isotropicExtractedConnections;
    }

    bool remesh();

    void updateProgress(size_t threadIndex, float progress);

    static const double m_defaultSharpEdgeDegrees;

private:
    std::vector<Vector3> m_vertices;
    std::vector<std::vector<size_t>> m_triangles;
    std::vector<Vector3> m_remeshedVertices;
    std::vector<std::vector<size_t>> m_remeshedQuads;
    std::vector<Vector3> m_isotropicVertices;
    std::vector<std::vector<size_t>> m_isotropicTriangles;
    std::vector<std::vector<Vector2>> m_isotropicTriangleUvs;
    std::vector<std::vector<Vector2>> m_isotropicOriginalTriangleUvs;
    std::vector<uint8_t> m_isotropicExtractedConnectionMoved;
    std::vector<Vector3> m_isotropicSingularVertices;
    std::vector<std::pair<Vector3, Vector3>> m_isotropicExtractedConnections;
    std::vector<float> m_threadProgress;
    std::vector<float> m_threadProgressWeights;
    mutable std::mutex m_progressMutex;
    double m_scaling = 0.0;
    size_t m_targetTriangleCount = 0;
    double m_voxelSize = 0.0;
    double m_adaptivity = 1.0;
    double m_anisotropy = 1.0;
    double m_sharpEdgeDegrees = m_defaultSharpEdgeDegrees;
    double m_smoothNormalDegrees = 0.0;
    ModelType m_modelType = ModelType::Organic;
    AutoRemesherProgressHandler m_progressHandler = nullptr;
    void* m_tag = nullptr;

    static double calculateAverageEdgeLength(const std::vector<Vector3>& vertices,
        const std::vector<std::vector<size_t>>& faces);
    void initializeVoxelSize();
    static void resample(std::vector<Vector3>& vertices,
        std::vector<std::vector<size_t>>& triangles,
        double voxelSize,
        double adaptivity,
        double sharpEdgeDegrees,
        double smoothNormalDegrees,
        size_t islandIndex);
    static double calculateMeshArea(const std::vector<Vector3>& vertices,
        const std::vector<std::vector<size_t>>& triangles);
};

}

#endif
