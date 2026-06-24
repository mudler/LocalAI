def select_device(torch_module):
    mps = getattr(getattr(torch_module, "backends", None), "mps", None)
    if mps is not None and mps.is_available():
        return "mps"
    if torch_module.cuda.is_available():
        return "cuda"
    xpu = getattr(torch_module, "xpu", None)
    if xpu is not None and xpu.is_available():
        return "xpu"
    return "cpu"


def device_map_for(device):
    if device == "mps":
        return None
    if device in ("cuda", "xpu"):
        return f"{device}:0"
    return "cpu"
