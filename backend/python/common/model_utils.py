import os


def resolve_model_reference(request, default=""):
    model_file = (getattr(request, "ModelFile", "") or "").strip()
    if model_file and os.path.exists(model_file):
        return model_file, True
    model = (getattr(request, "Model", "") or "").strip()
    return model or default, False


def require_snapshot_file(model_ref, suffix):
    if os.path.isfile(model_ref) and model_ref.endswith(suffix):
        return model_ref
    if not os.path.isdir(model_ref):
        raise ValueError(f"model snapshot does not exist: {model_ref}")

    matches = []
    for root, directories, files in os.walk(model_ref):
        directories.sort()
        for name in sorted(files):
            if name.endswith(suffix):
                matches.append(os.path.join(root, name))
    if len(matches) != 1:
        raise ValueError(
            f"model snapshot must contain exactly one {suffix} file; found {len(matches)}"
        )
    return matches[0]
