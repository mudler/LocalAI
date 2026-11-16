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
 
#include "axisalignedboundingboxtree.h"

const size_t AxisAlignedBoudingBoxTree::m_leafMaxNodeSize = 20;

AxisAlignedBoudingBoxTree::AxisAlignedBoudingBoxTree(const std::vector<AxisAlignedBoudingBox> *boxes,
        const std::vector<size_t> &boxIndices,
        const AxisAlignedBoudingBox &outterBox)
{
    m_boxes = boxes;
    
    m_root = new Node;
    m_root->boundingBox = outterBox;
    m_root->boxIndices = boxIndices;
    
    if (!boxIndices.empty()) {
        for (const auto &boxIndex: boxIndices) {
            m_root->center += (*boxes)[boxIndex].center();
        }
        m_root->center /= (float)boxIndices.size();
    }
    
    splitNode(m_root);
}

const AxisAlignedBoudingBoxTree::Node *AxisAlignedBoudingBoxTree::root() const
{
    return m_root;
}

const std::vector<AxisAlignedBoudingBox> *AxisAlignedBoudingBoxTree::boxes() const
{
    return m_boxes;
}

AxisAlignedBoudingBoxTree::~AxisAlignedBoudingBoxTree()
{
    deleteNode(m_root);
}

void AxisAlignedBoudingBoxTree::deleteNode(Node *node)
{
    if (nullptr == node)
        return;
    deleteNode(node->left);
    deleteNode(node->right);
    delete node;
}

void AxisAlignedBoudingBoxTree::splitNode(Node *node)
{
    const auto &boxIndices = node->boxIndices;
    if (boxIndices.size() <= m_leafMaxNodeSize)
        return;
    const auto &splitBox = node->boundingBox;
    const Vector3 &lower = splitBox.lowerBound();
    const Vector3 &upper = splitBox.upperBound();
    for (size_t i = 0; i < 3; ++i)
        m_spans[i] = {i, upper[i] - lower[i]};
    size_t longestAxis = std::max_element(m_spans.begin(), m_spans.end(), [](const std::pair<size_t, float> &first,
            const std::pair<size_t, float> &second) {
        return first.second < second.second;
    })->first;
    auto splitPoint = node->center[longestAxis];
    node->left = new Node;
    node->right = new Node;
    m_boxIndicesOrderList.resize(boxIndices.size() + boxIndices.size() + 2);
    size_t leftOffset = boxIndices.size();
    size_t rightOffset = boxIndices.size() - 1;
    size_t leftCount = 0;
    size_t rightCount = 0;
    for (size_t i = 0; i < boxIndices.size(); ++i) {
        const auto &boxIndex = boxIndices[i];
        const AxisAlignedBoudingBox &box = (*m_boxes)[boxIndex];
        const auto &center = box.center()[longestAxis];
        if (center < splitPoint) {
            m_boxIndicesOrderList[--leftOffset] = boxIndex;
            ++leftCount;
        } else {
            m_boxIndicesOrderList[++rightOffset] = boxIndex;
            ++rightCount;
        }
    }
    
    if (0 == leftCount) {
        leftCount = rightCount / 2;
        rightCount -= leftCount;
        leftOffset = rightOffset - boxIndices.size() + 1;
    } else if (0 == rightCount) {
        rightCount = leftCount / 2;
        leftCount -= rightCount;
        rightOffset = leftOffset + boxIndices.size() - 1;
    }
    
    size_t middle = leftOffset + leftCount - 1;
    
    for (size_t i = leftOffset; i <= middle; ++i) {
        const auto &boxIndex = m_boxIndicesOrderList[i];
        const AxisAlignedBoudingBox &box = (*m_boxes)[boxIndex];
        node->left->boundingBox.update(box.lowerBound());
        node->left->boundingBox.update(box.upperBound());
        node->left->boxIndices.push_back(boxIndex);
        node->left->center += box.center();
    }
    
    for (size_t i = middle + 1; i <= rightOffset; ++i) {
        const auto &boxIndex = m_boxIndicesOrderList[i];
        const AxisAlignedBoudingBox &box = (*m_boxes)[boxIndex];
        node->right->boundingBox.update(box.lowerBound());
        node->right->boundingBox.update(box.upperBound());
        node->right->boxIndices.push_back(boxIndex);
        node->right->center += box.center();
    }

    node->left->center /= (float)node->left->boxIndices.size();
    splitNode(node->left);

    node->right->center /= (float)node->right->boxIndices.size();
    splitNode(node->right);
}
