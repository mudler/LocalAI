"""
Dynamic Diffusers Pipeline Loader

This module provides dynamic discovery and loading of diffusers pipelines at runtime,
eliminating the need for per-pipeline conditional statements. New pipelines added to
diffusers become available automatically without code changes.

The module also supports discovering other diffusers classes like schedulers, models,
and other components, making it a generic solution for dynamic class loading.

Usage:
    from diffusers_dynamic_loader import load_diffusers_pipeline, get_available_pipelines

    # Load by class name
    pipe = load_diffusers_pipeline(class_name="StableDiffusionPipeline", model_id="...", torch_dtype=torch.float16)

    # Load by task alias
    pipe = load_diffusers_pipeline(task="text-to-image", model_id="...", torch_dtype=torch.float16)

    # Load using model_id (infers from HuggingFace Hub if possible)
    pipe = load_diffusers_pipeline(model_id="runwayml/stable-diffusion-v1-5", torch_dtype=torch.float16)

    # Get list of available pipelines
    available = get_available_pipelines()

    # Discover other diffusers classes (schedulers, models, etc.)
    schedulers = discover_diffusers_classes("SchedulerMixin")
    models = discover_diffusers_classes("ModelMixin")
"""

import importlib
import re
import sys
from typing import Any, Dict, List, Optional, Tuple, Type


# Global cache for discovered pipelines - computed once per process
_pipeline_registry: Optional[Dict[str, Type]] = None
_task_aliases: Optional[Dict[str, List[str]]] = None

# Global cache for other discovered class types
_class_registries: Dict[str, Dict[str, Type]] = {}


def _camel_to_kebab(name: str) -> str:
    """
    Convert CamelCase to kebab-case.

    Examples:
        StableDiffusionPipeline -> stable-diffusion-pipeline
        StableDiffusionXLImg2ImgPipeline -> stable-diffusion-xl-img-2-img-pipeline
    """
    # Insert hyphen before uppercase letters (but not at the start)
    s1 = re.sub('(.)([A-Z][a-z]+)', r'\1-\2', name)
    # Insert hyphen before uppercase letters following lowercase letters or numbers
    s2 = re.sub('([a-z0-9])([A-Z])', r'\1-\2', s1)
    return s2.lower()


def _extract_task_keywords(class_name: str) -> List[str]:
    """
    Extract task-related keywords from a pipeline class name.

    This function derives useful task aliases from the class name without
    hardcoding per-pipeline branches.

    Returns a list of potential task aliases for this pipeline.
    """
    aliases = []
    name_lower = class_name.lower()

    # Direct task mappings based on common patterns in class names
    task_patterns = {
        'text2image': ['text-to-image', 'txt2img', 'text2image'],
        'texttoimage': ['text-to-image', 'txt2img', 'text2image'],
        'txt2img': ['text-to-image', 'txt2img', 'text2image'],
        'img2img': ['image-to-image', 'img2img', 'image2image'],
        'image2image': ['image-to-image', 'img2img', 'image2image'],
        'imagetoimage': ['image-to-image', 'img2img', 'image2image'],
        'img2video': ['image-to-video', 'img2vid', 'img2video'],
        'imagetovideo': ['image-to-video', 'img2vid', 'img2video'],
        'text2video': ['text-to-video', 'txt2vid', 'text2video'],
        'texttovideo': ['text-to-video', 'txt2vid', 'text2video'],
        'inpaint': ['inpainting', 'inpaint'],
        'depth2img': ['depth-to-image', 'depth2img'],
        'depthtoimage': ['depth-to-image', 'depth2img'],
        'controlnet': ['controlnet', 'control-net'],
        'upscale': ['upscaling', 'upscale', 'super-resolution'],
        'superresolution': ['upscaling', 'upscale', 'super-resolution'],
    }

    # Check for each pattern in the class name
    for pattern, task_aliases in task_patterns.items():
        if pattern in name_lower:
            aliases.extend(task_aliases)

    # Also detect general pipeline types from the class name structure
    # E.g., StableDiffusionPipeline -> stable-diffusion, flux -> flux
    # Remove "Pipeline" suffix and convert to kebab case
    if class_name.endswith('Pipeline'):
        base_name = class_name[:-8]  # Remove "Pipeline"
        kebab_name = _camel_to_kebab(base_name)
        aliases.append(kebab_name)

        # Extract model family name (e.g., "stable-diffusion" from "stable-diffusion-xl-img-2-img")
        parts = kebab_name.split('-')
        if len(parts) >= 2:
            # Try the first two words as a family name
            family = '-'.join(parts[:2])
            if family not in aliases:
                aliases.append(family)

    # If no specific task pattern matched but class contains "Pipeline", add "text-to-image" as default
    # since most diffusion pipelines support text-to-image generation
    if 'text-to-image' not in aliases and 'image-to-image' not in aliases:
        # Only add for pipelines that seem to be generation pipelines (not schedulers, etc.)
        if 'pipeline' in name_lower and not any(x in name_lower for x in ['scheduler', 'processor', 'encoder']):
            # Don't automatically add - let it be explicit
            pass

    return list(set(aliases))  # Remove duplicates


def discover_diffusers_classes(
    base_class_name: str,
    include_base: bool = True
) -> Dict[str, Type]:
    """
    Discover all subclasses of a given base class from diffusers.

    This function provides a generic way to discover any type of diffusers class,
    not just pipelines. It can be used to discover schedulers, models, processors,
    and other components.

    Args:
        base_class_name: Name of the base class to search for subclasses
                        (e.g., "DiffusionPipeline", "SchedulerMixin", "ModelMixin")
        include_base: Whether to include the base class itself in results

    Returns:
        Dict mapping class names to class objects

    Examples:
        # Discover all pipeline classes
        pipelines = discover_diffusers_classes("DiffusionPipeline")

        # Discover all scheduler classes
        schedulers = discover_diffusers_classes("SchedulerMixin")

        # Discover all model classes
        models = discover_diffusers_classes("ModelMixin")

        # Discover AutoPipeline classes
        auto_pipelines = discover_diffusers_classes("AutoPipelineForText2Image")
    """
    global _class_registries

    # Check cache first
    if base_class_name in _class_registries:
        return _class_registries[base_class_name]

    import diffusers

    # Try to get the base class from diffusers
    base_class = None
    try:
        base_class = getattr(diffusers, base_class_name)
    except AttributeError:
        # Try to find in submodules
        for submodule in ['schedulers', 'models', 'pipelines']:
            try:
                module = importlib.import_module(f'diffusers.{submodule}')
                if hasattr(module, base_class_name):
                    base_class = getattr(module, base_class_name)
                    break
            except (ImportError, ModuleNotFoundError):
                continue

    if base_class is None:
        raise ValueError(f"Could not find base class '{base_class_name}' in diffusers")

    registry: Dict[str, Type] = {}

    # Include base class if requested
    if include_base:
        registry[base_class_name] = base_class

    # Scan diffusers module for subclasses
    for attr_name in dir(diffusers):
        try:
            attr = getattr(diffusers, attr_name)
            if (isinstance(attr, type) and
                issubclass(attr, base_class) and
                (include_base or attr is not base_class)):
                registry[attr_name] = attr
        except (ImportError, AttributeError, TypeError, RuntimeError, ModuleNotFoundError):
            continue

    # Cache the results
    _class_registries[base_class_name] = registry
    return registry


def get_available_classes(base_class_name: str) -> List[str]:
    """
    Get a sorted list of all discovered class names for a given base class.

    Args:
        base_class_name: Name of the base class (e.g., "SchedulerMixin")

    Returns:
        Sorted list of discovered class names
    """
    return sorted(discover_diffusers_classes(base_class_name).keys())


def _discover_pipelines() -> Tuple[Dict[str, Type], Dict[str, List[str]]]:
    """
    Discover all subclasses of DiffusionPipeline from diffusers.

    This function uses the generic discover_diffusers_classes() internally
    and adds pipeline-specific task alias generation. It also includes
    AutoPipeline classes which are special utility classes for automatic
    pipeline selection.

    Returns:
        A tuple of (pipeline_registry, task_aliases) where:
        - pipeline_registry: Dict mapping class names to class objects
        - task_aliases: Dict mapping task aliases to lists of class names
    """
    # Use the generic discovery function
    pipeline_registry = discover_diffusers_classes("DiffusionPipeline", include_base=True)

    # Also add AutoPipeline classes - these are special utility classes that are
    # NOT subclasses of DiffusionPipeline but are commonly used
    import diffusers
    auto_pipeline_classes = [
        "AutoPipelineForText2Image",
        "AutoPipelineForImage2Image",
        "AutoPipelineForInpainting",
    ]
    for cls_name in auto_pipeline_classes:
        try:
            cls = getattr(diffusers, cls_name)
            if cls is not None:
                pipeline_registry[cls_name] = cls
        except AttributeError:
            # Class not available in this version of diffusers
            pass

    # Generate task aliases for pipelines
    task_aliases: Dict[str, List[str]] = {}
    for attr_name in pipeline_registry:
        if attr_name == "DiffusionPipeline":
            continue  # Skip base class for alias generation

        aliases = _extract_task_keywords(attr_name)
        for alias in aliases:
            if alias not in task_aliases:
                task_aliases[alias] = []
            if attr_name not in task_aliases[alias]:
                task_aliases[alias].append(attr_name)

    return pipeline_registry, task_aliases


def get_pipeline_registry() -> Dict[str, Type]:
    """
    Get the cached pipeline registry.

    Returns a dictionary mapping pipeline class names to their class objects.
    The registry is built on first access and cached for subsequent calls.
    """
    global _pipeline_registry, _task_aliases
    if _pipeline_registry is None:
        _pipeline_registry, _task_aliases = _discover_pipelines()
    return _pipeline_registry


def get_task_aliases() -> Dict[str, List[str]]:
    """
    Get the cached task aliases dictionary.

    Returns a dictionary mapping task aliases (e.g., "text-to-image") to
    lists of pipeline class names that support that task.
    """
    global _pipeline_registry, _task_aliases
    if _task_aliases is None:
        _pipeline_registry, _task_aliases = _discover_pipelines()
    return _task_aliases


def get_available_pipelines() -> List[str]:
    """
    Get a sorted list of all discovered pipeline class names.

    Returns:
        List of pipeline class names available for loading.
    """
    return sorted(get_pipeline_registry().keys())


def get_available_tasks() -> List[str]:
    """
    Get a sorted list of all available task aliases.

    Returns:
        List of task aliases (e.g., ["text-to-image", "image-to-image", ...])
    """
    return sorted(get_task_aliases().keys())


def resolve_pipeline_class(
    class_name: Optional[str] = None,
    task: Optional[str] = None,
    model_id: Optional[str] = None
) -> Type:
    """
    Resolve a pipeline class from class_name, task, or model_id.

    Priority:
    1. If class_name is provided, look it up directly
    2. If task is provided, resolve through task aliases
    3. If model_id is provided, try to infer from HuggingFace Hub

    Args:
        class_name: Exact pipeline class name (e.g., "StableDiffusionPipeline")
        task: Task alias (e.g., "text-to-image", "img2img")
        model_id: HuggingFace model ID (e.g., "runwayml/stable-diffusion-v1-5")

    Returns:
        The resolved pipeline class.

    Raises:
        ValueError: If no pipeline could be resolved.
    """
    registry = get_pipeline_registry()
    aliases = get_task_aliases()

    # 1. Direct class name lookup
    if class_name:
        if class_name in registry:
            return registry[class_name]
        # Try case-insensitive match
        for name, cls in registry.items():
            if name.lower() == class_name.lower():
                return cls
        raise ValueError(
            f"Unknown pipeline class '{class_name}'. "
            f"Available pipelines: {', '.join(sorted(registry.keys())[:20])}..."
        )

    # 2. Task alias lookup
    if task:
        task_lower = task.lower().replace('_', '-')
        if task_lower in aliases:
            # Return the first matching pipeline for this task
            matching_classes = aliases[task_lower]
            if matching_classes:
                return registry[matching_classes[0]]

        # Try partial matching
        for alias, classes in aliases.items():
            if task_lower in alias or alias in task_lower:
                if classes:
                    return registry[classes[0]]

        raise ValueError(
            f"Unknown task '{task}'. "
            f"Available tasks: {', '.join(sorted(aliases.keys())[:20])}..."
        )

    # 3. Try to infer from HuggingFace Hub
    if model_id:
        try:
            from huggingface_hub import model_info
            info = model_info(model_id)

            # Check pipeline_tag
            if hasattr(info, 'pipeline_tag') and info.pipeline_tag:
                tag = info.pipeline_tag.lower().replace('_', '-')
                if tag in aliases:
                    matching_classes = aliases[tag]
                    if matching_classes:
                        return registry[matching_classes[0]]

            # Check model card for hints
            if hasattr(info, 'cardData') and info.cardData:
                card = info.cardData
                if 'pipeline_tag' in card:
                    tag = card['pipeline_tag'].lower().replace('_', '-')
                    if tag in aliases:
                        matching_classes = aliases[tag]
                        if matching_classes:
                            return registry[matching_classes[0]]

        except ImportError:
            # huggingface_hub not available
            pass
        except (KeyError, AttributeError, ValueError, OSError):
            # Model info lookup failed - common cases:
            # - KeyError: Missing keys in model card
            # - AttributeError: Missing attributes on model info
            # - ValueError: Invalid model data
            # - OSError: Network or file access issues
            pass

        # Fallback: use DiffusionPipeline.from_pretrained which auto-detects
        # DiffusionPipeline is always added to registry in _discover_pipelines (line 132)
        # but use .get() with import fallback for extra safety
        from diffusers import DiffusionPipeline
        return registry.get('DiffusionPipeline', DiffusionPipeline)

    raise ValueError(
        "Must provide at least one of: class_name, task, or model_id. "
        f"Available pipelines: {', '.join(sorted(registry.keys())[:20])}... "
        f"Available tasks: {', '.join(sorted(aliases.keys())[:20])}..."
    )


def load_diffusers_pipeline(
    class_name: Optional[str] = None,
    task: Optional[str] = None,
    model_id: Optional[str] = None,
    from_single_file: bool = False,
    **kwargs
) -> Any:
    """
    Load a diffusers pipeline dynamically.

    This function resolves the appropriate pipeline class based on the provided
    parameters and instantiates it with the given kwargs.

    Args:
        class_name: Exact pipeline class name (e.g., "StableDiffusionPipeline")
        task: Task alias (e.g., "text-to-image", "img2img")
        model_id: HuggingFace model ID or local path
        from_single_file: If True, use from_single_file() instead of from_pretrained()
        **kwargs: Additional arguments passed to from_pretrained() or from_single_file()

    Returns:
        An instantiated pipeline object.

    Raises:
        ValueError: If no pipeline could be resolved.
        Exception: If pipeline loading fails.

    Examples:
        # Load by class name
        pipe = load_diffusers_pipeline(
            class_name="StableDiffusionPipeline",
            model_id="runwayml/stable-diffusion-v1-5",
            torch_dtype=torch.float16
        )

        # Load by task
        pipe = load_diffusers_pipeline(
            task="text-to-image",
            model_id="runwayml/stable-diffusion-v1-5",
            torch_dtype=torch.float16
        )

        # Load from single file
        pipe = load_diffusers_pipeline(
            class_name="StableDiffusionPipeline",
            model_id="/path/to/model.safetensors",
            from_single_file=True,
            torch_dtype=torch.float16
        )
    """
    # Resolve the pipeline class
    pipeline_class = resolve_pipeline_class(
        class_name=class_name,
        task=task,
        model_id=model_id
    )

    # If no model_id provided but we have a class, we can't load
    if model_id is None:
        raise ValueError("model_id is required to load a pipeline")

    # Load the pipeline
    try:
        if from_single_file:
            # Check if the class has from_single_file method
            if hasattr(pipeline_class, 'from_single_file'):
                return pipeline_class.from_single_file(model_id, **kwargs)
            else:
                raise ValueError(
                    f"Pipeline class {pipeline_class.__name__} does not support from_single_file(). "
                    f"Use from_pretrained() instead."
                )
        else:
            return pipeline_class.from_pretrained(model_id, **kwargs)

    except Exception as e:
        # Provide helpful error message
        available = get_available_pipelines()
        raise RuntimeError(
            f"Failed to load pipeline '{pipeline_class.__name__}' from '{model_id}': {e}\n"
            f"Available pipelines: {', '.join(available[:20])}..."
        ) from e


def get_pipeline_info(class_name: str) -> Dict[str, Any]:
    """
    Get information about a specific pipeline class.

    Args:
        class_name: The pipeline class name

    Returns:
        Dictionary with pipeline information including:
        - name: Class name
        - aliases: List of task aliases
        - supports_single_file: Whether from_single_file() is available
        - docstring: Class docstring (if available)
    """
    registry = get_pipeline_registry()
    aliases = get_task_aliases()

    if class_name not in registry:
        raise ValueError(f"Unknown pipeline: {class_name}")

    cls = registry[class_name]

    # Find all aliases for this pipeline
    pipeline_aliases = []
    for alias, classes in aliases.items():
        if class_name in classes:
            pipeline_aliases.append(alias)

    return {
        'name': class_name,
        'aliases': pipeline_aliases,
        'supports_single_file': hasattr(cls, 'from_single_file'),
        'docstring': cls.__doc__[:200] if cls.__doc__ else None
    }
