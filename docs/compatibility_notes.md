# Model Architecture Compatibility Notes

## Unsupported Model Architectures

LocalAI relies on llama.cpp for model loading and inference. If you encounter an error like:

```
unknown model architecture: 'lumina2'
```

This indicates that the model you're trying to load uses an architecture that the bundled llama.cpp version doesn't support yet.

### Common Cases:

1. **Lumina2 Models**: These are specialized image-generation models that require newer versions of llama.cpp with Lumina2 support.

2. **Other Unsupported Architectures**: Similar issues may occur with other newer or less common model architectures.

### Solutions:

1. **Check Model Compatibility**: Before downloading a model, verify that it's compatible with llama.cpp. Check the model card for supported architectures.

2. **Use Supported Alternatives**: Consider using models with standard architectures (Llama, Mistral, Gemma, etc.) that are well-supported.

3. **Update LocalAI**: Newer versions of LocalAI may include updated llama.cpp with support for more architectures.

4. **Request Architecture Support**: If you need a specific architecture, you can open an issue in the LocalAI repository to request support.

### Error Message Reference:

When you see:
- `error loading model architecture: unknown model architecture: '<architecture_name>'`
- `Failed to load model: ... error: llama_model_load: error loading model: error loading model architecture: unknown model architecture: '<architecture_name>'`

This means the model architecture is not recognized by the current llama.cpp version bundled with LocalAI.
