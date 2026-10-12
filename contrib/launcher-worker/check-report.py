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
        stats = suite["PreRunStats"]
        total = stats["TotalSpecs"]
        scheduled = stats["SpecsThatWillRun"]
        require(type(total) is int and total > 0, "invalid TotalSpecs")
        require(type(scheduled) is int and scheduled > 0, "invalid SpecsThatWillRun")
        require(total == scheduled, "not all specs scheduled")
        specs = suite["SpecReports"]
        require(isinstance(specs, list) and specs, "no specs")
        require(all(s["State"] == "passed" for s in specs), "non-passing/skipped spec")
        executed = sum(s.get("LeafNodeType") == "It" for s in specs)
        require(executed == scheduled, "It report count does not match scheduled specs")
        count += executed
    print(f"Verified {len(suites)} suite(s), {count} passing specs; no skips")
except (OSError, ValueError, KeyError, TypeError, AttributeError, IndexError) as error:
    print(f"Invalid baseline report: {error}", file=sys.stderr)
    sys.exit(1)
