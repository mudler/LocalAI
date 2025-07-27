+++
disableToc = false
title = "🔍 Object detection"
weight = 13
url = "/features/object-detection/"
+++

LocalAI supports object detection through various backends. This feature allows you to identify and locate objects within images with high accuracy and real-time performance. Currently, [RF-DETR](https://github.com/roboflow/rf-detr) is available as an implementation.

## Overview

Object detection in LocalAI is implemented through dedicated backends that can identify and locate objects within images. Each backend provides different capabilities and model architectures.

**Key Features:**
- Real-time object detection
- High accuracy detection with bounding boxes
- Support for multiple hardware accelerators (CPU, NVIDIA GPU, Intel GPU, AMD GPU)
- Structured detection results with confidence scores
- Easy integration through the `/v1/detection` endpoint

## Usage

### Detection Endpoint

LocalAI provides a dedicated `/v1/detection` endpoint for object detection tasks. This endpoint is specifically designed for object detection and returns structured detection results with bounding boxes and confidence scores.

### API Reference

To perform object detection, send a POST request to the `/v1/detection` endpoint:

```bash
curl -X POST http://localhost:8080/v1/detection \
  -H "Content-Type: application/json" \
  -d '{
    "model": "rfdetr-base",
    "image": "https://media.roboflow.com/dog.jpeg"
  }'
```

### Request Format

The request body should contain:

- `model`: The name of the object detection model (e.g., "rfdetr-base")
- `image`: The image to analyze, which can be:
  - A URL to an image
  - A base64-encoded image

### Response Format

The API returns a JSON response with detected objects:

```json
{
  "detections": [
    {
      "x": 100.5,
      "y": 150.2,
      "width": 200.0,
      "height": 300.0,
      "confidence": 0.95,
      "class_name": "dog"
    },
    {
      "x": 400.0,
      "y": 200.0,
      "width": 150.0,
      "height": 250.0,
      "confidence": 0.87,
      "class_name": "person"
    }
  ]
}
```

Each detection includes:
- `x`, `y`: Coordinates of the bounding box top-left corner
- `width`, `height`: Dimensions of the bounding box
- `confidence`: Detection confidence score (0.0 to 1.0)
- `class_name`: The detected object class

## Backends

### RF-DETR Backend

The RF-DETR backend is implemented as a Python-based gRPC service that integrates seamlessly with LocalAI. It provides object detection capabilities using the RF-DETR model architecture and supports multiple hardware configurations:

- **CPU**: Optimized for CPU inference
- **NVIDIA GPU**: CUDA acceleration for NVIDIA GPUs
- **Intel GPU**: Intel oneAPI optimization
- **AMD GPU**: ROCm acceleration for AMD GPUs
- **NVIDIA Jetson**: Optimized for ARM64 NVIDIA Jetson devices

#### Setup

1. **Using the Model Gallery (Recommended)**

   The easiest way to get started is using the model gallery. The `rfdetr-base` model is available in the official LocalAI gallery:

   ```bash
   # Install and run the rfdetr-base model
   local-ai run rfdetr-base
   ```

   You can also install it through the web interface by navigating to the Models section and searching for "rfdetr-base".

2. **Manual Configuration**

   Create a model configuration file in your `models` directory:

   ```yaml
   name: rfdetr
   backend: rfdetr
   parameters:
     model: rfdetr-base
   ```

#### Available Models

Currently, the following model is available in the [Model Gallery]({{%relref "docs/features/model-gallery" %}}):

- **rfdetr-base**: Base model with balanced performance and accuracy

You can browse and install this model through the LocalAI web interface or using the command line.

## Examples

### Basic Object Detection

```bash
# Detect objects in an image from URL
curl -X POST http://localhost:8080/v1/detection \
  -H "Content-Type: application/json" \
  -d '{
    "model": "rfdetr-base",
    "image": "https://example.com/image.jpg"
  }'
```

### Base64 Image Detection

```bash
# Convert image to base64 and send
base64_image=$(base64 -w 0 image.jpg)
curl -X POST http://localhost:8080/v1/detection \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"rfdetr-base\",
    \"image\": \"data:image/jpeg;base64,$base64_image\"
  }"
```

## Troubleshooting

### Common Issues

1. **Model Loading Errors**
   - Ensure the model file is properly downloaded
   - Check available disk space
   - Verify model compatibility with your backend version

2. **Low Detection Accuracy**
   - Ensure good image quality and lighting
   - Check if objects are clearly visible
   - Consider using a larger model for better accuracy

3. **Slow Performance**
   - Enable GPU acceleration if available
   - Use a smaller model for faster inference
   - Optimize image resolution

### Debug Mode

Enable debug logging for troubleshooting:

```bash
local-ai run --debug rfdetr-base
```

## Object Detection Category

LocalAI includes a dedicated **object-detection** category for models and backends that specialize in identifying and locating objects within images. This category currently includes:

- **RF-DETR**: Real-time transformer-based object detection

Additional object detection models and backends will be added to this category in the future. You can filter models by the `object-detection` tag in the model gallery to find all available object detection models.

## Related Features

- [🎨 Image generation]({{%relref "docs/features/image-generation" %}}): Generate images with AI
- [📖 Text generation]({{%relref "docs/features/text-generation" %}}): Generate text with language models
- [🔍 GPT Vision]({{%relref "docs/features/gpt-vision" %}}): Analyze images with language models
- [🚀 GPU acceleration]({{%relref "docs/features/GPU-acceleration" %}}): Optimize performance with GPU acceleration 