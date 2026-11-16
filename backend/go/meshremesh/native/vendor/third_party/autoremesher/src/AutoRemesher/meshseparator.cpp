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
#include <AutoRemesher/MeshSeparator>
#include <queue>
#include <unordered_set>

namespace AutoRemesher {

void MeshSeparator::splitToIslands(const std::vector<std::vector<size_t>>& faces,
    std::vector<std::vector<std::vector<size_t>>>& islands)
{
    std::map<std::pair<size_t, size_t>, size_t> edgeToFaceMap;
    buildEdgeToFaceMap(faces, edgeToFaceMap);

    std::unordered_set<size_t> processedFaces;
    std::queue<size_t> waitFaces;
    for (size_t indexInGroup = 0; indexInGroup < faces.size(); ++indexInGroup) {
        if (processedFaces.find(indexInGroup) != processedFaces.end())
            continue;
        waitFaces.push(indexInGroup);
        std::vector<std::vector<size_t>> island;
        while (!waitFaces.empty()) {
            size_t index = waitFaces.front();
            waitFaces.pop();
            if (processedFaces.find(index) != processedFaces.end())
                continue;
            const auto& face = faces[index];
            for (size_t i = 0; i < face.size(); i++) {
                size_t j = (i + 1) % face.size();
                auto findOppositeFaceResult = edgeToFaceMap.find({ face[j], face[i] });
                if (findOppositeFaceResult == edgeToFaceMap.end())
                    continue;
                waitFaces.push(findOppositeFaceResult->second);
            }
            island.push_back(faces[index]);
            processedFaces.insert(index);
        }
        if (island.empty())
            continue;
        islands.push_back(island);
    }
}

void MeshSeparator::buildEdgeToFaceMap(const std::vector<std::vector<size_t>>& faces,
    std::map<std::pair<size_t, size_t>, size_t>& edgeToFaceMap)
{
    edgeToFaceMap.clear();
    for (size_t index = 0; index < faces.size(); ++index) {
        const auto& face = faces[index];
        for (size_t i = 0; i < face.size(); i++) {
            size_t j = (i + 1) % face.size();
            edgeToFaceMap[{ face[i], face[j] }] = index;
        }
    }
}

}
