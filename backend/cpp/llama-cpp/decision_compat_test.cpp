// SPDX-License-Identifier: MIT
#include "decision_compat.h"
#include <cassert>
#include <vector>

struct legacy_decision {
    void fill_task(const int & state, int question, int & result) const {
        result = state + question;
    }
};
struct full_request_decision {
    const std::vector<int> * expected;
    void fill_task(const int & state, const std::vector<int> & questions,
                   int question, int & result) const {
        assert(&questions == expected); // no copy or singleton substitution
        assert(questions.size() == 2);
        result = state + question + questions[1];
    }
};
int main() {
    const std::vector<int> questions{3, 7};
    int result = 0;
    localai_fill_decision_task(legacy_decision{}, 2, questions, 3, result);
    assert(result == 5);
    localai_fill_decision_task(full_request_decision{&questions}, 2, questions, 3, result);
    assert(result == 12);
}
