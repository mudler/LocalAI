import os


def resolve_model_reference(request, default=""):
    model_file = (getattr(request, "ModelFile", "") or "").strip()
    if model_file and os.path.exists(model_file):
        return model_file, True
    model = (getattr(request, "Model", "") or "").strip()
    return model or default, False
