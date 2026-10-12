#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Require complete passing Ginkgo evidence, even under python -O."""
import json
import sys


def require(condition, message):
    if not condition:
        raise ValueError(message)


try:
    with open(sys.argv[1], encoding="utf-8") as stream:
        suites = json.load(stream)
    require(isinstance(suites, list) and suites, "no suites")
    count = 0
    for suite in suites:
        require(suite["SuiteSucceeded"] is True, "suite failed")
        specs = suite["SpecReports"]
        require(isinstance(specs, list) and specs, "no specs")
        require(all(s["State"] == "passed" for s in specs), "non-passing/skipped spec")
        executed = sum(s.get("LeafNodeType") == "It" for s in specs)
        require(executed > 0, "no executed It specs")
        count += executed
    print(f"Verified {len(suites)} suite(s), {count} passing specs; no skips")
except (OSError, ValueError, KeyError, TypeError, AttributeError, IndexError) as error:
    print(f"Invalid baseline report: {error}", file=sys.stderr)
    sys.exit(1)
