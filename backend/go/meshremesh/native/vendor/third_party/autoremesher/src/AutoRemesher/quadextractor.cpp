/*
 *  Copyright (c) 2026 Jeremy HU <jeremy-at-dust3d dot org>. All rights reserved. 
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
#include "../../t2_ar_log.h"
#include <AutoRemesher/Double>
#include <AutoRemesher/MeshSeparator>
#include <AutoRemesher/PositionKey>
#include <AutoRemesher/QuadExtractor>
#include <algorithm>
#include <limits>
#include <map>
#include <queue>
#include <set>
#include <unordered_map>
#include <unordered_set>

namespace AutoRemesher {

bool QuadExtractor::extract()
{
    T2_AR_LOG << "Extract connections..." << std::endl;
    std::vector<Vector3> crossPoints;
    std::vector<size_t> crossPointSourceTriangles;
    std::set<std::pair<size_t, size_t>> connections;
    extractConnections(&crossPoints, &crossPointSourceTriangles, &connections);
    holdSingularLines(&crossPoints, &crossPointSourceTriangles, &connections);
    m_extractedConnections.clear();
    m_extractedConnectionMoved.clear();
    m_extractedConnections.reserve(connections.size());
    std::vector<uint8_t> triangleMoved;
    if (nullptr != m_originalTriangleUvs
        && m_originalTriangleUvs->size() == m_triangleUvs->size()) {
        triangleMoved.assign(m_triangleUvs->size(), 0);
        for (size_t i = 0; i < triangleMoved.size(); ++i) {
            const auto& before = (*m_originalTriangleUvs)[i];
            const auto& after = (*m_triangleUvs)[i];
            for (size_t k = 0; k < 3 && k < before.size() && k < after.size(); ++k) {
                if (before[k].x() != after[k].x() || before[k].y() != after[k].y()) {
                    triangleMoved[i] = 1;
                    break;
                }
            }
        }
    }
    m_extractedConnectionMoved.reserve(connections.size());
    for (const auto& connection : connections) {
        m_extractedConnections.emplace_back(crossPoints[connection.first],
            crossPoints[connection.second]);
        const auto edge = std::make_pair(std::min(connection.first, connection.second),
            std::max(connection.first, connection.second));
        if (m_addedConnections.end() != m_addedConnections.find(edge)) {
            m_extractedConnectionMoved.push_back(2);
        } else if (!triangleMoved.empty()) {
            const size_t firstTriangle = crossPointSourceTriangles[connection.first];
            const size_t secondTriangle = crossPointSourceTriangles[connection.second];
            m_extractedConnectionMoved.push_back(
                (triangleMoved[firstTriangle] || triangleMoved[secondTriangle]) ? 1 : 0);
        } else {
            m_extractedConnectionMoved.push_back(0);
        }
    }
    T2_AR_LOG << "Extract connections done" << std::endl;

#if AUTO_REMESHER_DEV
    {
        FILE* fp = fopen("debug-quadextractor-connections.obj", "wb");
        for (size_t i = 0; i < crossPoints.size(); ++i) {
            const auto& vertex = crossPoints[i];
            fprintf(fp, "v %f %f %f\n", vertex.x(), vertex.y(), vertex.z());
        }
        for (const auto& it : connections) {
            fprintf(fp, "l %zu %zu\n", 1 + it.first, 1 + it.second);
        }
        fclose(fp);
    }
#endif

    T2_AR_LOG << "Extract edges..." << std::endl;
    std::unordered_map<size_t, std::unordered_set<size_t>> edgeConnectMap;
    extractEdges(connections, &edgeConnectMap);
    if (collapseShortEdges(&crossPoints, &edgeConnectMap))
        simplifyGraph(edgeConnectMap);
    collapseTriangles(&crossPoints, &edgeConnectMap);
    if (removeSingleEndpoints(&crossPoints, &edgeConnectMap))
        simplifyGraph(edgeConnectMap);

    T2_AR_LOG << "Extract edges done" << std::endl;

#if AUTO_REMESHER_DEV
    {
        std::vector<Vector3> edgeVertices;
        std::unordered_map<size_t, size_t> oldToNewMap;
        auto addEdgeVertex = [&](size_t vertexIndex) {
            auto insertResult = oldToNewMap.insert({ vertexIndex, edgeVertices.size() });
            if (insertResult.second)
                edgeVertices.push_back(crossPoints[vertexIndex]);
            return insertResult.first->second;
        };
        for (const auto& it : edgeConnectMap) {
            addEdgeVertex(it.first);
            for (const auto& it2 : it.second) {
                addEdgeVertex(it2);
            }
        }
        FILE* fp = fopen("debug-quadextractor-edges.obj", "wb");
        for (size_t i = 0; i < edgeVertices.size(); ++i) {
            const auto& vertex = edgeVertices[i];
            fprintf(fp, "v %f %f %f\n", vertex.x(), vertex.y(), vertex.z());
        }
        for (const auto& it : edgeConnectMap) {
            for (const auto& it2 : it.second)
                fprintf(fp, "l %zu %zu\n", 1 + oldToNewMap[it.first], 1 + oldToNewMap[it2]);
        }
        fclose(fp);
    }
#endif

    T2_AR_LOG << "Extract mesh..." << std::endl;
    extractMesh(crossPoints, crossPointSourceTriangles, edgeConnectMap, &m_remeshedPolygons);
    T2_AR_LOG << "Extract mesh done" << std::endl;

    fixHoles();

#if AUTO_REMESHER_DEV
    {
        FILE* fp = fopen("debug-quadextractor-quads-withoutfix.obj", "wb");
        for (size_t i = 0; i < m_remeshedVertices.size(); ++i) {
            const auto& vertex = m_remeshedVertices[i];
            fprintf(fp, "v %f %f %f\n", vertex.x(), vertex.y(), vertex.z());
        }
        for (const auto& it : m_remeshedPolygons) {
            fprintf(fp, "f");
            std::unordered_set<size_t> indices;
            for (const auto& v : it) {
                indices.insert(v);
                fprintf(fp, " %zu", 1 + v);
            }
            if (indices.size() != it.size()) {
                T2_AR_LOG << "Found repeated vertices in face:";
                for (const auto& v : it) {
                    T2_AR_LOG << v << " ";
                }
                T2_AR_LOG << std::endl;
            }
            fprintf(fp, "\n");
        }
        fclose(fp);
    }
#endif

    bool changed = false;
    if (removeIsolatedFaces())
        changed = true;
    while (removeNonManifoldFaces()) {
        changed = true;
        removeIsolatedFaces();
    }

    if (changed) {
        rebuildHalfEdges();
        fixHoles();
    }

    {
        std::set<size_t> usedVertices;
        for (const auto& face : m_remeshedPolygons) {
            for (const auto& v : face)
                usedVertices.insert(v);
        }
        if (usedVertices.size() < m_remeshedVertices.size()) {
            std::vector<Vector3> compactedVertices;
            std::unordered_map<size_t, size_t> oldToNew;
            compactedVertices.reserve(usedVertices.size());
            for (const auto& oldIndex : usedVertices) {
                oldToNew[oldIndex] = compactedVertices.size();
                compactedVertices.push_back(m_remeshedVertices[oldIndex]);
            }
            for (auto& face : m_remeshedPolygons) {
                for (auto& v : face)
                    v = oldToNew[v];
            }
            m_remeshedVertices = std::move(compactedVertices);
        }
    }

#if AUTO_REMESHER_DEV
    {
        FILE* fp = fopen("debug-quadextractor-quads.obj", "wb");
        for (size_t i = 0; i < m_remeshedVertices.size(); ++i) {
            const auto& vertex = m_remeshedVertices[i];
            fprintf(fp, "v %f %f %f\n", vertex.x(), vertex.y(), vertex.z());
        }
        for (const auto& it : m_remeshedPolygons) {
            fprintf(fp, "f");
            for (const auto& v : it)
                fprintf(fp, " %zu", 1 + v);
            fprintf(fp, "\n");
        }
        fclose(fp);
    }
#endif

    return true;
}

void QuadExtractor::extractEdges(const std::set<std::pair<size_t, size_t>>& connections,
    std::unordered_map<size_t, std::unordered_set<size_t>>* edgeConnectMap)
{
    auto& graph = *edgeConnectMap;
    for (const auto& it : connections) {
        graph[it.first].insert(it.second);
        graph[it.second].insert(it.first);
    }
    simplifyGraph(graph);
}

void QuadExtractor::simplifyGraph(std::unordered_map<size_t, std::unordered_set<size_t>>& graph)
{
    for (;;) {
        std::unordered_map<size_t, std::pair<size_t, size_t>> delayPairs;
        for (auto it = graph.begin(); it != graph.end();) {
            if (it->second.size() != 2) {
                ++it;
                continue;
            }
            // Dissolving this one would turn the ring arc it sits on into a single
            // edge, and the cell it belongs to from a quad into a triangle
            if (m_pinnedPoints.end() != m_pinnedPoints.find(it->first)) {
                ++it;
                continue;
            }
            size_t firstNeighbor, secondNeighbor;
            auto neighborIt = it->second.begin();
            firstNeighbor = *neighborIt++;
            secondNeighbor = *neighborIt++;
            if (delayPairs.end() != delayPairs.find(firstNeighbor) || delayPairs.end() != delayPairs.find(secondNeighbor)) {
                ++it;
                continue;
            }
            delayPairs.insert({ it->first, { firstNeighbor, secondNeighbor } });
            it = graph.erase(it);
        }
        if (delayPairs.empty())
            break;
        for (const auto& it : delayPairs) {
            graph[it.second.first].erase(it.first);
            graph[it.second.first].insert(it.second.second);
            graph[it.second.second].erase(it.first);
            graph[it.second.second].insert(it.second.first);
        }
    }
}

bool QuadExtractor::removeSingleEndpoints(std::vector<Vector3>* crossPoints,
    std::unordered_map<size_t, std::unordered_set<size_t>>* edgeConnectMap)
{
    bool removed = false;
    std::unordered_map<size_t, std::unordered_set<size_t>>& graph = *edgeConnectMap;
    std::vector<size_t> endpoints;
    for (auto it = graph.begin(); it != graph.end(); ++it) {
        if (it->second.size() != 1)
            continue;
        endpoints.push_back(it->first);
    }
    for (const auto& endpoint : endpoints) {
        size_t loopIndex = endpoint;
        for (;;) {
            auto findEndpoint = graph.find(loopIndex);
            if (findEndpoint == graph.end())
                break;
            if (findEndpoint->second.size() != 1)
                break;
            size_t neighbor = *findEndpoint->second.begin();
            graph.erase(loopIndex);
            removed = true;
            auto findNeighbor = graph.find(neighbor);
            if (findNeighbor == graph.end())
                break;
            findNeighbor->second.erase(loopIndex);
            loopIndex = neighbor;
        }
    }
    return removed;
}

bool QuadExtractor::collapseTriangles(std::vector<Vector3>* crossPoints,
    std::unordered_map<size_t, std::unordered_set<size_t>>* edgeConnectMap)
{
    auto& graph = *edgeConnectMap;

    std::set<std::tuple<size_t, size_t, size_t>> triangles;
    for (const auto& level0It : graph) {
        const auto& level0 = level0It.first;
        for (const auto& level1 : level0It.second) {
            auto findLevel2 = graph.find(level1);
            if (findLevel2 == graph.end())
                continue;
            for (const auto& level2 : findLevel2->second) {
                if (level0 == level2)
                    continue;
                auto findLevel3 = graph.find(level2);
                if (findLevel3 == graph.end())
                    continue;
                if (findLevel3->second.end() == findLevel3->second.find(level0))
                    continue;
                std::vector<size_t> sorted = { level0, level1, level2 };
                std::sort(sorted.begin(), sorted.end());
                triangles.insert(std::make_tuple(sorted[0], sorted[1], sorted[2]));
            }
        }
    }

    if (triangles.empty())
        return false;

    // Collapse one edge per triangle, the shortest one. Merging every connected
    // triangle into a single point instead would drag all of their neighbors onto
    // that point and leave a star of slivers behind it
    bool collapsed = false;
    for (const auto& triangle : triangles) {
        const size_t corners[3] = { std::get<0>(triangle), std::get<1>(triangle), std::get<2>(triangle) };
        std::pair<size_t, size_t> shortestEdge;
        double shortestLength = std::numeric_limits<double>::max();
        bool stillATriangle = true;
        for (size_t i = 0; i < 3; ++i) {
            size_t j = (i + 1) % 3;
            auto findCorner = graph.find(corners[i]);
            if (findCorner == graph.end() || findCorner->second.end() == findCorner->second.find(corners[j])) {
                stillATriangle = false;
                break;
            }
            double length = ((*crossPoints)[corners[i]] - (*crossPoints)[corners[j]]).length();
            if (length < shortestLength) {
                shortestLength = length;
                shortestEdge = { corners[i], corners[j] };
            }
        }
        // An earlier collapse may have already taken this one apart
        if (!stillATriangle)
            continue;
        collapseEdge(crossPoints, edgeConnectMap, shortestEdge);
        // The survivor inherits the pin, the arc it was holding open is still there
        if (m_pinnedPoints.end() != m_pinnedPoints.find(shortestEdge.first)) {
            m_pinnedPoints.erase(shortestEdge.first);
            m_pinnedPoints.insert(shortestEdge.second);
        }
        collapsed = true;
    }

    return collapsed;
}

bool QuadExtractor::collapseShortEdges(std::vector<Vector3>* crossPoints,
    std::unordered_map<size_t, std::unordered_set<size_t>>* edgeConnectMap)
{
    double totalLength = 0.0;
    size_t edgeCount = 0;
    std::map<std::pair<size_t, size_t>, double> edgeLengths;
    for (const auto& it : *edgeConnectMap) {
        for (const auto& neighbor : it.second) {
            if (edgeLengths.end() != edgeLengths.find({ neighbor, it.first }))
                continue;
            double edgeLength = ((*crossPoints)[it.first] - (*crossPoints)[neighbor]).length();
            totalLength += edgeLength;
            edgeLengths.insert({ { it.first, neighbor }, edgeLength });
            ++edgeCount;
        }
    }
    if (0 == edgeCount)
        return false;
    double averageEdgeLength = totalLength / edgeCount;
    double collapsedLength = averageEdgeLength * 0.01;
    bool collapsed = false;
    for (const auto& it : edgeLengths) {
        if (it.second > collapsedLength)
            continue;
        collapseEdge(crossPoints, edgeConnectMap, it.first);
        collapsed = true;
    }
    return collapsed;
}

void QuadExtractor::collapseEdge(std::vector<Vector3>* crossPoints,
    std::unordered_map<size_t, std::unordered_set<size_t>>* edgeConnectMap,
    const std::pair<size_t, size_t>& edge)
{
    auto findSecondNeighbors = edgeConnectMap->find(edge.second);
    if (findSecondNeighbors == edgeConnectMap->end())
        return;
    auto findFirstNeighbors = edgeConnectMap->find(edge.first);
    if (findFirstNeighbors == edgeConnectMap->end())
        return;
    if (findSecondNeighbors->second.end() == findSecondNeighbors->second.find(edge.first))
        return;
    if (findFirstNeighbors->second.end() == findFirstNeighbors->second.find(edge.second))
        return;
    auto firstNeighbors = findFirstNeighbors->second;
    (*crossPoints)[edge.second] = ((*crossPoints)[edge.first] + (*crossPoints)[edge.second]) * 0.5;
    for (const auto& neighbor : firstNeighbors) {
        if (neighbor == edge.second)
            continue;
        (*edgeConnectMap)[edge.second].insert(neighbor);
        (*edgeConnectMap)[neighbor].insert(edge.second);
        (*edgeConnectMap)[neighbor].erase(edge.first);
    }
    (*edgeConnectMap).erase(edge.first);
    (*edgeConnectMap)[edge.second].erase(edge.first);
    if ((*edgeConnectMap)[edge.second].empty())
        (*edgeConnectMap).erase(edge.second);
}

void QuadExtractor::extractMesh(std::vector<Vector3>& points,
    const std::vector<size_t>& pointSourceTriangles,
    std::unordered_map<size_t, std::unordered_set<size_t>>& edgeConnectMap,
    std::vector<std::vector<size_t>>* quads)
{
    std::unordered_map<size_t, Vector3> triangleNormals;
    for (size_t pointIndex = 0; pointIndex < pointSourceTriangles.size(); ++pointIndex) {
        const auto& it = pointSourceTriangles[pointIndex];
        const auto& triangleVertices = (*m_triangles)[it];
        auto triangleNormal = Vector3::normal((*m_vertices)[triangleVertices[0]],
            (*m_vertices)[triangleVertices[1]],
            (*m_vertices)[triangleVertices[2]]);
        triangleNormals.insert({ pointIndex, triangleNormal });
    }

    auto calculateFaceNormal = [&](const std::vector<size_t>& corners) {
        Vector3 center;
        for (size_t i = 0; i < corners.size(); ++i) {
            center += points[corners[i]];
        }
        center /= corners.size();
        Vector3 normals;
        for (size_t i = 0; i < corners.size(); ++i) {
            normals += Vector3::normal(points[corners[(i + 0) % corners.size()]],
                points[corners[(i + 1) % corners.size()]],
                center);
        }
        return normals.normalized();
    };

    auto calculateSide = [&](const std::vector<size_t>& corners) {
        auto ringNormal = calculateFaceNormal(corners);
        Vector3 originalNormal;
        for (const auto& it : corners)
            originalNormal += triangleNormals[it];
        auto dot = Vector3::dotProduct(ringNormal, originalNormal.normalized());
        const double dotThreshold = 0.259; // > 75 or < 105 degrees
        if (dot > dotThreshold)
            return (int)1;
        else if (dot < -dotThreshold)
            return (int)-1;
        return (int)0;
    };

    std::set<std::tuple<size_t, size_t, size_t>> corners;
    auto& halfEdges = m_halfEdges;
    auto isConerUsed = [&](size_t previous, size_t current, size_t next) {
        if (corners.end() != corners.find(std::make_tuple(previous, current, next)))
            return true;
        if (corners.end() != corners.find(std::make_tuple(next, current, previous)))
            return true;
        return false;
    };
    auto isFaceCornerExist = [&](const std::vector<size_t>& vertices) {
        for (size_t i = 0; i < vertices.size(); ++i) {
            size_t j = (i + 1) % vertices.size();
            size_t k = (i + 2) % vertices.size();
            if (isConerUsed(vertices[i], vertices[j], vertices[k]))
                return true;
        }
        return false;
    };
    auto addFaceCorners = [&](const std::vector<size_t>& vertices) {
        for (size_t i = 0; i < vertices.size(); ++i) {
            size_t j = (i + 1) % vertices.size();
            size_t k = (i + 2) % vertices.size();
            corners.insert(std::make_tuple(vertices[i], vertices[j], vertices[k]));
            corners.insert(std::make_tuple(vertices[k], vertices[j], vertices[i]));
        }
    };
    auto isFaceHalfEdgeExist = [&](const std::vector<size_t>& vertices) {
        for (size_t i = 0; i < vertices.size(); ++i) {
            size_t j = (i + 1) % vertices.size();
            if (halfEdges.end() != halfEdges.find({ vertices[i], vertices[j] }))
                return true;
        }
        return false;
    };
    auto addFaceHalfEdges = [&](const std::vector<size_t>& vertices) {
        for (size_t i = 0; i < vertices.size(); ++i) {
            size_t j = (i + 1) % vertices.size();
            halfEdges.insert({ vertices[i], vertices[j] });
        }
    };

    size_t triangleRound = 4;
    for (size_t round = 0; round < 5; ++round) {
        for (const auto& level0It : edgeConnectMap) {
            const auto& level0 = level0It.first;
            auto findLevel1 = edgeConnectMap.find(level0);
            if (findLevel1 == edgeConnectMap.end())
                continue;
            const auto& triangleVertices = (*m_triangles)[pointSourceTriangles[level0]];
            auto triangleNormal = Vector3::normal((*m_vertices)[triangleVertices[0]],
                (*m_vertices)[triangleVertices[1]],
                (*m_vertices)[triangleVertices[2]]);
            for (const auto& level1 : findLevel1->second) {
                auto findLevel2 = edgeConnectMap.find(level1);
                if (findLevel2 == edgeConnectMap.end())
                    continue;
                if (halfEdges.find({ level0, level1 }) != halfEdges.end() && halfEdges.find({ level1, level0 }) != halfEdges.end())
                    continue;
                for (const auto& level2 : findLevel2->second) {
                    if (level0 == level2)
                        continue;
                    auto findLevel3 = edgeConnectMap.find(level2);
                    if (findLevel3 == edgeConnectMap.end())
                        continue;
                    if (halfEdges.find({ level1, level2 }) != halfEdges.end() && halfEdges.find({ level2, level1 }) != halfEdges.end())
                        continue;
                    for (const auto& level3 : findLevel3->second) {
                        if (level0 == level3) {
                            if (triangleRound == round) {
                                if (!isFaceCornerExist({ level0, level1, level2 })) {
                                    auto side = calculateSide({ level0, level1, level2 });
                                    if (side > 0) {
                                        if (!isFaceHalfEdgeExist({ level0, level1, level2 })) {
                                            quads->push_back({ level0, level1, level2 });
                                            addFaceCorners({ level0, level1, level2 });
                                            addFaceHalfEdges({ level0, level1, level2 });
                                        }
                                    } else if (side < 0) {
                                        if (!isFaceHalfEdgeExist({ level2, level1, level0 })) {
                                            quads->push_back({ level2, level1, level0 });
                                            addFaceCorners({ level2, level1, level0 });
                                            addFaceHalfEdges({ level2, level1, level0 });
                                        }
                                    }
                                }
                                break;
                            }
                        } else {
                            if (triangleRound == round)
                                break;
                        }
                        if (level1 == level3 || level0 == level3)
                            continue;
                        auto findLevel4 = edgeConnectMap.find(level3);
                        if (findLevel4 == edgeConnectMap.end())
                            continue;
                        if (halfEdges.find({ level2, level3 }) != halfEdges.end() && halfEdges.find({ level3, level2 }) != halfEdges.end())
                            continue;
                        for (const auto& level4 : findLevel4->second) {
                            if (level0 != level4) {
                                if (level2 == level4 || level1 == level4)
                                    continue;
                                if (round < 1)
                                    continue;
                                auto findLevel5 = edgeConnectMap.find(level4);
                                if (findLevel5 == edgeConnectMap.end())
                                    continue;
                                if (halfEdges.find({ level3, level4 }) != halfEdges.end() && halfEdges.find({ level4, level3 }) != halfEdges.end())
                                    continue;
                                for (const auto& level5 : findLevel5->second) {
                                    if (level0 != level5) {
                                        if (level3 == level5 || level2 == level5 || level1 == level5)
                                            continue;
                                        if (round < 2)
                                            continue;
                                        auto findLevel6 = edgeConnectMap.find(level5);
                                        if (findLevel6 == edgeConnectMap.end())
                                            continue;
                                        if (halfEdges.find({ level4, level5 }) != halfEdges.end() && halfEdges.find({ level5, level4 }) != halfEdges.end())
                                            continue;
                                        for (const auto& level6 : findLevel6->second) {
                                            if (level0 != level6) {
                                                if (level4 == level6 || level3 == level6 || level2 == level6 || level1 == level6)
                                                    continue;
                                                if (round < 3)
                                                    continue;
                                                auto findLevel7 = edgeConnectMap.find(level6);
                                                if (findLevel7 == edgeConnectMap.end())
                                                    continue;
                                                if (halfEdges.find({ level5, level6 }) != halfEdges.end() && halfEdges.find({ level6, level5 }) != halfEdges.end())
                                                    continue;
                                                for (const auto& level7 : findLevel7->second) {
                                                    if (level0 != level7)
                                                        continue;
                                                    if (3 != round)
                                                        break;
                                                    if (!isFaceCornerExist({ level0, level1, level2, level3, level4, level5, level6 })) {
                                                        auto side = calculateSide({ level0, level1, level2, level3, level4, level5, level6 });
                                                        if (side > 0) {
                                                            if (!isFaceHalfEdgeExist({ level0, level1, level2, level3, level4, level5, level6 })) {
                                                                quads->push_back({ level0, level1, level2, level3, level4, level5, level6 });
                                                                addFaceCorners({ level0, level1, level2, level3, level4, level5, level6 });
                                                                addFaceHalfEdges({ level0, level1, level2, level3, level4, level5, level6 });
                                                            }
                                                        } else if (side < 0) {
                                                            if (!isFaceHalfEdgeExist({ level6, level5, level4, level3, level2, level1, level0 })) {
                                                                quads->push_back({ level6, level5, level4, level3, level2, level1, level0 });
                                                                addFaceCorners({ level6, level5, level4, level3, level2, level1, level0 });
                                                                addFaceHalfEdges({ level6, level5, level4, level3, level2, level1, level0 });
                                                            }
                                                        }
                                                    }
                                                    break;
                                                }
                                                continue;
                                            }
                                            if (2 != round)
                                                break;
                                            if (!isFaceCornerExist({ level0, level1, level2, level3, level4, level5 })) {
                                                auto side = calculateSide({ level0, level1, level2, level3, level4, level5 });
                                                if (side > 0) {
                                                    if (!isFaceHalfEdgeExist({ level0, level1, level2, level3, level4, level5 })) {
                                                        quads->push_back({ level0, level1, level2, level3, level4, level5 });
                                                        addFaceCorners({ level0, level1, level2, level3, level4, level5 });
                                                        addFaceHalfEdges({ level0, level1, level2, level3, level4, level5 });
                                                    }
                                                } else if (side < 0) {
                                                    if (!isFaceHalfEdgeExist({ level5, level4, level3, level2, level1, level0 })) {
                                                        quads->push_back({ level5, level4, level3, level2, level1, level0 });
                                                        addFaceCorners({ level5, level4, level3, level2, level1, level0 });
                                                        addFaceHalfEdges({ level5, level4, level3, level2, level1, level0 });
                                                    }
                                                }
                                            }
                                            break;
                                        }
                                        continue;
                                    }
                                    if (1 != round)
                                        break;
                                    if (!isFaceCornerExist({ level0, level1, level2, level3, level4 })) {
                                        auto side = calculateSide({ level0, level1, level2, level3, level4 });
                                        if (side > 0) {
                                            if (!isFaceHalfEdgeExist({ level0, level1, level2, level3, level4 })) {
                                                quads->push_back({ level0, level1, level2, level3, level4 });
                                                addFaceCorners({ level0, level1, level2, level3, level4 });
                                                addFaceHalfEdges({ level0, level1, level2, level3, level4 });
                                            }
                                        } else if (side < 0) {
                                            if (!isFaceHalfEdgeExist({ level4, level3, level2, level1, level0 })) {
                                                quads->push_back({ level4, level3, level2, level1, level0 });
                                                addFaceCorners({ level4, level3, level2, level1, level0 });
                                                addFaceHalfEdges({ level4, level3, level2, level1, level0 });
                                            }
                                        }
                                    }
                                    break;
                                }
                                continue;
                            }
                            if (0 != round)
                                break;
                            if (!isFaceCornerExist({ level0, level1, level2, level3 })) {
                                auto side = calculateSide({ level0, level1, level2, level3 });
                                if (side > 0) {
                                    if (!isFaceHalfEdgeExist({ level0, level1, level2, level3 })) {
                                        quads->push_back({ level0, level1, level2, level3 });
                                        addFaceCorners({ level0, level1, level2, level3 });
                                        addFaceHalfEdges({ level0, level1, level2, level3 });
                                    }
                                } else if (side < 0) {
                                    if (!isFaceHalfEdgeExist({ level3, level2, level1, level0 })) {
                                        quads->push_back({ level3, level2, level1, level0 });
                                        addFaceCorners({ level3, level2, level1, level0 });
                                        addFaceHalfEdges({ level3, level2, level1, level0 });
                                    }
                                }
                            }
                            break;
                        }
                    }
                }
            }
        }
    }

    m_remeshedVertices = points;
}

void QuadExtractor::extractConnections(std::vector<Vector3>* crossPoints,
    std::vector<size_t>* sourceTriangles,
    std::set<std::pair<size_t, size_t>>* connections)
{
    std::map<PositionKey, size_t> crossPointMap;

    m_connectionInfos.clear();
    m_addedConnections.clear();
    m_pinnedPoints.clear();

    auto addCrossPoint = [&](const Vector3& position3, size_t triangleIndex) {
        auto insertResult = crossPointMap.insert({ position3, crossPoints->size() });
        if (insertResult.second) {
            crossPoints->push_back(position3);
            sourceTriangles->push_back(triangleIndex);
        }
        return insertResult.first->second;
    };
    auto addConnection = [&](size_t fromPointIndex, size_t toPointIndex,
                             size_t triangleIndex, int coordIndex, int integer) {
        if (fromPointIndex == toPointIndex)
            return;
        connections->insert({ fromPointIndex, toPointIndex });
        m_connectionInfos.insert({ { std::min(fromPointIndex, toPointIndex),
                                       std::max(fromPointIndex, toPointIndex) },
            ConnectionInfo { triangleIndex, coordIndex, integer } });
    };

    struct CrossPoint {
        Vector3 position3;
        Vector2 position2;
        int integer;
    };

    for (size_t triangleIndex = 0; triangleIndex < m_triangles->size(); ++triangleIndex) {
        //T2_AR_LOG << "Extract connections for triangle:" << triangleIndex << "..." << std::endl;

        const auto& cornerUvs = (*m_triangleUvs)[triangleIndex];
        const auto& cornerIndices = (*m_triangles)[triangleIndex];

        // Extract intersections of isolines with edges
        std::map<int, std::vector<std::vector<CrossPoint>>> lines[2];
        bool edgeCollapsed[2][3] = { { false, false, false },
            { false, false, false } };
        for (size_t i = 0; i < 2; ++i) {
            for (size_t j = 0; j < 3; ++j) {
                size_t k = (j + 1) % 3;
                const auto& current = cornerUvs[j];
                const auto& next = cornerUvs[k];
                if ((Double::isZero((double)(int)current[i] - current[i]) && Double::isZero(current[i] - next[i]))) {
                    int integer = (int)current[i];
                    edgeCollapsed[i][j] = true;
                    CrossPoint fromPoint;
                    fromPoint.position3 = (*m_vertices)[cornerIndices[j]];
                    fromPoint.position2 = cornerUvs[j];
                    fromPoint.integer = integer;
                    CrossPoint toPoint;
                    toPoint.position3 = (*m_vertices)[cornerIndices[k]];
                    toPoint.position2 = cornerUvs[k];
                    toPoint.integer = integer;
                    lines[i][integer].push_back({ fromPoint, toPoint });
                }
            }
            std::map<int, std::vector<CrossPoint>> points;
            for (size_t j = 0; j < 3; ++j) {
                size_t k = (j + 1) % 3;
                const auto& current = cornerUvs[j];
                const auto& next = cornerUvs[k];
                double distance = std::abs(current[i] - next[i]);
                if ((int)current[i] != (int)next[i] || (current[i] > 0) != (next[i] > 0)) {
                    int lowInteger, highInteger;
                    double fromPosition;
                    double toPosition;
                    size_t fromIndex, toIndex;
                    if (current[i] < next[i]) {
                        lowInteger = (int)current[i];
                        highInteger = (int)next[i];
                        fromPosition = current[i];
                        toPosition = next[i];
                        fromIndex = j;
                        toIndex = k;
                    } else {
                        lowInteger = (int)next[i];
                        highInteger = (int)current[i];
                        fromPosition = next[i];
                        toPosition = current[i];
                        fromIndex = k;
                        toIndex = j;
                    }
                    for (int integer = lowInteger; integer <= highInteger; ++integer) {
                        double ratio = (integer - fromPosition) / distance;
                        if (ratio < 0 || ratio > 1)
                            continue;
                        if (Double::isZero(ratio) || Double::isZero(ratio - 1.0)) {
                            if (edgeCollapsed[i][j])
                                continue;
                        }
                        CrossPoint point;
                        point.position3 = (*m_vertices)[cornerIndices[fromIndex]] * (1 - ratio) + (*m_vertices)[cornerIndices[toIndex]] * ratio;
                        point.position2 = cornerUvs[fromIndex] * (1 - ratio) + cornerUvs[toIndex] * ratio;
                        point.integer = integer;
                        points[integer].push_back(point);
                    }
                }
            }
            for (const auto& it : points) {
                for (size_t pointIndex = 0; pointIndex < it.second.size(); ++pointIndex) {
                    size_t nextPointIndex = (pointIndex + 1) % it.second.size();
                    const auto& point = it.second[pointIndex];
                    const auto& nextPoint = it.second[nextPointIndex];
                    lines[i][it.first].push_back({ point, nextPoint });
                }
            }
        }

        //T2_AR_LOG << "Segment lines by isolines for triangle:" << triangleIndex << "..." << std::endl;

        // Segment lines by isolines
        for (size_t i = 0; i < 2; ++i) {
            size_t j = (i + 1) % 2;
            for (const auto& targetIt : lines[i]) {
                for (const auto& target : targetIt.second) {
                    std::vector<std::vector<CrossPoint>> segments = { target };
                    for (const auto& splitIt : lines[j]) {
                        const auto& split = splitIt.second.begin();
                        const auto& coordIndex = j;
                        double segmentPosition = split[0][0].position2[coordIndex];
                        for (int segmentIndex = (int)segments.size() - 1; segmentIndex >= 0; --segmentIndex) {
                            auto& segment = segments[segmentIndex];
                            double fromPosition;
                            double toPosition;
                            size_t fromIndex;
                            size_t toIndex;
                            const auto& uv0 = segment[0].position2;
                            const auto& uv1 = segment[1].position2;
                            double distance = std::abs(uv0[coordIndex] - uv1[coordIndex]);
                            if (Double::isZero(distance))
                                continue;
                            if (uv0[coordIndex] < uv1[coordIndex]) {
                                fromPosition = uv0[coordIndex];
                                toPosition = uv1[coordIndex];
                                fromIndex = 0;
                                toIndex = 1;
                            } else {
                                fromPosition = uv1[coordIndex];
                                toPosition = uv0[coordIndex];
                                fromIndex = 1;
                                toIndex = 0;
                            }
                            if (segmentPosition < fromPosition || segmentPosition > toPosition)
                                continue;
                            double ratio = (segmentPosition - fromPosition) / distance;
                            //T2_AR_LOG << "Split at ratio:" << ratio << std::endl;
                            Vector3 position3 = segment[fromIndex].position3 * (1 - ratio) + segment[toIndex].position3 * ratio;
                            Vector2 position2 = segment[fromIndex].position2 * (1 - ratio) + segment[toIndex].position2 * ratio;
                            int integer = segment[toIndex].integer;
                            CrossPoint newFromPoint;
                            newFromPoint.position3 = position3;
                            newFromPoint.position2 = position2;
                            newFromPoint.integer = integer;
                            CrossPoint newToPoint = segment[toIndex];
                            segment[toIndex] = newFromPoint;
                            segments.push_back({ newFromPoint, newToPoint });
                        }
                    }
                    for (const auto& segment : segments) {
                        addConnection(addCrossPoint(segment[0].position3, triangleIndex),
                            addCrossPoint(segment[1].position3, triangleIndex),
                            triangleIndex, (int)i, targetIt.first);
                    }
                }
            }
        }
    }
}

// Cones left with no or a single isoline branch cannot anchor the lines that bend
// around them: those lines come back on themselves, form a U, and the U is dropped
// because nothing ties it into the grid. Walk out of the cone towards the nearest U
// top, cut through every U met along the way, and stop on the first straight line.
// The chain of new connections holds the U's in place.
void QuadExtractor::holdSingularLines(std::vector<Vector3>* crossPoints,
    std::vector<size_t>* sourceTriangles,
    std::set<std::pair<size_t, size_t>>* connections)
{
    if (nullptr == m_singularVertices || m_singularVertices->empty())
        return;

    const size_t ringCount = 4;
    // Total bend, in radians, that makes a line count as a U
    const double uTurningThreshold = M_PI * 0.5;
    const size_t maxChainPoints = 512;

    auto makeEdge = [](size_t first, size_t second) {
        return std::make_pair(std::min(first, second), std::max(first, second));
    };

    std::map<PositionKey, size_t> crossPointMap;
    for (size_t i = 0; i < crossPoints->size(); ++i)
        crossPointMap.insert({ (*crossPoints)[i], i });

    std::unordered_map<size_t, std::vector<size_t>> trianglesAroundVertex;
    for (size_t triangleIndex = 0; triangleIndex < m_triangles->size(); ++triangleIndex) {
        for (const auto& vertexIndex : (*m_triangles)[triangleIndex])
            trianglesAroundVertex[vertexIndex].push_back(triangleIndex);
    }

    size_t addedConnections = 0;
    size_t starvedCones = 0;
    size_t pinnedPoints = 0;
    size_t walkedCones = 0;
    for (const auto& singularVertexIndex : *m_singularVertices) {
        if (singularVertexIndex >= m_vertices->size())
            continue;
        const auto& singularPosition = (*m_vertices)[singularVertexIndex];

        // Only the starved cones are interesting, the well connected ones already
        // hold their lines
        size_t singularPointIndex = std::numeric_limits<size_t>::max();
        {
            auto findCrossPoint = crossPointMap.find(PositionKey(singularPosition));
            if (findCrossPoint != crossPointMap.end())
                singularPointIndex = findCrossPoint->second;
        }
        // Count the branches, not the entries: a segment shared by two triangles is
        // held in the connection set under both orientations, and counting those
        // separately doubles the valence and hides most of the starved cones
        std::unordered_set<size_t> branches;
        if (std::numeric_limits<size_t>::max() != singularPointIndex) {
            for (const auto& connection : *connections) {
                if (connection.first == singularPointIndex)
                    branches.insert(connection.second);
                else if (connection.second == singularPointIndex)
                    branches.insert(connection.first);
            }
        }
        const size_t valence = branches.size();
        const size_t comingFromPointIndex = 1 == valence
            ? *branches.begin()
            : std::numeric_limits<size_t>::max();
        ++starvedCones;
        if (valence > 1) {
            --starvedCones;
            continue;
        }

        // Gather four rings of neighbor triangles
        std::unordered_set<size_t> neighborTriangles;
        {
            std::unordered_set<size_t> ringVertices = { singularVertexIndex };
            for (size_t ring = 0; ring < ringCount; ++ring) {
                std::unordered_set<size_t> nextRingVertices;
                for (const auto& vertexIndex : ringVertices) {
                    auto findTriangles = trianglesAroundVertex.find(vertexIndex);
                    if (findTriangles == trianglesAroundVertex.end())
                        continue;
                    for (const auto& triangleIndex : findTriangles->second) {
                        neighborTriangles.insert(triangleIndex);
                        for (const auto& corner : (*m_triangles)[triangleIndex])
                            nextRingVertices.insert(corner);
                    }
                }
                ringVertices = std::move(nextRingVertices);
            }
        }
        if (neighborTriangles.empty())
            continue;

        // Collect the connections sitting on those triangles
        std::map<std::pair<size_t, size_t>, ConnectionInfo> localEdges;
        for (const auto& connection : *connections) {
            auto edge = makeEdge(connection.first, connection.second);
            auto findInfo = m_connectionInfos.find(edge);
            if (findInfo == m_connectionInfos.end())
                continue;
            if (neighborTriangles.end() == neighborTriangles.find(findInfo->second.triangleIndex))
                continue;
            localEdges.insert({ edge, findInfo->second });
        }
        if (localEdges.size() < 3)
            continue;

        std::unordered_map<size_t, std::vector<size_t>> localGraph;
        double totalEdgeLength = 0.0;
        for (const auto& it : localEdges) {
            localGraph[it.first.first].push_back(it.first.second);
            localGraph[it.first.second].push_back(it.first.first);
            totalEdgeLength += ((*crossPoints)[it.first.first] - (*crossPoints)[it.first.second]).length();
        }
        const double averageEdgeLength = totalEdgeLength / localEdges.size();
        if (Double::isZero(averageEdgeLength))
            continue;

        // Chain the segments into lines, following the isoline label through the
        // crossings and falling back to the straightest continuation
        std::vector<std::vector<size_t>> chains;
        std::map<std::pair<size_t, size_t>, size_t> edgeToChain;
        {
            std::set<std::pair<size_t, size_t>> visitedEdges;
            auto extend = [&](std::vector<size_t>& points, const ConnectionInfo& label) {
                while (points.size() < maxChainPoints) {
                    size_t current = points.back();
                    size_t previous = points[points.size() - 2];
                    Vector3 direction = ((*crossPoints)[current] - (*crossPoints)[previous]).normalized();
                    size_t best = std::numeric_limits<size_t>::max();
                    double bestScore = -1.0;
                    bool bestLabeled = false;
                    auto findNeighbors = localGraph.find(current);
                    if (findNeighbors == localGraph.end())
                        break;
                    for (const auto& neighbor : findNeighbors->second) {
                        if (neighbor == previous)
                            continue;
                        auto edge = makeEdge(current, neighbor);
                        if (visitedEdges.end() != visitedEdges.find(edge))
                            continue;
                        auto findInfo = localEdges.find(edge);
                        if (findInfo == localEdges.end())
                            continue;
                        const bool labeled = findInfo->second.coordIndex == label.coordIndex
                            && findInfo->second.integer == label.integer;
                        double score = Vector3::dotProduct(direction,
                            ((*crossPoints)[neighbor] - (*crossPoints)[current]).normalized());
                        if (labeled != bestLabeled) {
                            if (!labeled)
                                continue;
                        } else if (score <= bestScore) {
                            continue;
                        }
                        best = neighbor;
                        bestScore = score;
                        bestLabeled = labeled;
                    }
                    if (std::numeric_limits<size_t>::max() == best)
                        break;
                    visitedEdges.insert(makeEdge(current, best));
                    points.push_back(best);
                }
            };
            for (const auto& it : localEdges) {
                if (visitedEdges.end() != visitedEdges.find(it.first))
                    continue;
                visitedEdges.insert(it.first);
                std::vector<size_t> points = { it.first.first, it.first.second };
                extend(points, it.second);
                std::reverse(points.begin(), points.end());
                extend(points, it.second);
                for (size_t i = 1; i < points.size(); ++i)
                    edgeToChain[makeEdge(points[i - 1], points[i])] = chains.size();
                chains.push_back(std::move(points));
            }
        }

        // A line is a U when it bends far enough to come back on itself
        std::vector<uint8_t> chainIsU(chains.size(), 0);
        std::vector<Vector3> chainTops(chains.size());
        for (size_t chainIndex = 0; chainIndex < chains.size(); ++chainIndex) {
            const auto& points = chains[chainIndex];
            if (points.size() < 4)
                continue;
            const auto& first = (*crossPoints)[points[0]];
            const auto& last = (*crossPoints)[points.back()];
            // How far the line bends over its whole length. Measuring the turning
            // rather than the two end tangents keeps working on the chains the four
            // ring window cuts short before they have come all the way around
            double turning = 0.0;
            for (size_t i = 2; i < points.size(); ++i) {
                Vector3 previousDirection = ((*crossPoints)[points[i - 1]] - (*crossPoints)[points[i - 2]]).normalized();
                Vector3 nextDirection = ((*crossPoints)[points[i]] - (*crossPoints)[points[i - 1]]).normalized();
                if (previousDirection.isZero() || nextDirection.isZero())
                    continue;
                // Signed against the surface, so a line that merely wiggles cancels
                // itself out and only a line that keeps turning one way adds up
                auto findInfo = localEdges.find(makeEdge(points[i - 1], points[i]));
                if (findInfo == localEdges.end())
                    continue;
                const auto& triangle = (*m_triangles)[findInfo->second.triangleIndex];
                Vector3 triangleNormal = Vector3::normal((*m_vertices)[triangle[0]],
                    (*m_vertices)[triangle[1]], (*m_vertices)[triangle[2]]);
                double angle = Vector3::angle(previousDirection, nextDirection);
                if (Vector3::dotProduct(Vector3::crossProduct(previousDirection, nextDirection), triangleNormal) < 0.0)
                    angle = -angle;
                turning += angle;
            }
            if (std::abs(turning) < uTurningThreshold)
                continue;
            // The top of the U is the point that bulges farthest from its chord
            Vector3 chord = last - first;
            double chordLength = chord.length();
            size_t topIndex = points.size() / 2;
            if (!Double::isZero(chordLength)) {
                Vector3 chordDirection = chord / chordLength;
                double maxDistance = -1.0;
                for (size_t i = 1; i + 1 < points.size(); ++i) {
                    Vector3 offset = (*crossPoints)[points[i]] - first;
                    double distance = (offset - chordDirection * Vector3::dotProduct(offset, chordDirection)).length();
                    if (distance > maxDistance) {
                        maxDistance = distance;
                        topIndex = i;
                    }
                }
            }
            chainIsU[chainIndex] = 1;
            chainTops[chainIndex] = (*crossPoints)[points[topIndex]];
        }

        // A cone that holds its lines carries three branches. A starved one carries
        // one, or none, so the walk supplies the missing ones: three separatrices
        // sit 120 degrees apart, and one crossbar cannot close the cells between the
        // U's on its own
        Vector3 coneNormal;
        {
            auto findTriangles = trianglesAroundVertex.find(singularVertexIndex);
            if (findTriangles == trianglesAroundVertex.end())
                continue;
            for (const auto& triangleIndex : findTriangles->second) {
                const auto& triangle = (*m_triangles)[triangleIndex];
                coneNormal += Vector3::normal((*m_vertices)[triangle[0]],
                    (*m_vertices)[triangle[1]], (*m_vertices)[triangle[2]]);
            }
            coneNormal = coneNormal.normalized();
            if (coneNormal.isZero())
                continue;
        }

        // The branch that is already there fixes where the missing ones go. Without
        // one, the closest U's top stands in for it and all three get walked
        Vector3 seedDirection;
        if (std::numeric_limits<size_t>::max() != comingFromPointIndex) {
            seedDirection = (*crossPoints)[comingFromPointIndex] - singularPosition;
        } else {
            double nearestDistance = std::numeric_limits<double>::max();
            for (size_t chainIndex = 0; chainIndex < chains.size(); ++chainIndex) {
                if (!chainIsU[chainIndex])
                    continue;
                double distance = (chainTops[chainIndex] - singularPosition).length();
                if (distance < nearestDistance) {
                    nearestDistance = distance;
                    seedDirection = chainTops[chainIndex] - singularPosition;
                }
            }
            if (nearestDistance == std::numeric_limits<double>::max())
                continue;
        }
        seedDirection = (seedDirection - coneNormal * Vector3::dotProduct(seedDirection, coneNormal)).normalized();
        if (seedDirection.isZero())
            continue;

        std::vector<Vector3> walkDirections;
        {
            auto rotateAroundConeNormal = [&](const Vector3& direction, double angle) {
                const double cosine = std::cos(angle);
                const double sine = std::sin(angle);
                return direction * cosine + Vector3::crossProduct(coneNormal, direction) * sine
                    + coneNormal * Vector3::dotProduct(coneNormal, direction) * (1.0 - cosine);
            };
            const double branchAngle = M_PI * 2.0 / 3.0;
            if (std::numeric_limits<size_t>::max() == comingFromPointIndex)
                walkDirections.push_back(seedDirection);
            walkDirections.push_back(rotateAroundConeNormal(seedDirection, branchAngle));
            walkDirections.push_back(rotateAroundConeNormal(seedDirection, -branchAngle));
        }

        // Walk out along a direction and collect what it cuts through. The walk steps
        // from crossing to crossing rather than following one straight ray, otherwise
        // it leaves the surface long before it reaches the outer lines
        struct Crossing {
            Vector3 position;
            size_t chainIndex;
            std::pair<size_t, size_t> edge;
        };
        auto march = [&](Vector3 walkDirection) {
            const double tolerance = 0.5 * averageEdgeLength;
            const double parallelCosineThreshold = 0.9;
            std::vector<Crossing> crossings;
            Vector3 walkPosition = singularPosition;
            std::unordered_set<size_t> visitedChains;
            for (size_t step = 0; step < chains.size(); ++step) {
                bool found = false;
                Crossing nearest {};
                double nearestDistance = std::numeric_limits<double>::max();
                for (const auto& it : localEdges) {
                    auto findChain = edgeToChain.find(it.first);
                    if (findChain == edgeToChain.end())
                        continue;
                    if (visitedChains.end() != visitedChains.find(findChain->second))
                        continue;
                    const auto& from = (*crossPoints)[it.first.first];
                    const auto& to = (*crossPoints)[it.first.second];
                    Vector3 edgeVector = to - from;
                    // A line running along the walk is not something the walk crosses
                    if (std::abs(Vector3::dotProduct(walkDirection, edgeVector.normalized())) > parallelCosineThreshold)
                        continue;
                    // Closest approach between the walk ray and this segment
                    Vector3 offset = from - walkPosition;
                    double a = Vector3::dotProduct(edgeVector, edgeVector);
                    double b = Vector3::dotProduct(walkDirection, edgeVector);
                    double c = Vector3::dotProduct(walkDirection, offset);
                    double d = Vector3::dotProduct(edgeVector, offset);
                    double denominator = a - b * b;
                    double edgeRatio = Double::isZero(denominator) ? 0.0 : (b * c - d) / denominator;
                    if (edgeRatio < 0.0)
                        edgeRatio = 0.0;
                    else if (edgeRatio > 1.0)
                        edgeRatio = 1.0;
                    Vector3 pointOnEdge = from + edgeVector * edgeRatio;
                    double distance = Vector3::dotProduct(pointOnEdge - walkPosition, walkDirection);
                    if (distance <= tolerance)
                        continue;
                    if (distance >= nearestDistance)
                        continue;
                    Vector3 pointOnRay = walkPosition + walkDirection * distance;
                    if ((pointOnEdge - pointOnRay).length() > tolerance)
                        continue;
                    nearest = Crossing { pointOnEdge, findChain->second, it.first };
                    nearestDistance = distance;
                    found = true;
                }
                if (!found)
                    break;
                crossings.push_back(nearest);
                visitedChains.insert(nearest.chainIndex);
                // Stop as soon as the walk reaches a line that is not a U, that line
                // is what the whole chain hangs from
                if (!chainIsU[nearest.chainIndex])
                    break;
                // Carry the direction on across the line just crossed, flattened onto
                // the surface there so the walk keeps hugging the mesh
                auto findInfo = localEdges.find(nearest.edge);
                if (findInfo != localEdges.end()) {
                    const auto& triangle = (*m_triangles)[findInfo->second.triangleIndex];
                    Vector3 triangleNormal = Vector3::normal((*m_vertices)[triangle[0]],
                        (*m_vertices)[triangle[1]], (*m_vertices)[triangle[2]]);
                    Vector3 flattened = walkDirection - triangleNormal * Vector3::dotProduct(walkDirection, triangleNormal);
                    if (!flattened.isZero())
                        walkDirection = flattened.normalized();
                }
                walkPosition = nearest.position;
            }
            return crossings;
        };

        // Splitting the crossed segment at the crossing point gives us something to
        // connect to
        auto splitAt = [&](const Crossing& crossing) {
            auto findInfo = m_connectionInfos.find(crossing.edge);
            if (findInfo == m_connectionInfos.end())
                return std::numeric_limits<size_t>::max();
            const auto info = findInfo->second;
            auto insertResult = crossPointMap.insert({ PositionKey(crossing.position), crossPoints->size() });
            if (!insertResult.second)
                return insertResult.first->second;
            const size_t newPointIndex = crossPoints->size();
            crossPoints->push_back(crossing.position);
            sourceTriangles->push_back(info.triangleIndex);
            connections->erase({ crossing.edge.first, crossing.edge.second });
            connections->erase({ crossing.edge.second, crossing.edge.first });
            m_connectionInfos.erase(crossing.edge);
            for (const auto& endpoint : { crossing.edge.first, crossing.edge.second }) {
                connections->insert({ endpoint, newPointIndex });
                m_connectionInfos.insert({ makeEdge(endpoint, newPointIndex), info });
            }
            return newPointIndex;
        };

        // Where two spokes cut the same ring, the ring arc between them has to keep a
        // point of its own, otherwise it simplifies down to a single edge and the
        // cell against the cone comes out a triangle instead of a quad
        std::map<size_t, std::vector<size_t>> crossedPositionsOfChain;
        auto positionInChain = [&](size_t chainIndex, const std::pair<size_t, size_t>& edge) {
            const auto& points = chains[chainIndex];
            for (size_t i = 1; i < points.size(); ++i) {
                if (makeEdge(points[i - 1], points[i]) == edge)
                    return i - 1;
            }
            return std::numeric_limits<size_t>::max();
        };

        bool coneWalked = false;
        for (const auto& walkDirection : walkDirections) {
            std::vector<size_t> walkPoints;
            for (const auto& crossing : march(walkDirection)) {
                size_t pointIndex = splitAt(crossing);
                if (std::numeric_limits<size_t>::max() == pointIndex)
                    continue;
                size_t position = positionInChain(crossing.chainIndex, crossing.edge);
                if (std::numeric_limits<size_t>::max() != position)
                    crossedPositionsOfChain[crossing.chainIndex].push_back(position);
                walkPoints.push_back(pointIndex);
            }
            if (walkPoints.size() < 2)
                continue;

            if (std::numeric_limits<size_t>::max() == singularPointIndex) {
                auto insertResult = crossPointMap.insert({ PositionKey(singularPosition), crossPoints->size() });
                if (insertResult.second) {
                    singularPointIndex = crossPoints->size();
                    crossPoints->push_back(singularPosition);
                    sourceTriangles->push_back(*neighborTriangles.begin());
                } else {
                    singularPointIndex = insertResult.first->second;
                }
            }

            size_t previousPointIndex = singularPointIndex;
            for (const auto& pointIndex : walkPoints) {
                if (pointIndex == previousPointIndex)
                    continue;
                auto edge = makeEdge(previousPointIndex, pointIndex);
                if (m_connectionInfos.end() == m_connectionInfos.find(edge)) {
                    connections->insert({ previousPointIndex, pointIndex });
                    ConnectionInfo info;
                    info.triangleIndex = (*sourceTriangles)[pointIndex];
                    info.coordIndex = -1;
                    info.integer = 0;
                    m_connectionInfos.insert({ edge, info });
                    m_addedConnections.insert(edge);
                    ++addedConnections;
                }
                previousPointIndex = pointIndex;
            }
            coneWalked = true;
        }

        for (auto& it : crossedPositionsOfChain) {
            auto& positions = it.second;
            if (positions.size() < 2)
                continue;
            std::sort(positions.begin(), positions.end());
            const auto& points = chains[it.first];
            for (size_t i = 1; i < positions.size(); ++i) {
                // The arc runs from the edge one spoke cut to the edge the next one
                // cut, so the points in between are the candidates to keep
                if (positions[i] <= positions[i - 1])
                    continue;
                size_t middle = (positions[i - 1] + 1 + positions[i]) / 2;
                if (middle >= points.size())
                    continue;
                // No point pinning something that sits right on top of a crossing
                const auto& middlePosition = (*crossPoints)[points[middle]];
                if ((middlePosition - (*crossPoints)[points[positions[i - 1]]]).length() < 0.25 * averageEdgeLength)
                    continue;
                if ((middlePosition - (*crossPoints)[points[positions[i] + 1]]).length() < 0.25 * averageEdgeLength)
                    continue;
                m_pinnedPoints.insert(points[middle]);
                ++pinnedPoints;
            }
        }

        if (coneWalked)
            ++walkedCones;
    }

    T2_AR_LOG << "Hold singular lines walked " << walkedCones << " of " << starvedCones
              << " starved cone(s), added " << addedConnections << " connection(s), pinned "
              << pinnedPoints << " ring point(s)" << std::endl;
}

bool QuadExtractor::testPointInTriangle(const std::vector<Vector3>& points,
    const std::vector<size_t>& triangle,
    const std::vector<size_t>& testPoints)
{
    Vector3 triangleNormal = Vector3::normal(points[triangle[0]],
        points[triangle[1]], points[triangle[2]]);
    std::vector<Vector3> pointsIn3d;
    for (const auto& it : triangle)
        pointsIn3d.push_back(points[it]);
    for (const auto& it : testPoints)
        pointsIn3d.push_back(points[it]);
    std::vector<Vector2> pointsIn2d;
    Vector3 origin = (points[triangle[0]] + points[triangle[1]] + points[triangle[2]]) / 3;
    Vector3 axis = (points[triangle[0]] - origin).normalized();
    Vector3::project(pointsIn3d, &pointsIn2d, triangleNormal, axis, origin);
    const Vector2& a = pointsIn2d[0];
    const Vector2& b = pointsIn2d[1];
    const Vector2& c = pointsIn2d[2];
    for (size_t i = 3; i < pointsIn2d.size(); ++i) {
        if (Vector2::isInTriangle(a, b, c, pointsIn2d[i]))
            return true;
    }
    return false;
}

void QuadExtractor::rebuildHalfEdges()
{
    m_halfEdges.clear();
    for (const auto& it : m_remeshedPolygons) {
        for (size_t i = 0; i < it.size(); ++i) {
            size_t j = (i + 1) % it.size();
            m_halfEdges.insert({ it[i], it[j] });
        }
    }
}

void QuadExtractor::fixHoles()
{
    std::vector<std::vector<size_t>> loops;
    searchBoundaries(m_halfEdges, &loops);
    for (auto& loop : loops) {
        if (loop.size() > 65) {
            T2_AR_LOG << "Ignore long hole at length:" << loop.size() << std::endl;
            continue;
        }
        T2_AR_LOG << "Fixing hole at length:" << loop.size() << "..." << std::endl;
        fixHoleWithQuads(loop, true);
        if (loop.size() >= 4)
            fixHoleWithQuads(loop, false);
    }
}

void QuadExtractor::fixHoleWithQuads(std::vector<size_t>& hole, bool checkScore)
{
    auto recordHalfEdgesOfLastPolygon = [&]() {
        const auto& it = m_remeshedPolygons[m_remeshedPolygons.size() - 1];
        for (size_t i = 0; i < it.size(); ++i) {
            size_t j = (i + 1) % it.size();
            m_halfEdges.insert({ it[i], it[j] });
        }
    };

    for (;;) {
        if (hole.size() <= 2) {
            T2_AR_LOG << "fixHoleWithQuads cancel on edge length:" << hole.size() << std::endl;
            return;
        }

        if (3 == hole.size()) {
            m_remeshedPolygons.push_back({ (size_t)hole[2], (size_t)hole[1], (size_t)hole[0] });
            recordHalfEdgesOfLastPolygon();
            return;
        }

        if (4 == hole.size()) {
            m_remeshedPolygons.push_back({ (size_t)hole[3], (size_t)hole[2], (size_t)hole[1], (size_t)hole[0] });
            recordHalfEdgesOfLastPolygon();
            return;
        }

        std::vector<std::pair<int, double>> edgeScores;
        edgeScores.reserve(hole.size());
        for (int i = 0; i < hole.size(); ++i) {
            int h = (i + hole.size() - 1) % hole.size();
            int j = (i + 1) % hole.size();
            int k = (j + 1) % hole.size();
            auto left = (m_remeshedVertices[hole[h]] - m_remeshedVertices[hole[i]]).normalized();
            auto right = (m_remeshedVertices[hole[k]] - m_remeshedVertices[hole[j]]).normalized();
            edgeScores.push_back({ i, Vector3::dotProduct(left, right) });
        }
        std::sort(edgeScores.begin(), edgeScores.end(), [](const std::pair<int, double>& first, const std::pair<int, double>& second) {
            return first.second < second.second;
        });
        bool holeChanged = false;
        for (int edgeIndex = edgeScores.size() - 1; edgeIndex >= 0; --edgeIndex) {
            const auto& score = edgeScores[edgeIndex];
            if (checkScore) {
                if (score.second <= 0) {
                    T2_AR_LOG << "fixHoleWithQuads failed, highest score(dot):" << score.second << std::endl;
                    return;
                }
            }
            int i = score.first;
            int h = (i + hole.size() - 1) % hole.size();
            int j = (i + 1) % hole.size();
            int k = (j + 1) % hole.size();
            std::vector<size_t> candidate = { (size_t)hole[k], (size_t)hole[j], (size_t)hole[i], (size_t)hole[h] };
            if (m_halfEdges.end() != m_halfEdges.find({ candidate[0], candidate[1] }) || m_halfEdges.end() != m_halfEdges.find({ candidate[1], candidate[2] }) || m_halfEdges.end() != m_halfEdges.find({ candidate[2], candidate[3] }) || m_halfEdges.end() != m_halfEdges.find({ candidate[3], candidate[0] })) {
                T2_AR_LOG << "fixHoleWithQuads ignore score:" << score.second << " because conflicts with existed quads" << std::endl;
                continue;
            }
            std::vector<size_t> remainPoints;
            for (int w = 0; w < hole.size(); ++w) {
                if (w == i || w == j || w == h || w == k)
                    continue;
                remainPoints.push_back(hole[w]);
            }
            if (testPointInTriangle(m_remeshedVertices, { candidate[0], candidate[1], candidate[2] }, remainPoints) || testPointInTriangle(m_remeshedVertices, { candidate[2], candidate[3], candidate[0] }, remainPoints)) {
                T2_AR_LOG << "fixHoleWithQuads ignore score:" << score.second << " because other point in the same loop fall into quad plane" << std::endl;
                continue;
            }
            m_remeshedPolygons.push_back(candidate);
            recordHalfEdgesOfLastPolygon();

            std::vector<size_t> newHole;
            for (int w = 0; w < hole.size(); ++w) {
                if (w == i || w == j)
                    continue;
                newHole.push_back(hole[w]);
            }
            hole = newHole;
            holeChanged = true;
            break;
        }
        if (!holeChanged)
            break;
    }
}

void QuadExtractor::searchBoundaries(const std::set<std::pair<size_t, size_t>>& halfEdges,
    std::vector<std::vector<size_t>>* loops)
{
    T2_AR_LOG << "Searching boundaries..." << std::endl;

    std::unordered_map<size_t, std::unordered_set<size_t>> nextMap;
    for (const auto& it : halfEdges) {
        if (halfEdges.end() != halfEdges.find({ it.second, it.first }))
            continue;
        nextMap[it.first].insert(it.second);
    }

    while (!nextMap.empty()) {
        auto it = nextMap.begin();
        std::vector<size_t> loop;
        size_t startVertex = it->first;
        bool validate = false;
        T2_AR_LOG << "Searching loop from:" << startVertex << std::endl;
        while (it != nextMap.end()) {
            if (startVertex == it->first && loop.size() >= 3) {
                T2_AR_LOG << "Found valid loop, size:" << loop.size() << std::endl;
                validate = true;
                break;
            }
            T2_AR_LOG << "Loop add vertex:" << it->first << std::endl;
            loop.push_back(it->first);
            if (it->second.size() != 1) {
                T2_AR_LOG << "Break loop, because of next size:" << it->second.size() << std::endl;
                break;
            }
            it = nextMap.find(*it->second.begin());
        }
        for (const auto& v : loop)
            nextMap.erase(v);
        if (validate)
            loops->push_back(loop);
    }

    T2_AR_LOG << "Searching boundaries done" << std::endl;
}

bool QuadExtractor::removeIsolatedFaces()
{
    std::vector<std::vector<std::vector<size_t>>> quadsIslands;
    MeshSeparator::splitToIslands(m_remeshedPolygons, quadsIslands);
    if (quadsIslands.empty())
        return false;
    m_remeshedPolygons = *std::max_element(quadsIslands.begin(), quadsIslands.end(), [&](const std::vector<std::vector<size_t>>& first, const std::vector<std::vector<size_t>>& second) {
        return first.size() < second.size();
    });
    return true;
}

bool QuadExtractor::removeNonManifoldFaces()
{
    bool changed = false;
    std::map<std::pair<size_t, size_t>, size_t> edgeToFaceMap;
    MeshSeparator::buildEdgeToFaceMap(m_remeshedPolygons, edgeToFaceMap);
    std::unordered_map<size_t, size_t> vertexOpenBoundaryCountMap;
    for (const auto& it : edgeToFaceMap) {
        if (edgeToFaceMap.end() != edgeToFaceMap.find({ it.first.second, it.first.first }))
            continue;
        vertexOpenBoundaryCountMap[it.first.first]++;
        vertexOpenBoundaryCountMap[it.first.second]++;
    }
    std::vector<std::vector<size_t>> manifoldFaces;
    for (const auto& it : m_remeshedPolygons) {
        bool isNonManifold = false;
        for (size_t i = 0; i < it.size(); ++i) {
            auto findCount = vertexOpenBoundaryCountMap.find(it[i]);
            if (findCount == vertexOpenBoundaryCountMap.end())
                continue;
            if (findCount->second > 2) {
                isNonManifold = true;
                break;
            }
        }
        if (isNonManifold) {
            changed = true;
            continue;
        }
        manifoldFaces.push_back(it);
    }
    m_remeshedPolygons = manifoldFaces;
    return changed;
}

}
