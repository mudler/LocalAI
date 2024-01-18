
+++
disableToc = false
title = "Fine-tuning LLMs for text generation"
weight = 22
+++

{{% alert note %}}
Section under construction
{{% /alert %}}

This section covers how to fine-tune a language model for text generation and consume it in LocalAI.

[![Open In Colab](https://colab.research.google.com/assets/colab-badge.svg)](https://colab.research.google.com/github/mudler/LocalAI/blob/master/examples/e2e-fine-tuning/notebook.ipynb)

## Requirements

For this example you will need at least a 12GB VRAM of GPU and a Linux box.

## Fine-tuning

Fine-tuning a language model is a process that requires a lot of computational power and time.

Currently LocalAI doesn't support the fine-tuning endpoint as LocalAI but there are are [plans](https://github.com/mudler/LocalAI/issues/596) to support that. For the time being a guide is proposed here to give a simple starting point on how to fine-tune a model and use it with LocalAI (but also with llama.cpp).

There is an e2e example of fine-tuning a LLM model to use with [LocalAI](https://github/mudler/LocalAI) written by [@mudler](https://github.com/mudler) available [here](https://github.com/mudler/LocalAI/tree/master/examples/e2e-fine-tuning/).

The steps involved are:

- Preparing a dataset
- Prepare the environment and install dependencies
- Fine-tune the model
- Merge the Lora base with the model
- Convert the model to gguf
- Use the model with LocalAI

## Dataset preparation

We are going to need a dataset or a set of datasets. 

Axolotl supports a variety of formats, in the notebook and in this example we are aiming for a very simple dataset and build that manually, so we are going to use the `completion` format which requires the full text to be used for fine-tuning.

A dataset for an instructor model (like Alpaca) can look like the following:

```json
[
 {
    "text": "As an AI language model you are trained to reply to an instruction. Try to be as much polite as possible\n\n## Instruction\n\nWrite a poem about a tree.\n\n## Response\n\nTrees are beautiful, ...",
 },
 {
    "text": "As an AI language model you are trained to reply to an instruction. Try to be as much polite as possible\n\n## Instruction\n\nWrite a poem about a tree.\n\n## Response\n\nTrees are beautiful, ...",
 }
]
```

Every block in the text is the whole text that is used to fine-tune. For example, for an instructor model it follows the following format (more or less):

```
<System prompt>

## Instruction

<Question, instruction>

## Response

<Expected response from the LLM>
```

The instruction format works such as when we are going to inference with the model, we are going to feed it only the first part up to the `## Instruction` block, and the model is going to complete the text with the `## Response` block.

Prepare a dataset, and upload it to your Google Drive in case you are using the Google colab. Otherwise place it next the `axolotl.yaml` file as `dataset.json`.

### Install dependencies

```bash
# Install axolotl and dependencies
git clone https://github.com/OpenAccess-AI-Collective/axolotl && pushd axolotl && git checkout 797f3dd1de8fd8c0eafbd1c9fdb172abd9ff840a && popd #0.3.0
pip install packaging
pushd axolotl && pip install -e '.[flash-attn,deepspeed]' && popd

# https://github.com/oobabooga/text-generation-webui/issues/4238
pip install https://github.com/Dao-AILab/flash-attention/releases/download/v2.3.0/flash_attn-2.3.0+cu117torch2.0cxx11abiFALSE-cp310-cp310-linux_x86_64.whl
```

Configure accelerate:

```bash
accelerate config default
```

## Fine-tuning

We will need to configure axolotl. In this example is provided a file to use `axolotl.yaml` that uses openllama-3b for fine-tuning. Copy the `axolotl.yaml` file and edit it to your needs. The dataset needs to be next to it as `dataset.json`. You can find the axolotl.yaml file [here](https://github.com/mudler/LocalAI/tree/master/examples/e2e-fine-tuning/).

If you have a big dataset, you can pre-tokenize it to speedup the fine-tuning process:

```bash
# Optional pre-tokenize (run only if big dataset)
python -m axolotl.cli.preprocess axolotl.yaml
```

Now we are ready to start the fine-tuning process:
```bash
# Fine-tune
accelerate launch -m axolotl.cli.train axolotl.yaml
```

After we have finished the fine-tuning, we merge the Lora base with the model:
```bash
# Merge lora
python3 -m axolotl.cli.merge_lora axolotl.yaml --lora_model_dir="./qlora-out" --load_in_8bit=False --load_in_4bit=False
```

And we convert it to the gguf format that LocalAI can consume:

```bash

# Convert to gguf
git clone https://github.com/ggerganov/llama.cpp.git
pushd llama.cpp && make LLAMA_CUBLAS=1 && popd

# We need to convert the pytorch model into ggml for quantization
# It crates 'ggml-model-f16.bin' in the 'merged' directory.
pushd llama.cpp && python convert.py --outtype f16 \
    ../qlora-out/merged/pytorch_model-00001-of-00002.bin && popd

# Start off by making a basic q4_0 4-bit quantization.
# It's important to have 'ggml' in the name of the quant for some
# software to recognize it's file format.
pushd llama.cpp &&  ./quantize ../qlora-out/merged/ggml-model-f16.gguf \
    ../custom-model-q4_0.bin q4_0

```

Now you should have ended up with a `custom-model-q4_0.bin` file that you can copy in the LocalAI models directory and use it with LocalAI.
