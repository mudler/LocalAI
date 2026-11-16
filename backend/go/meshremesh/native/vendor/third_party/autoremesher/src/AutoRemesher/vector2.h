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
#ifndef AUTO_REMESHER_VECTOR2_H
#define AUTO_REMESHER_VECTOR2_H
#include <AutoRemesher/Double>
#include <Eigen/Dense>

namespace AutoRemesher {

class Vector2;

inline Vector2 operator*(double number, const Vector2& v);
inline Vector2 operator*(const Vector2& v, double number);
inline Vector2 operator+(const Vector2& a, const Vector2& b);
inline Vector2 operator-(const Vector2& a, const Vector2& b);

class Vector2 {
public:
    inline Vector2()
        : m_data { 0.0, 0.0 }
    {
    }

    inline Vector2(double x, double y)
        : m_data { x, y }
    {
    }

    inline double& operator[](size_t index)
    {
        return m_data[index];
    }

    inline const double& operator[](size_t index) const
    {
        return m_data[index];
    }

    inline const double& x() const
    {
        return m_data[0];
    }

    inline const double& y() const
    {
        return m_data[1];
    }

    inline void setX(double x)
    {
        m_data[0] = x;
    }

    inline void setY(double y)
    {
        m_data[1] = y;
    }

    inline const double* data() const
    {
        return &m_data[0];
    }

    inline void setData(double x, double y)
    {
        m_data[0] = x;
        m_data[1] = y;
    }

    inline double lengthSquared() const
    {
        return x() * x() + y() * y();
    }

    inline double length() const
    {
        double length2 = x() * x() + y() * y();
        return std::sqrt(length2);
    }

    inline Vector2 normalized() const
    {
        double length2 = x() * x() + y() * y();

        double length = std::sqrt(length2);
        if (Double::isZero(length))
            return Vector2();

        return Vector2(x() / length, y() / length);
    }

    inline void normalize()
    {
        double length2 = x() * x() + y() * y();

        double length = std::sqrt(length2);
        if (Double::isZero(length))
            return;

        m_data[0] = x() / length;
        m_data[1] = y() / length;
    }

    inline bool isInCircle(const Vector2& a, const Vector2& b, const Vector2& c) const
    {
        Eigen::Matrix4d m;
        m << a.x(), a.y(), a.x() * a.x() + a.y() * a.y(), 1,
            b.x(), b.y(), b.x() * b.x() + b.y() * b.y(), 1,
            c.x(), c.y(), c.x() * c.x() + c.y() * c.y(), 1,
            x(), y(), x() * x() + y() * y(), 1;
        return m.determinant() > 0;
    }

    inline bool isOnLeft(const Vector2& a, const Vector2& b) const
    {
        return ((b.x() - a.x()) * (y() - a.y()) - (b.y() - a.y()) * (x() - a.x())) > 0;
    }

    inline static double dotProduct(const Vector2& a, const Vector2& b)
    {
        return a.x() * b.x() + a.y() * b.y();
    }

    inline static Vector2 barycentricCoordinates(const Vector2& a, const Vector2& b, const Vector2& c, const Vector2& point)
    {
        auto v0 = c - a;
        auto v1 = b - a;
        auto v2 = point - a;

        auto dot00 = dotProduct(v0, v0);
        auto dot01 = dotProduct(v0, v1);
        auto dot02 = dotProduct(v0, v2);
        auto dot11 = dotProduct(v1, v1);
        auto dot12 = dotProduct(v1, v2);

        auto invDenom = 1.0 / (dot00 * dot11 - dot01 * dot01);
        auto alpha = (dot11 * dot02 - dot01 * dot12) * invDenom;
        auto beta = (dot00 * dot12 - dot01 * dot02) * invDenom;

        return Vector2(alpha, beta);
    }

    inline static bool isInTriangle(const Vector2& a, const Vector2& b, const Vector2& c, const Vector2& point)
    {
        auto alphaAndBeta = barycentricCoordinates(a, b, c, point);
        if (alphaAndBeta[0] < 0)
            return false;
        if (alphaAndBeta[1] < 0)
            return false;
        if ((1.0 - (alphaAndBeta[0] + alphaAndBeta[1])) < 0)
            return false;
        return true;
    }

private:
    double m_data[2] = { 0.0 };
};

inline Vector2 operator*(double number, const Vector2& v)
{
    return Vector2(number * v.x(), number * v.y());
}

inline Vector2 operator*(const Vector2& v, double number)
{
    return Vector2(number * v.x(), number * v.y());
}

inline Vector2 operator+(const Vector2& a, const Vector2& b)
{
    return Vector2(a.x() + b.x(), a.y() + b.y());
}

inline Vector2 operator-(const Vector2& a, const Vector2& b)
{
    return Vector2(a.x() - b.x(), a.y() - b.y());
}

inline std::string to_string(const Vector2& v)
{
    return std::to_string(v.x()) + "," + std::to_string(v.y());
}

inline std::ostream& operator<<(std::ostream& os, const Vector2& v)
{
    os << v.x() << ',' << v.y();
    return os;
}

inline bool operator==(const Vector2& a, const Vector2& b)
{
    return Double::isEqual(a.x(), b.x()) && Double::isEqual(a.y(), b.y());
}

inline bool operator!=(const Vector2& a, const Vector2& b)
{
    return !Double::isEqual(a.x(), b.x()) || !Double::isEqual(a.y(), b.y());
}

}

#endif
