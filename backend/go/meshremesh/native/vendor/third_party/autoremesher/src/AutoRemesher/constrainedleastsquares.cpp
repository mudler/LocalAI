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
#include <AutoRemesher/ConstrainedLeastSquares>

#include <Eigen/Sparse>
#include <Eigen/SparseCholesky>
#include <Eigen/SparseLU>
#include <Eigen/SparseQR>
#if AUTO_REMESHER_USE_ACCELERATE
#include <Eigen/AccelerateSupport>
#endif
#include <algorithm>
#include <cmath>
#include <limits>

namespace AutoRemesher {
namespace {
    const size_t noIndex = std::numeric_limits<size_t>::max();
    const double ridge = 1e-10;
}

struct ConstrainedLeastSquares::Cache {
    Eigen::SparseMatrix<double> scaledMatrix;
    Eigen::SparseMatrix<double> normalMatrix;
    Eigen::VectorXd rowShift;
    Eigen::VectorXd rowWeightRoot;
    Eigen::SimplicialLDLT<Eigen::SparseMatrix<double>> solver;
#if AUTO_REMESHER_USE_ACCELERATE
    Eigen::AccelerateLLT<Eigen::SparseMatrix<double>, Eigen::Lower> acceleratedSolver;
    bool useAccelerated = true;
#endif
    bool patternAnalyzed = false;
    bool factorized = false;

    bool factorize()
    {
#if AUTO_REMESHER_USE_ACCELERATE
        if (useAccelerated) {
            if (!patternAnalyzed)
                acceleratedSolver.analyzePattern(normalMatrix);
            acceleratedSolver.factorize(normalMatrix);
            if (acceleratedSolver.info() == Eigen::Success) {
                patternAnalyzed = true;
                return true;
            }
            useAccelerated = false;
            patternAnalyzed = false;
        }
#endif
        if (!patternAnalyzed)
            solver.analyzePattern(normalMatrix);
        solver.factorize(normalMatrix);
        if (solver.info() != Eigen::Success)
            return false;
        patternAnalyzed = true;
        return true;
    }

    Eigen::VectorXd solve(const Eigen::VectorXd& rightHandSide, bool* succeeded)
    {
#if AUTO_REMESHER_USE_ACCELERATE
        if (useAccelerated) {
            const Eigen::VectorXd solution = acceleratedSolver.solve(rightHandSide);
            *succeeded = acceleratedSolver.info() == Eigen::Success && solution.allFinite();
            return solution;
        }
#endif
        const Eigen::VectorXd solution = solver.solve(rightHandSide);
        *succeeded = solver.info() == Eigen::Success && solution.allFinite();
        return solution;
    }
};

ConstrainedLeastSquares::ConstrainedLeastSquares(size_t variableCount)
    : m_variableCount(variableCount)
    , m_cache(new Cache)
{
}

ConstrainedLeastSquares::~ConstrainedLeastSquares() = default;

size_t ConstrainedLeastSquares::addEnergy(const std::vector<std::pair<size_t, double>>& coefficients,
    double rightHandSide, double weight)
{
    if (coefficients.empty() || weight <= 0.0)
        return noIndex;
    m_energyEquations.push_back({ coefficients, rightHandSide, weight });
    m_matrixDirty = true;
    return m_energyEquations.size() - 1;
}

void ConstrainedLeastSquares::setEnergyRightHandSide(size_t energyIndex, double rightHandSide)
{
    if (energyIndex >= m_energyEquations.size())
        return;
    m_energyEquations[energyIndex].rightHandSide = rightHandSide;
}

void ConstrainedLeastSquares::addConstraint(const std::vector<std::pair<size_t, double>>& coefficients,
    double rightHandSide)
{
    if (coefficients.empty())
        return;
    m_constraintEquations.push_back({ coefficients, rightHandSide, 1.0 });
    m_matrixDirty = true;
}

void ConstrainedLeastSquares::clearConstraints()
{
    if (m_constraintEquations.empty())
        return;
    m_constraintEquations.clear();
    m_matrixDirty = true;
}

ConstrainedLeastSquares::Substitution ConstrainedLeastSquares::resolve(size_t variable)
{
    double scale = 1.0, offset = 0.0;
    size_t cursor = variable;
    while (!m_substitutions[cursor].fixed && m_substitutions[cursor].root != cursor) {
        const Substitution& link = m_substitutions[cursor];
        offset += scale * link.offset;
        scale *= link.scale;
        cursor = link.root;
    }
    if (m_substitutions[cursor].fixed)
        m_substitutions[variable] = { variable, 1.0, offset + scale * m_substitutions[cursor].offset, true };
    else
        m_substitutions[variable] = { cursor, scale, offset, false };
    return m_substitutions[variable];
}

bool ConstrainedLeastSquares::buildSubstitutions()
{
    m_substitutions.resize(m_variableCount);
    for (size_t i = 0; i < m_variableCount; ++i)
        m_substitutions[i] = { i, 1.0, 0.0, false };

    std::vector<std::pair<size_t, double>> rootCoefficients;
    for (const LinearEquation& constraint : m_constraintEquations) {
        double rightHandSide = constraint.rightHandSide;
        rootCoefficients.clear();
        for (const auto& coefficient : constraint.coefficients) {
            if (coefficient.first >= m_variableCount)
                return false;
            const Substitution substitution = resolve(coefficient.first);
            if (substitution.fixed) {
                rightHandSide -= coefficient.second * substitution.offset;
                continue;
            }
            rightHandSide -= coefficient.second * substitution.offset;
            const double scaled = coefficient.second * substitution.scale;
            bool merged = false;
            for (auto& existing : rootCoefficients) {
                if (existing.first == substitution.root) {
                    existing.second += scaled;
                    merged = true;
                    break;
                }
            }
            if (!merged)
                rootCoefficients.push_back({ substitution.root, scaled });
        }
        rootCoefficients.erase(std::remove_if(rootCoefficients.begin(), rootCoefficients.end(),
                                   [](const std::pair<size_t, double>& c) { return std::fabs(c.second) < 1e-12; }),
            rootCoefficients.end());
        if (rootCoefficients.empty()) {
            if (std::fabs(rightHandSide) > 1e-9 * (1.0 + std::fabs(constraint.rightHandSide)))
                return false;
            continue;
        }
        if (1 == rootCoefficients.size()) {
            const size_t root = rootCoefficients[0].first;
            m_substitutions[root] = { root, 1.0, rightHandSide / rootCoefficients[0].second, true };
        } else if (2 == rootCoefficients.size()) {
            const size_t a = rootCoefficients[0].first, b = rootCoefficients[1].first;
            m_substitutions[a] = { b, -rootCoefficients[1].second / rootCoefficients[0].second,
                rightHandSide / rootCoefficients[0].second, false };
        } else {
            return false;
        }
    }

    m_freeIndexOfRoot.assign(m_variableCount, noIndex);
    m_freeCount = 0;
    for (size_t i = 0; i < m_variableCount; ++i) {
        const Substitution substitution = resolve(i);
        if (substitution.fixed)
            continue;
        if (noIndex == m_freeIndexOfRoot[substitution.root])
            m_freeIndexOfRoot[substitution.root] = m_freeCount++;
    }
    return true;
}

bool ConstrainedLeastSquares::buildReducedSystem()
{
    const Eigen::Index rowCount = static_cast<Eigen::Index>(m_energyEquations.size());
    const Eigen::Index columnCount = static_cast<Eigen::Index>(m_freeCount);
    Cache& cache = *m_cache;

    std::vector<Eigen::Triplet<double>> entries;
    size_t entryCount = 0;
    for (const LinearEquation& equation : m_energyEquations)
        entryCount += equation.coefficients.size();
    entries.reserve(entryCount);

    cache.rowShift = Eigen::VectorXd::Zero(rowCount);
    cache.rowWeightRoot = Eigen::VectorXd::Zero(rowCount);
    for (Eigen::Index row = 0; row < rowCount; ++row) {
        const LinearEquation& equation = m_energyEquations[static_cast<size_t>(row)];
        const double weightRoot = std::sqrt(equation.weight);
        cache.rowWeightRoot[row] = weightRoot;
        double shift = 0.0;
        for (const auto& coefficient : equation.coefficients) {
            if (coefficient.first >= m_variableCount)
                return false;
            const Substitution& substitution = m_substitutions[coefficient.first];
            if (substitution.fixed) {
                shift += coefficient.second * substitution.offset;
                continue;
            }
            shift += coefficient.second * substitution.offset;
            const size_t column = m_freeIndexOfRoot[substitution.root];
            if (noIndex == column)
                continue;
            entries.emplace_back(row, static_cast<Eigen::Index>(column),
                weightRoot * coefficient.second * substitution.scale);
        }
        cache.rowShift[row] = shift;
    }

    cache.scaledMatrix.resize(rowCount, columnCount);
    cache.scaledMatrix.setFromTriplets(entries.begin(), entries.end());

    cache.normalMatrix = (cache.scaledMatrix.transpose() * cache.scaledMatrix).pruned();
    Eigen::SparseMatrix<double> regularizer(columnCount, columnCount);
    regularizer.setIdentity();
    cache.normalMatrix += ridge * regularizer;
    cache.normalMatrix.makeCompressed();
    return true;
}

bool ConstrainedLeastSquares::solveReduced(std::vector<double>* solution)
{
    Cache& cache = *m_cache;
    if (0 == m_freeCount) {
        solution->assign(m_variableCount, 0.0);
        for (size_t i = 0; i < m_variableCount; ++i)
            (*solution)[i] = m_substitutions[i].offset;
        return true;
    }

    if (m_matrixDirty) {
        if (!buildReducedSystem())
            return false;
        if (!cache.factorize())
            return false;
        cache.factorized = true;
        m_matrixDirty = false;
    }
    if (!cache.factorized)
        return false;

    Eigen::VectorXd scaledRightHandSide(static_cast<Eigen::Index>(m_energyEquations.size()));
    for (Eigen::Index row = 0; row < scaledRightHandSide.size(); ++row) {
        scaledRightHandSide[row] = cache.rowWeightRoot[row]
            * (m_energyEquations[static_cast<size_t>(row)].rightHandSide - cache.rowShift[row]);
    }
    bool solveSucceeded = false;
    const Eigen::VectorXd reducedSolution = cache.solve(cache.scaledMatrix.transpose() * scaledRightHandSide,
        &solveSucceeded);
    if (!solveSucceeded)
        return false;

    solution->resize(m_variableCount);
    for (size_t i = 0; i < m_variableCount; ++i) {
        const Substitution& substitution = m_substitutions[i];
        if (substitution.fixed) {
            (*solution)[i] = substitution.offset;
            continue;
        }
        const size_t column = m_freeIndexOfRoot[substitution.root];
        (*solution)[i] = noIndex == column
            ? substitution.offset
            : substitution.offset + substitution.scale * reducedSolution[static_cast<Eigen::Index>(column)];
    }
    return true;
}

bool ConstrainedLeastSquares::solve(std::vector<double>* solution)
{
    if (0 == m_variableCount)
        return false;

    if (m_matrixDirty) {
        m_cache->patternAnalyzed = false;
        m_cache->factorized = false;
        if (!buildSubstitutions())
            return solveWithLagrangeMultipliers(solution);
    }
    return solveReduced(solution);
}

bool ConstrainedLeastSquares::solveWithLagrangeMultipliers(std::vector<double>* solution) const
{
    const Eigen::Index variableCount = static_cast<Eigen::Index>(m_variableCount);
    const Eigen::Index originalConstraintCount = static_cast<Eigen::Index>(m_constraintEquations.size());

    Eigen::SparseMatrix<double> constraintMatrix(originalConstraintCount, variableCount);
    std::vector<Eigen::Triplet<double>> constraintEntries;
    for (Eigen::Index constraintIndex = 0; constraintIndex < originalConstraintCount; ++constraintIndex) {
        for (const auto& coefficient : m_constraintEquations[static_cast<size_t>(constraintIndex)].coefficients) {
            if (coefficient.first >= m_variableCount)
                return false;
            constraintEntries.emplace_back(constraintIndex, static_cast<Eigen::Index>(coefficient.first), coefficient.second);
        }
    }
    constraintMatrix.setFromTriplets(constraintEntries.begin(), constraintEntries.end());
    Eigen::SparseQR<Eigen::SparseMatrix<double>, Eigen::COLAMDOrdering<int>> constraintQR;
    constraintQR.compute(constraintMatrix.transpose());
    if (constraintQR.info() != Eigen::Success)
        return false;
    const Eigen::Index independentConstraintCount = constraintQR.rank();
    std::vector<Eigen::Index> selectedConstraints;
    selectedConstraints.reserve(static_cast<size_t>(independentConstraintCount));
    const auto permutation = constraintQR.colsPermutation().indices();
    for (Eigen::Index i = 0; i < independentConstraintCount; ++i)
        selectedConstraints.push_back(permutation[i]);

    Eigen::SparseMatrix<double> normal(variableCount + independentConstraintCount, variableCount + independentConstraintCount);
    std::vector<Eigen::Triplet<double>> entries;
    Eigen::VectorXd rightHandSide = Eigen::VectorXd::Zero(variableCount + independentConstraintCount);
    for (const LinearEquation& equation : m_energyEquations) {
        for (const auto& coefficient : equation.coefficients) {
            if (coefficient.first >= m_variableCount)
                return false;
            rightHandSide[static_cast<Eigen::Index>(coefficient.first)] += equation.weight * coefficient.second * equation.rightHandSide;
            for (const auto& otherCoefficient : equation.coefficients) {
                if (otherCoefficient.first >= m_variableCount)
                    return false;
                entries.emplace_back(static_cast<Eigen::Index>(coefficient.first), static_cast<Eigen::Index>(otherCoefficient.first),
                    equation.weight * coefficient.second * otherCoefficient.second);
            }
        }
    }
    for (Eigen::Index constraintIndex = 0; constraintIndex < independentConstraintCount; ++constraintIndex) {
        const LinearEquation& equation = m_constraintEquations[static_cast<size_t>(selectedConstraints[constraintIndex])];
        rightHandSide[variableCount + constraintIndex] = equation.rightHandSide;
        for (const auto& coefficient : equation.coefficients) {
            entries.emplace_back(static_cast<Eigen::Index>(coefficient.first), variableCount + constraintIndex, coefficient.second);
            entries.emplace_back(variableCount + constraintIndex, static_cast<Eigen::Index>(coefficient.first), coefficient.second);
        }
    }
    for (Eigen::Index i = 0; i < variableCount; ++i)
        entries.emplace_back(i, i, ridge);
    normal.setFromTriplets(entries.begin(), entries.end());
    Eigen::SparseLU<Eigen::SparseMatrix<double>> solver;
    solver.compute(normal);
    if (solver.info() != Eigen::Success)
        return false;
    const Eigen::VectorXd solutionVector = solver.solve(rightHandSide);
    if (solver.info() != Eigen::Success || !solutionVector.allFinite())
        return false;
    solution->resize(m_variableCount);
    for (size_t i = 0; i < m_variableCount; ++i)
        (*solution)[i] = solutionVector[static_cast<Eigen::Index>(i)];
    return true;
}

}
