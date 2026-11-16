/*
 *  Copyright (c) 2020-2021 Jeremy HU <jeremy-at-dust3d dot org>. All rights reserved. 
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
#ifndef ISOTROPIC_HALFEDGE_MESH_H
#define ISOTROPIC_HALFEDGE_MESH_H
#include <set>
#include <functional>
#include "vector3.h"
#include <cstdint>

class IsotropicHalfedgeMesh
{
public:
    struct Halfedge;
      
    struct Vertex
    {
        Vector3 position;
        Halfedge *firstHalfedge = nullptr;
        Vertex *previousVertex = nullptr;
        Vertex *nextVertex = nullptr;
        int initialFaces = 0;
        int _valence = -1;
        bool _isBoundary = false;
        Vector3 _normal;
        Vector3 _smoothNormal; // Smooth normal from input (propagated through splits)
        bool removed = false;
        size_t debugIndex = 0;
        size_t outputIndex = 0;
        bool featured = false;
        double targetEdgeLength = 0.0; // 0 = use global default in split/collapse
        Vertex *_allocLink = nullptr;
    };

    struct Face
    {
        Halfedge *halfedge = nullptr;
        Face *previousFace = nullptr;
        Face *nextFace = nullptr;
        Vector3 _normal;
        bool removed = false;
        size_t debugIndex = 0;
        Face *_allocLink = nullptr;
    };

    struct Halfedge
    {
        Vertex *startVertex = nullptr;
        Face *leftFace = nullptr;
        Halfedge *nextHalfedge = nullptr;
        Halfedge *previousHalfedge = nullptr;
        Halfedge *oppositeHalfedge = nullptr;
        int featureState = -1;
        size_t debugIndex = 0;
        Halfedge *_allocLink = nullptr;
    };
    
    IsotropicHalfedgeMesh(const std::vector<Vector3> &vertices,
        const std::vector<std::vector<size_t>> &faces);
    ~IsotropicHalfedgeMesh();
    
    double averageEdgeLength();
    void breakEdge(Halfedge *halfedge);
    bool collapseEdge(Halfedge *halfedge, double maxEdgeLengthSquared);
    bool flipEdge(Halfedge *halfedge);
    void relaxVertex(Vertex *vertex);
    size_t vertexValence(Vertex *vertex, bool *isBoundary=nullptr);
    Face *moveToNextFace(Face *face);
    Vertex *moveToNextVertex(Vertex *vertex);
    void updateVertexValences();
    void updateVertexNormals();
    void updateTriangleNormals();
    void featureEdges(double radians);
    void featureBoundaries();
private:
    Vertex *m_firstVertex = nullptr;
    Vertex *m_lastVertex = nullptr;
    Face *m_firstFace = nullptr;
    Face *m_lastFace = nullptr;
    size_t m_debugVertexIndex = 0;
    size_t m_debugFaceIndex = 0;
    size_t m_debugHalfedgeIndex = 0;
    Vertex *m_vertexAllocLink = nullptr;
    Face *m_faceAllocLink = nullptr;
    Halfedge *m_halfedgeAllocLink = nullptr;
    
    static inline uint64_t makeHalfedgeKey(size_t first, size_t second)
    {
        return (first << 32) | second;
    }
    
    static inline uint64_t swapHalfedgeKey(uint64_t key)
    {
        return makeHalfedgeKey(key & 0xffffffff, key >> 32);
    }
    
    Face *firstFace()
    {
        return m_firstFace;
    }
    
    Vertex *firstVertex()
    {
        return m_firstVertex;
    }
    
    Face *newFace();
    Vertex *newVertex();
    Halfedge *newHalfedge();
    void linkFaceHalfedges(std::vector<Halfedge *> &halfedges);
    void updateFaceHalfedgesLeftFace(std::vector<Halfedge *> &halfedges,
        Face *leftFace);
    void linkHalfedgePair(Halfedge *first, Halfedge *second);
    void breakFace(Face *leftOldFace,
        Halfedge *halfedge,
        Vertex *breakPointVertex,
        std::vector<Halfedge *> &leftNewFaceHalfedges,
        std::vector<Halfedge *> &leftOldFaceHalfedges);
    void pointerVertexToNewVertex(Vertex *vertex, Vertex *replacement);
    bool testLengthSquaredAroundVertex(Vertex *vertex, 
        const Vector3 &target, 
        double maxEdgeLengthSquared);
    void collectVerticesAroundVertex(Vertex *vertex,
        std::set<Vertex *> *vertices);
    void featureHalfedge(Halfedge *halfedge, double radians);
    void iterateVertexHalfedges(Vertex *vertex, std::function<bool(Halfedge *halfedge)> handler);
};

#endif
