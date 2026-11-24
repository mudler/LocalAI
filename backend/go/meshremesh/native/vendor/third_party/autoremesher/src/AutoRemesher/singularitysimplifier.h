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
#ifndef AUTO_REMESHER_SINGULARITY_SIMPLIFIER_H
#define AUTO_REMESHER_SINGULARITY_SIMPLIFIER_H

#include <AutoRemesher/SurfaceMesh>
#include <cstddef>
#include <vector>

namespace AutoRemesher {

class SingularitySimplifier {
public:
    SingularitySimplifier(const SurfaceMesh& mesh,
        std::vector<Vector3>* faceField);
    void setMaximumPairDistance(size_t hops) { m_maximumPairDistance = hops; }
    void setSharpEdgeDegrees(double degrees) { m_sharpEdgeDegrees = degrees; }
    void simplify();
    std::vector<int> vertexCharges() const;
    size_t singularityCount() const;
    size_t singularityCountBefore() const { return m_singularityCountBefore; }
    size_t singularityCountAfter() const { return m_singularityCountAfter; }
    size_t cancelledPairCount() const { return m_cancelledPairCount; }

private:
    const SurfaceMesh& m_mesh;
    std::vector<Vector3>* m_field;
    size_t m_maximumPairDistance = 6, m_maximumRounds = 4, m_regionMargin = 6;
    double m_sharpEdgeDegrees = 90.0;
    size_t m_singularityCountBefore = 0, m_singularityCountAfter = 0, m_cancelledPairCount = 0;
    std::vector<double> m_angles, m_connection;
    std::vector<int> m_mismatch;
    std::vector<bool> m_sharpCorner;
    std::vector<Vector3> m_frameU, m_frameV;
    void buildFramesAndConnection();
    void updateMismatches(const std::vector<size_t>& faces);
    std::vector<size_t> facetsAroundVertex(size_t vertex) const;
    size_t cornerAlongEdge(size_t from, size_t to) const;
    std::vector<size_t> pathBetween(size_t first, size_t second,
        const std::vector<char>& inRegion, const std::vector<size_t>& freeFaces) const;
    bool cancelPair(size_t first, size_t second, size_t hops);
};

}
#endif
