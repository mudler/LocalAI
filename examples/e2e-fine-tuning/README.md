This is an example of fine-tuning a LLM model to use with [LocalAI](https://github.com/mudler/LocalAI) written by [@mudler](https://github.com/mudler).

Specifically, this example shows how to use [axolotl](https://github.com/OpenAccess-AI-Collective/axolotl) to fine-tune a LLM model to consume with LocalAI as a `gguf` model.

A notebook is provided that currently works on _very small_ datasets on Google colab on the free instance. It is far from producing good models, but it gives a sense of how to use the code to use with a better dataset and configurations, and how to use the model produced with LocalAI. [![Open In Colab](https://colab.research.google.com/assets/colab-badge.svg)](https://colab.research.google.com/github/mudler/LocalAI/blob/master/examples/e2e-fine-tuning/notebook.ipynb)

## Requirements

For this example you will need at least a 12GB VRAM of GPU and a Linux box.
The notebook is tested on Google Colab with a Tesla T4 GPU.

## Clone this directory

Clone the repository and enter the example directory:

```bash
git clone http://github.com/mudler/LocalAI
cd LocalAI/examples/e2e-fine-tuning
```

## Install dependencies

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

We will need to configure axolotl. In this example is provided a file to use `axolotl.yaml` that uses openllama-3b for fine-tuning. Copy the `axolotl.yaml` file and edit it to your needs. The dataset needs to be next to it as `dataset.json`. The format used is `completion` which is a list of JSON objects with a `text` field with the full text to train the LLM with.

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
pushd llama.cpp && make GGML_CUDA=1 && popd

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
