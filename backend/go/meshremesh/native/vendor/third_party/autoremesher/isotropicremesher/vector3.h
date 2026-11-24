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
#ifndef VECTOR3_H
#define VECTOR3_H
#include <string>
#include <iostream>
#include <vector>
#include "double.h"
#include "vector2.h"

#ifndef M_PI
#define M_PI           3.14159265358979323846
#endif

class Vector3;

inline Vector3 operator*(double number, const Vector3 &v);
inline Vector3 operator*(const Vector3 &v, double number);
inline Vector3 operator+(const Vector3 &a, const Vector3 &b);
inline Vector3 operator-(const Vector3 &a, const Vector3 &b);
inline Vector3 operator-(const Vector3 &v);
inline Vector3 operator/(const Vector3 &v, double number);

class Vector3
{
public:
    inline Vector3() :
        m_data {0.0, 0.0, 0.0}
    {
    }
    
    inline Vector3(double x, double y, double z) :
        m_data {x, y, z}
    {
    }
    
    inline double &operator[](size_t index)
    {
        return m_data[index];
    }
    
    inline const double &operator[](size_t index) const
    {
        return m_data[index];
    }

    inline const double &x() const
    {
        return m_data[0];
    }
    
    inline const double &y() const
    {
        return m_data[1];
    }
    
    inline const double &z() const
    {
        return m_data[2];
    }
    
    inline void setX(double x)
    {
        m_data[0] = x;
    }
    
    inline void setY(double y)
    {
        m_data[1] = y;
    }
    
    inline void setZ(double z)
    {
        m_data[2] = z;
    }
    
    inline const double *constData() const
    {
        return &m_data[0];
    }
    
    inline void setData(double x, double y, double z)
    {
        m_data[0] = x;
        m_data[1] = y;
        m_data[2] = z;
    }
    
    inline double lengthSquared() const
    {
        return x() * x() + y() * y() + z() * z();
    }
    
    inline double length() const
    {
        double length2 = x() * x() + y() * y() + z() * z();
        return std::sqrt(length2);
    }
    
    inline Vector3 normalized() const
    {
        double length2 = x() * x() + y() * y() + z() * z();
    
        double length = std::sqrt(length2);
        if (Double::isZero(length))
            return Vector3();
        
        return Vector3(x() / length, y() / length, z() / length);
    }
    
    inline void normalize()
    {
        double length2 = x() * x() + y() * y() + z() * z();
    
        double length = std::sqrt(length2);
        if (Double::isZero(length))
            return;
        
        m_data[0] = x() / length;
        m_data[1] = y() / length;
        m_data[2] = z() / length;
    }
    
    inline static Vector3 crossProduct(const Vector3 &a, const Vector3 &b)
    {
        return Vector3(a.y() * b.z() - a.z() * b.y(),
            a.z() * b.x() - a.x() * b.z(),
            a.x() * b.y() - a.y() * b.x());
    }
    
    inline static double dotProduct(const Vector3 &a, const Vector3 &b)
    {
        return a.x() * b.x() + a.y() * b.y() + a.z() * b.z();
    }
    
    inline static Vector3 normal(const Vector3 &a, const Vector3 &b, const Vector3 &c)
    {
        double baX = b.x() - a.x();
        double baY = b.y() - a.y();
        double baZ = b.z() - a.z();
        
        double caX = c.x() - a.x();
        double caY = c.y() - a.y();
        double caZ = c.z() - a.z();
        
        double crossX = baY * caZ - baZ * caY;
        double crossY = baZ * caX - baX * caZ;
        double crossZ = baX * caY - baY * caX;
        
        double length2 = crossX * crossX + crossY * crossY + crossZ * crossZ;
        
        double length = std::sqrt(length2);
        if (Double::isZero(length))
            return Vector3();
        
        return Vector3(crossX / length, crossY / length, crossZ / length);
    }
    
    inline static double angle(const Vector3 &a, const Vector3 &b)
    {
        double dot = Vector3::dotProduct(a.normalized(), b.normalized());
        if (dot <= -1.0)
            return M_PI;
        else if (dot >= 1.0)
            return 0;
        else
            return std::acos(dot);
    }
    
    inline bool isZero() const
    {
        return Double::isZero(m_data[0]) &&
            Double::isZero(m_data[1]) &&
            Double::isZero(m_data[2]);
    }
    
    inline bool containsNan() const
    {
        return std::isnan(m_data[0]) ||
            std::isnan(m_data[1]) ||
            std::isnan(m_data[2]);
    }
    
    inline bool containsInf() const
    {
        return std::isinf(m_data[0]) ||
            std::isinf(m_data[1]) ||
            std::isinf(m_data[2]);
    }
    
    inline Vector3 &operator+=(const Vector3 &other)
    {
        m_data[0] += other.x();
        m_data[1] += other.y();
        m_data[2] += other.z();
        return *this;
    }
    
    inline Vector3 &operator*=(double number)
    {
        m_data[0] *= number;
        m_data[1] *= number;
        m_data[2] *= number;
        return *this;
    }
    
    inline Vector3 &operator/=(double number)
    {
        m_data[0] /= number;
        m_data[1] /= number;
        m_data[2] /= number;
        return *this;
    }
    
    inline static double area(const Vector3 &a, const Vector3 &b, const Vector3 &c)
    {
        auto ab = b - a;
        auto ac = c - a;
        return 0.5 * crossProduct(ab, ac).length();
    }
    
    inline static void project(const std::vector<Vector3> &pointsIn3d, std::vector<Vector2> *pointsIn2d,
        const Vector3 &normal, const Vector3 &axis, const Vector3 &origin=Vector3())
    {
        Vector3 perpendicularAxis = crossProduct(normal, axis);
        for (const auto &it: pointsIn3d) {
            Vector3 direction = it - origin;
            pointsIn2d->push_back({
                dotProduct(direction, axis),
                dotProduct(direction, perpendicularAxis)
            });
        }
    }
    
    inline static void project(const std::vector<Vector3> &pointsIn3d, std::vector<Vector3> *pointsIn2d,
        const Vector3 &normal, const Vector3 &axis, const Vector3 &origin=Vector3())
    {
        Vector3 perpendicularAxis = crossProduct(normal, axis);
        for (const auto &it: pointsIn3d) {
            Vector3 direction = it - origin;
            pointsIn2d->push_back({
                dotProduct(direction, axis),
                dotProduct(direction, perpendicularAxis),
                0.0
            });
        }
    }
    
    inline static Vector3 barycentricCoordinates(const Vector3 &a, const Vector3 &b, const Vector3 &c, const Vector3 &point)
    {
        auto invertedAreaOfAbc = 1.0 / area(a, b, c);
        auto areaOfPbc = area(point, b, c);
        auto areaOfPca = area(point, c, a);
        auto alpha = areaOfPbc * invertedAreaOfAbc;
        auto beta = areaOfPca * invertedAreaOfAbc;
        return Vector3(alpha, beta, 1.0 - alpha - beta);
    }
    
    inline static bool intersectSegmentAndPlane(const Vector3 &segmentPoint0, const Vector3 &segmentPoint1,
        const Vector3 &pointOnPlane, const Vector3 &planeNormal,
        Vector3 *intersection)
    {
        auto u = segmentPoint1 - segmentPoint0;
        auto w = segmentPoint0 - pointOnPlane;
        auto d = Vector3::dotProduct(planeNormal, u);
        auto n = Vector3::dotProduct(-planeNormal, w);
        if (std::abs(d) <= std::numeric_limits<double>::epsilon())
            return false;
        auto s = n / d;
        if (s < 0 || s > 1 || std::isnan(s) || std::isinf(s))
            return false;
        if (nullptr != intersection)
            *intersection = segmentPoint0 + s * u;
        return true;
    }

    inline static bool intersectSegmentAndTriangle(const Vector3 &segmentPoint0, const Vector3 &segmentPoint1,
        const std::vector<Vector3> &triangle,
        const Vector3 &triangleNormal,
        Vector3 *intersection)
    {
        Vector3 possibleIntersection;
        if (!intersectSegmentAndPlane(segmentPoint0, segmentPoint1,
                triangle[0], triangleNormal, &possibleIntersection)) {
            return false;
        }
        auto ray = (segmentPoint0 - segmentPoint1).normalized();
        std::vector<Vector3> normals;
        for (size_t i = 0; i < 3; ++i) {
            size_t j = (i + 1) % 3;
            normals.push_back(Vector3::normal(possibleIntersection, triangle[i], triangle[j]));
        }
        if (Vector3::dotProduct(normals[0], ray) <= 0)
            return false;
        if (Vector3::dotProduct(normals[0], normals[1]) <= 0)
            return false;
        if (Vector3::dotProduct(normals[0], normals[2]) <= 0)
            return false;
        if (nullptr != intersection)
            *intersection = possibleIntersection;
        return true;
    }
    
    inline static Vector3 projectPointOnLine(const Vector3 &point, const Vector3 &linePointA, const Vector3 &linePointB)
    {
        auto aToPoint = point - linePointA;
        auto aToB = linePointB - linePointA;
        return linePointA + Vector3::dotProduct(aToPoint, aToB) / Vector3::dotProduct(aToB, aToB) * aToB;
    }
    
private:
    double m_data[3] = {0.0};
};

inline Vector3 operator*(double number, const Vector3 &v)
{
    return Vector3(number * v.x(), number * v.y(), number * v.z());
}

inline Vector3 operator*(const Vector3 &v, double number)
{
    return Vector3(number * v.x(), number * v.y(), number * v.z());
}

inline Vector3 operator*(const Vector3 &a, const Vector3 &b)
{
    return Vector3(a.x() * b.x(), a.y() * b.y(), a.z() * b.z());
}

inline Vector3 operator/(const Vector3 &a, const Vector3 &b)
{
    return Vector3(a.x() / b.x(), a.y() / b.y(), a.z() / b.z());
}

inline Vector3 operator+(const Vector3 &a, const Vector3 &b)
{
    return Vector3(a.x() + b.x(), a.y() + b.y(), a.z() + b.z());
}

inline Vector3 operator-(const Vector3 &a, const Vector3 &b)
{
    return Vector3(a.x() - b.x(), a.y() - b.y(), a.z() - b.z());
}

inline Vector3 operator-(const Vector3 &v)
{
    return Vector3(-v.x(), -v.y(), -v.z());
}

inline Vector3 operator/(const Vector3 &v, double number)
{
    return Vector3(v.x() / number, v.y() / number, v.z() / number);
}

inline std::string to_string(const Vector3 &v)
{
    return std::to_string(v.x()) + "," + std::to_string(v.y()) + "," + std::to_string(v.z());
}

inline std::ostream &operator<<(std::ostream &os, const Vector3 &v)
{
    os << v.x() << ',' << v.y() << ',' << v.z();
    return os;
}

#endif

