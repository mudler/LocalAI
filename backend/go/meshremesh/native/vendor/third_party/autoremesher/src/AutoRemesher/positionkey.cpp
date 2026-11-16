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
#include <AutoRemesher/PositionKey>

namespace AutoRemesher {

long PositionKey::m_toIntFactor = 100000;

PositionKey::PositionKey(const Vector3& v)
    : PositionKey(v.x(), v.y(), v.z())
{
}

PositionKey::PositionKey(double x, double y, double z)
{
    m_position.setX(x);
    m_position.setY(y);
    m_position.setZ(z);

    m_intX = (long)(x * m_toIntFactor);
    m_intY = (long)(y * m_toIntFactor);
    m_intZ = (long)(z * m_toIntFactor);
}

const Vector3& PositionKey::position() const
{
    return m_position;
}

bool PositionKey::operator<(const PositionKey& right) const
{
    if (m_intX < right.m_intX)
        return true;
    if (m_intX > right.m_intX)
        return false;
    if (m_intY < right.m_intY)
        return true;
    if (m_intY > right.m_intY)
        return false;
    if (m_intZ < right.m_intZ)
        return true;
    if (m_intZ > right.m_intZ)
        return false;
    return false;
}

bool PositionKey::operator==(const PositionKey& right) const
{
    return m_intX == right.m_intX && m_intY == right.m_intY && m_intZ == right.m_intZ;
}

}
