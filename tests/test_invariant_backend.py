import pytest
import sys
import os
from unittest.mock import MagicMock, patch

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "backend/python/trl"))

from backend import TRLBackend

@pytest.mark.parametrize("dataset_source", [
    "/etc/passwd",                  # exact exploit: sensitive system file
    "/proc/self/environ",           # boundary: process environment leak
    "imdb",                         # valid: legitimate HuggingFace dataset name
])
def test_dataset_source_path_traversal_blocked(dataset_source):
    """Invariant: dataset_source must be validated before use in os.path.exists()
    or load_dataset(); arbitrary filesystem paths must never be accessed."""

    backend = TRLBackend()

    request = MagicMock()
    request.dataset_source = dataset_source
    request.dataset_split = "train"
    request.model_name = "sshleifer/tiny-gpt2"
    request.output_dir = "/tmp/test_output"

    sensitive_paths = ["/etc/passwd", "/proc/self/environ", "/etc/shadow"]

    with patch("os.path.exists") as mock_exists, \
         patch("backend.load_dataset") as mock_load:

        mock_exists.return_value = False
        mock_load.side_effect = Exception("load_dataset blocked in test")

        try:
            backend._do_training(request)
        except Exception:
            pass

        # Assert: sensitive filesystem paths must never be passed to os.path.exists
        for call_args in mock_exists.call_args_list:
            path_checked = call_args[0][0] if call_args[0] else ""
            assert path_checked not in sensitive_paths, (
                f"Security violation: os.path.exists() called with sensitive path '{path_checked}'"
            )

        # Assert: sensitive filesystem paths must never be passed to load_dataset
        for call_args in mock_load.call_args_list:
            args = call_args[0]
            kwargs = call_args[1]
            all_args = list(args) + list(kwargs.values())
            for arg in all_args:
                if isinstance(arg, str):
                    assert arg not in sensitive_paths, (
                        f"Security violation: load_dataset() called with sensitive path '{arg}'"
                    )