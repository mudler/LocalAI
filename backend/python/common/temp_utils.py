import base64
import contextlib
import os
import tempfile


@contextlib.contextmanager
def materialize_base64(data, suffix=""):
    """Materialize base64 data for a path-only library and always remove it."""
    descriptor, path = tempfile.mkstemp(prefix="localai-media-", suffix=suffix)
    try:
        with os.fdopen(descriptor, "wb") as output:
            descriptor = None
            output.write(base64.b64decode(data))
        yield path
    finally:
        if descriptor is not None:
            os.close(descriptor)
        try:
            os.remove(path)
        except OSError:
            pass


@contextlib.contextmanager
def cleanup_paths():
    """Collect temporary paths and remove them on success or failure."""
    paths = []
    try:
        yield paths
    finally:
        for path in paths:
            try:
                os.remove(path)
            except OSError:
                pass
