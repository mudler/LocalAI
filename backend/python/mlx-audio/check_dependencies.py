#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Fail packaging when the private Darwin serving environment drifts."""

import importlib.metadata
import os
import sys

sys.path.insert(0, os.path.dirname(__file__))

from runtime import BackendFailure, ensure_torch_free


EXPECTED = {
    "grpcio": "1.78.0",
    "mlx-audio": "0.4.7",
}


def verify_serving_environment(distributions=None, version_getter=None):
    ensure_torch_free(distributions)
    get_version = version_getter or importlib.metadata.version
    for distribution, expected in EXPECTED.items():
        try:
            actual = get_version(distribution)
        except importlib.metadata.PackageNotFoundError as err:
            raise BackendFailure(
                "FAILED_PRECONDITION", f"required serving dependency is missing: {distribution}"
            ) from err
        if actual != expected:
            raise BackendFailure(
                "FAILED_PRECONDITION",
                f"serving dependency drift: {distribution}=={actual}, expected {expected}",
            )


if __name__ == "__main__":
    try:
        verify_serving_environment()
    except BackendFailure as failure:
        print(failure.message, file=sys.stderr)
        raise SystemExit(1)
