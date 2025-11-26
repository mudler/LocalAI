"""
Unit tests for the diffusers dynamic loader module.

These tests validate:
- Discovery of known pipeline classes
- Alias resolution for common tasks
- Fallback behavior with helpful error messages
"""

import unittest
from unittest.mock import patch, MagicMock


class TestDiffusersDynamicLoader(unittest.TestCase):
    """Test cases for the diffusers dynamic loader functionality."""

    @classmethod
    def setUpClass(cls):
        """Set up test fixtures - import the module and clear caches."""
        # Import the module to test
        import diffusers_dynamic_loader as loader
        cls.loader = loader
        # Reset the caches to ensure fresh discovery
        loader._pipeline_registry = None
        loader._task_aliases = None

    def test_camel_to_kebab_conversion(self):
        """Test CamelCase to kebab-case conversion."""
        test_cases = [
            ("StableDiffusionPipeline", "stable-diffusion-pipeline"),
            ("StableDiffusionXLPipeline", "stable-diffusion-xl-pipeline"),
            ("FluxPipeline", "flux-pipeline"),
            ("DiffusionPipeline", "diffusion-pipeline"),
        ]
        for input_val, expected in test_cases:
            with self.subTest(input=input_val):
                result = self.loader._camel_to_kebab(input_val)
                self.assertEqual(result, expected)

    def test_extract_task_keywords(self):
        """Test task keyword extraction from class names."""
        # Test text-to-image detection
        aliases = self.loader._extract_task_keywords("StableDiffusionPipeline")
        self.assertIn("stable-diffusion", aliases)

        # Test img2img detection
        aliases = self.loader._extract_task_keywords("StableDiffusionImg2ImgPipeline")
        self.assertIn("image-to-image", aliases)
        self.assertIn("img2img", aliases)

        # Test inpainting detection
        aliases = self.loader._extract_task_keywords("StableDiffusionInpaintPipeline")
        self.assertIn("inpainting", aliases)
        self.assertIn("inpaint", aliases)

        # Test depth2img detection
        aliases = self.loader._extract_task_keywords("StableDiffusionDepth2ImgPipeline")
        self.assertIn("depth-to-image", aliases)

    def test_discover_pipelines_finds_known_classes(self):
        """Test that pipeline discovery finds at least one known pipeline class."""
        registry = self.loader.get_pipeline_registry()

        # Check that the registry is not empty
        self.assertGreater(len(registry), 0, "Pipeline registry should not be empty")

        # Check for known pipeline classes
        known_pipelines = [
            "StableDiffusionPipeline",
            "DiffusionPipeline",
        ]

        for pipeline_name in known_pipelines:
            with self.subTest(pipeline=pipeline_name):
                self.assertIn(
                    pipeline_name,
                    registry,
                    f"Expected to find {pipeline_name} in registry"
                )

    def test_discover_pipelines_caches_results(self):
        """Test that pipeline discovery results are cached."""
        # Get registry twice
        registry1 = self.loader.get_pipeline_registry()
        registry2 = self.loader.get_pipeline_registry()

        # Should be the same object (cached)
        self.assertIs(registry1, registry2, "Registry should be cached")

    def test_get_available_pipelines(self):
        """Test getting list of available pipelines."""
        available = self.loader.get_available_pipelines()

        # Should return a list
        self.assertIsInstance(available, list)

        # Should contain known pipelines
        self.assertIn("StableDiffusionPipeline", available)
        self.assertIn("DiffusionPipeline", available)

        # Should be sorted
        self.assertEqual(available, sorted(available))

    def test_get_available_tasks(self):
        """Test getting list of available task aliases."""
        tasks = self.loader.get_available_tasks()

        # Should return a list
        self.assertIsInstance(tasks, list)

        # Should be sorted
        self.assertEqual(tasks, sorted(tasks))

    def test_resolve_pipeline_class_by_name(self):
        """Test resolving pipeline class by exact name."""
        from diffusers import StableDiffusionPipeline

        cls = self.loader.resolve_pipeline_class(class_name="StableDiffusionPipeline")
        self.assertEqual(cls, StableDiffusionPipeline)

    def test_resolve_pipeline_class_by_name_case_insensitive(self):
        """Test that class name resolution is case-insensitive."""
        cls1 = self.loader.resolve_pipeline_class(class_name="StableDiffusionPipeline")
        cls2 = self.loader.resolve_pipeline_class(class_name="stablediffusionpipeline")
        self.assertEqual(cls1, cls2)

    def test_resolve_pipeline_class_by_task(self):
        """Test resolving pipeline class by task alias."""
        # Get the registry to find available tasks
        aliases = self.loader.get_task_aliases()

        # Test with a common task that should be available
        if "stable-diffusion" in aliases:
            cls = self.loader.resolve_pipeline_class(task="stable-diffusion")
            self.assertIsNotNone(cls)

    def test_resolve_pipeline_class_unknown_name_raises(self):
        """Test that resolving unknown class name raises ValueError with helpful message."""
        with self.assertRaises(ValueError) as ctx:
            self.loader.resolve_pipeline_class(class_name="NonExistentPipeline")

        # Check that error message includes available pipelines
        error_msg = str(ctx.exception)
        self.assertIn("Unknown pipeline class", error_msg)
        self.assertIn("Available pipelines", error_msg)

    def test_resolve_pipeline_class_unknown_task_raises(self):
        """Test that resolving unknown task raises ValueError with helpful message."""
        with self.assertRaises(ValueError) as ctx:
            self.loader.resolve_pipeline_class(task="nonexistent-task-xyz")

        # Check that error message includes available tasks
        error_msg = str(ctx.exception)
        self.assertIn("Unknown task", error_msg)
        self.assertIn("Available tasks", error_msg)

    def test_resolve_pipeline_class_no_params_raises(self):
        """Test that calling with no parameters raises helpful ValueError."""
        with self.assertRaises(ValueError) as ctx:
            self.loader.resolve_pipeline_class()

        error_msg = str(ctx.exception)
        self.assertIn("Must provide at least one of", error_msg)

    def test_get_pipeline_info(self):
        """Test getting pipeline information."""
        info = self.loader.get_pipeline_info("StableDiffusionPipeline")

        self.assertEqual(info['name'], "StableDiffusionPipeline")
        self.assertIsInstance(info['aliases'], list)
        self.assertIsInstance(info['supports_single_file'], bool)

    def test_get_pipeline_info_unknown_raises(self):
        """Test that getting info for unknown pipeline raises ValueError."""
        with self.assertRaises(ValueError) as ctx:
            self.loader.get_pipeline_info("NonExistentPipeline")

        self.assertIn("Unknown pipeline", str(ctx.exception))


class TestDiffusersDynamicLoaderWithMocks(unittest.TestCase):
    """Test cases using mocks to test edge cases."""

    def test_load_pipeline_requires_model_id(self):
        """Test that load_diffusers_pipeline requires model_id."""
        import diffusers_dynamic_loader as loader

        with self.assertRaises(ValueError) as ctx:
            loader.load_diffusers_pipeline(class_name="StableDiffusionPipeline")

        self.assertIn("model_id is required", str(ctx.exception))

    def test_resolve_with_model_id_uses_diffusion_pipeline_fallback(self):
        """Test that resolving with only model_id falls back to DiffusionPipeline."""
        import diffusers_dynamic_loader as loader
        from diffusers import DiffusionPipeline

        # When model_id is provided, if hub lookup is not successful,
        # should fall back to DiffusionPipeline.
        # This tests the fallback behavior - the actual hub lookup may succeed
        # or fail depending on network, but the fallback path should work.
        cls = loader.resolve_pipeline_class(model_id="some/nonexistent/model")
        self.assertEqual(cls, DiffusionPipeline)


if __name__ == "__main__":
    unittest.main()
