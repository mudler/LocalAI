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
#ifndef AUTO_REMESHER_CONSTRAINED_LEAST_SQUARES_H
#define AUTO_REMESHER_CONSTRAINED_LEAST_SQUARES_H

#include <cstddef>
#include <memory>
#include <utility>
#include <vector>

namespace AutoRemesher {

class ConstrainedLeastSquares {
public:
    explicit ConstrainedLeastSquares(size_t variableCount);
    ~ConstrainedLeastSquares();

    size_t addEnergy(const std::vector<std::pair<size_t, double>>& coefficients,
        double rightHandSide, double weight = 1.0);
    void setEnergyRightHandSide(size_t energyIndex, double rightHandSide);
    void addConstraint(const std::vector<std::pair<size_t, double>>& coefficients,
        double rightHandSide = 0.0);
    void clearConstraints();

    bool solve(std::vector<double>* solution);

private:
    struct LinearEquation {
        std::vector<std::pair<size_t, double>> coefficients;
        double rightHandSide;
        double weight;
    };

    struct Substitution {
        size_t root;
        double scale;
        double offset;
        bool fixed;
    };

    Substitution resolve(size_t variable);
    bool buildSubstitutions();
    bool buildReducedSystem();
    bool solveReduced(std::vector<double>* solution);
    bool solveWithLagrangeMultipliers(std::vector<double>* solution) const;

    size_t m_variableCount;
    std::vector<LinearEquation> m_energyEquations;
    std::vector<LinearEquation> m_constraintEquations;

    bool m_matrixDirty = true;

    std::vector<Substitution> m_substitutions;
    std::vector<size_t> m_freeIndexOfRoot;
    size_t m_freeCount = 0;

    struct Cache;
    std::unique_ptr<Cache> m_cache;
};
}
#endif
