# LocalAI Demonstration with Embeddings

This demonstration shows you how to use embeddings with existing data in LocalAI.
We are using the `llama-index` library to facilitate the embedding and querying processes.
The `Weaviate` client is used as the embedding source.

## Getting Started

1. Clone this repository and navigate to this directory

    ```bash
    git clone git@github.com:mudler/LocalAI.git
    cd LocalAI/examples/llamaindex
    ```

2. pip install LlamaIndex and Weviate's client: `pip install llama-index>=0.9.9 weviate-client`
3. Run the example: `python main.py`

```none
Downloading (…)lve/main/config.json: 100%|███████████████████████████| 684/684 [00:00<00:00, 6.01MB/s]
Downloading model.safetensors: 100%|███████████████████████████████| 133M/133M [00:03<00:00, 39.5MB/s]
Downloading (…)okenizer_config.json: 100%|███████████████████████████| 366/366 [00:00<00:00, 2.79MB/s]
Downloading (…)solve/main/vocab.txt: 100%|█████████████████████████| 232k/232k [00:00<00:00, 6.00MB/s]
Downloading (…)/main/tokenizer.json: 100%|█████████████████████████| 711k/711k [00:00<00:00, 18.8MB/s]
Downloading (…)cial_tokens_map.json: 100%|███████████████████████████| 125/125 [00:00<00:00, 1.18MB/s]
LocalAI is a community-driven project that aims to make AI accessible to everyone. It was created by Ettore Di Giacinto and is focused on providing various AI-related features such as text generation with GPTs, text to audio, audio to text, image generation, and more. The project is constantly growing and evolving, with a roadmap for future improvements. Anyone is welcome to contribute, provide feedback, and submit pull requests to help make LocalAI better.
```
