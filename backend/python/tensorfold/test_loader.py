import types
import unittest

from tf_loader import LoadError, check_cuda_capability, load_app
from tf_options import LoadConfig


class FakeFamily:
    title = "Fake"


def make_tf(backend, *, detect_raises=None, serve_raises=None, capture_cuda=True):
    calls = []
    app = object()

    def detect(_dir):
        if detect_raises:
            raise detect_raises
        return FakeFamily()

    families = types.SimpleNamespace(detect=detect)

    class Args:
        model = "org/model"
        backend = "auto"

    parser = types.SimpleNamespace(parse_args=lambda argv: (calls.append(("parse", tuple(argv))), Args())[1])
    stacks = types.SimpleNamespace(start=lambda: calls.append("real-start"), arm=lambda: calls.append("real-arm"))
    http_module = types.SimpleNamespace(make_handler=lambda a: calls.append("real-handler"))
    cuda_module = types.SimpleNamespace(serve=lambda a, h, p: calls.append("real-serve"))

    def cmd_serve(args):
        calls.append("cmd_serve")
        stacks.start()          # must be the patched no-op
        stacks.arm()
        if serve_raises:
            raise serve_raises
        if backend == "mlx":
            http_module.make_handler(app)       # patched: captures and aborts
            calls.append("after-handler")       # must not run
        else:
            cuda_module.serve(app, "127.0.0.1", 8080)
        return 0

    cli = types.SimpleNamespace(
        build_parser=lambda: parser,
        cmd_serve=cmd_serve,
        _config_dir=lambda model: "/cfg",
        _backend=lambda choice, family: backend,
    )
    tf = types.SimpleNamespace(cli=cli, families=families, stacks=stacks,
                               http_module=http_module, cuda_server_module=lambda: cuda_module)
    return tf, calls, app


CFG = LoadConfig(model="org/model", argv=("serve", "org/model", "--no-update-check"))


class LoadAppTest(unittest.TestCase):
    def test_mlx_captures_the_app_when_the_handler_is_built(self):
        tf, calls, app = make_tf("mlx")
        loaded = load_app(CFG, tf, platform="darwin", cuda_capability=lambda: (9, 0))
        self.assertIs(loaded.app, app)
        self.assertEqual(loaded.backend, "mlx")
        self.assertNotIn("after-handler", calls)
        self.assertNotIn("real-start", calls)
        self.assertNotIn("real-arm", calls)

    def test_cuda_captures_the_app_when_serve_is_called(self):
        tf, calls, app = make_tf("cuda")
        loaded = load_app(CFG, tf, platform="linux", cuda_capability=lambda: (9, 0))
        self.assertIs(loaded.app, app)
        self.assertEqual(loaded.backend, "cuda")
        self.assertNotIn("real-serve", calls)

    def test_patches_are_undone_afterwards(self):
        tf, _, _ = make_tf("cuda")
        original_start = tf.stacks.start
        load_app(CFG, tf, platform="linux", cuda_capability=lambda: (9, 0))
        self.assertIs(tf.stacks.start, original_start)

    def test_unknown_family_fails_before_serving(self):
        tf, calls, _ = make_tf("mlx", detect_raises=ValueError("no TensorFold family for llama"))
        with self.assertRaises(LoadError) as caught:
            load_app(CFG, tf, platform="darwin", cuda_capability=lambda: (9, 0))
        self.assertIn("no TensorFold family", str(caught.exception))
        self.assertNotIn("cmd_serve", calls)

    def test_serve_errors_become_load_errors(self):
        tf, _, _ = make_tf("mlx", serve_raises=ValueError("weights do not fit"))
        with self.assertRaises(LoadError) as caught:
            load_app(CFG, tf, platform="darwin", cuda_capability=lambda: (9, 0))
        self.assertIn("weights do not fit", str(caught.exception))

    def test_argparse_exit_becomes_a_load_error(self):
        tf, _, _ = make_tf("mlx")
        tf.cli.build_parser = lambda: types.SimpleNamespace(
            parse_args=lambda argv: (_ for _ in ()).throw(SystemExit(2)))
        with self.assertRaises(LoadError):
            load_app(CFG, tf, platform="darwin", cuda_capability=lambda: (9, 0))

    def test_old_gpu_is_refused_before_serving(self):
        tf, calls, _ = make_tf("cuda")
        with self.assertRaises(LoadError) as caught:
            load_app(CFG, tf, platform="linux", cuda_capability=lambda: (8, 6))
        self.assertIn("9.0", str(caught.exception))
        self.assertNotIn("cmd_serve", calls)


class CapabilityTest(unittest.TestCase):
    def test_boundaries(self):
        check_cuda_capability((9, 0))
        check_cuda_capability((12, 1))
        with self.assertRaises(LoadError):
            check_cuda_capability((8, 9))
