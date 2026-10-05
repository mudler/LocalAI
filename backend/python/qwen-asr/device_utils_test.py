import unittest

from device_utils import device_map_for, select_device


class Availability:
    def __init__(self, available):
        self._available = available

    def is_available(self):
        return self._available


class TorchStub:
    def __init__(self, *, cuda=False, mps=False, xpu=False):
        self.cuda = Availability(cuda)
        self.backends = type("Backends", (), {"mps": Availability(mps)})()
        self.xpu = Availability(xpu)


class SelectDeviceTest(unittest.TestCase):
    def test_preserves_cuda_selection(self):
        torch_module = TorchStub(cuda=True)

        self.assertEqual(select_device(torch_module), "cuda")

    def test_preserves_mps_selection(self):
        torch_module = TorchStub(mps=True)

        self.assertEqual(select_device(torch_module), "mps")

    def test_selects_xpu_when_intel_gpu_is_available(self):
        torch_module = TorchStub(xpu=True)

        self.assertEqual(select_device(torch_module), "xpu")

    def test_falls_back_to_cpu(self):
        torch_module = TorchStub()

        self.assertEqual(select_device(torch_module), "cpu")


class DeviceMapTest(unittest.TestCase):
    def test_preserves_cuda_model_placement(self):
        self.assertEqual(device_map_for("cuda"), "cuda:0")

    def test_preserves_mps_model_placement(self):
        self.assertIsNone(device_map_for("mps"))

    def test_places_the_model_on_the_first_xpu(self):
        self.assertEqual(device_map_for("xpu"), "xpu:0")

    def test_preserves_cpu_model_placement(self):
        self.assertEqual(device_map_for("cpu"), "cpu")


if __name__ == "__main__":
    unittest.main()
