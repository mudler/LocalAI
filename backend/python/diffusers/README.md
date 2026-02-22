# Multi-GPU Configuration for Diffusers

When using diffusers with multiple GPUs, you need to configure the model with `tensor_parallel_size` set to the number of GPUs you want to use.

## YAML Configuration Example

```yaml
name: stable-diffusion-multigpu
model: stabilityai/stable-diffusion-xl-base-1.0
backend: diffusers
parameters:
  tensor_parallel_size: 2 # Number of GPUs to use
```

## gRPC Configuration

The `tensor_parallel_size` parameter is set in the gRPC proto configuration (in `ModelOptions` message, field 55). When this is set to a value greater than 1, the diffusers backend automatically enables `device_map="auto"` to distribute the model across multiple GPUs.

## Notes

- Ensure you have sufficient GPU memory across all devices
- The model will be automatically distributed across available GPUs
- For optimal performance, use GPUs of the same type and memory capacity