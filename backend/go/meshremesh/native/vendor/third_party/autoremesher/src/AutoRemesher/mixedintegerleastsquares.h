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
#ifndef AUTO_REMESHER_MIXED_INTEGER_LEAST_SQUARES_H
#define AUTO_REMESHER_MIXED_INTEGER_LEAST_SQUARES_H

#include <cstddef>
#include <memory>
#include <utility>
#include <vector>

namespace AutoRemesher {

class ConstrainedLeastSquares;

class MixedIntegerLeastSquares {
public:
    explicit MixedIntegerLeastSquares(size_t variableCount);
    ~MixedIntegerLeastSquares();
    void setVariablePeriod(size_t variable, int period);
    void addConstraint(const std::vector<std::pair<size_t, double>>& coefficients);
    void addConstraint(size_t firstVariable, double firstCoefficient);
    void addConstraint(size_t firstVariable, double firstCoefficient,
        size_t secondVariable, double secondCoefficient);
    void addConstraint(size_t firstVariable, double firstCoefficient,
        size_t secondVariable, double secondCoefficient,
        size_t thirdVariable, double thirdCoefficient);
    void finalizeConstraints();

    void beginConstraint();
    void addConstraintCoefficient(size_t variable, double coefficient);
    bool endConstraint();
    void endPass(size_t pass);

    void clearEnergy();
    void addEnergy(const std::vector<std::pair<size_t, double>>& coefficients,
        double rhs, double weight = 1.0);
    void addEnergy(size_t firstVariable, double firstCoefficient,
        size_t secondVariable, double secondCoefficient, double rhs, double weight = 1.0);

    bool solveIteration();
    bool converged() const;
    double value(size_t originalVariable) const;
    size_t kernelSize() const;
    size_t integerKernelVariableCount() const;
    size_t originalIntegerVariableCount() const;

private:
    struct Coeff {
        size_t index;
        double a;
    };
    struct Row {
        std::vector<Coeff> coefficients;
        double rhs;
        double weight;
    };
    struct SparseMatrix {
        std::vector<std::vector<Coeff>> rows;
    };

    static void normalize(std::vector<Coeff>* row);
    static void add(std::vector<Coeff>* row, size_t index, double a);
    void axpy(std::vector<Coeff>* row, const std::vector<Coeff>& source, double factor);
    std::vector<Coeff> multiply(const std::vector<Coeff>& row,
        const SparseMatrix& matrix) const;
    std::vector<Coeff> line(size_t originalVariable) const;
    const std::vector<Coeff>& kernelLine(size_t originalVariable) const;
    void buildKernel();
    std::vector<Coeff> throughM0(const std::vector<Coeff>& row) const;
    std::vector<Coeff> throughM1(const std::vector<Coeff>& row) const;
    bool processConstraint(const std::vector<Coeff>& row);
    void recordConstraint();
    const std::vector<Coeff>& recordedConstraint(size_t index) const;
    void finalizeM0();
    void finalizeM1();
    void finalizeM2();
    void applyRemoveColumn(const std::vector<Coeff>& row, size_t remove);

    size_t m_size;
    size_t m_pass;
    size_t m_kernelSize;
    std::vector<size_t> m_m0;
    std::vector<Coeff> m_m1;
    SparseMatrix m_m2, m_m2t;
    std::vector<int> m_period;
    std::vector<int> m_initialPeriod;
    std::vector<Coeff> m_pendingConstraint;
    std::vector<Coeff> m_constraintData;
    std::vector<std::pair<size_t, size_t>> m_constraintRanges;
    std::vector<Coeff> m_constraintScratch;
    std::vector<size_t> m_singleTermConstraints;
    std::vector<size_t> m_multiTermConstraints;
    mutable std::vector<Coeff> m_rowScratch;
    std::vector<Row> m_energy;
    std::vector<Row> m_reducedEnergy;
    std::vector<double> m_values;
    std::vector<bool> m_fixed;

    SparseMatrix m_kernel;
    bool m_kernelBuilt = false;

    mutable std::vector<double> m_scatter;
    mutable std::vector<size_t> m_touched;
    mutable std::vector<Coeff> m_merged;

    std::unique_ptr<ConstrainedLeastSquares> m_system;
};
}
#endif
