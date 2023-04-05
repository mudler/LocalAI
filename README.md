## :camel: llama-cli


llama-cli is a straightforward golang CLI interface for [llama.cpp](https://github.com/ggerganov/llama.cpp), providing a simple API and a command line interface that allows text generation using a GPT-based model like llama directly from the terminal. It is also compatible with [gpt4all](https://github.com/nomic-ai/gpt4all) and [alpaca](https://github.com/tatsu-lab/stanford_alpaca).

`llama-cli` uses https://github.com/go-skynet/llama, which is a fork of [llama.cpp](https://github.com/ggerganov/llama.cpp) providing golang binding.

## Container images

To begin, run:

```
docker run -ti --rm quay.io/go-skynet/llama-cli:v0.4  --instruction "What's an alpaca?" --topk 10000 --model ...
```

You will receive a response like the following:

```
An alpaca is a member of the South American Camelid family, which includes the llama, guanaco and vicuña. It is a domesticated species that originates from the Andes mountain range in South America. Alpacas are used in the textile industry for their fleece, which is much softer than wool. Alpacas are also used for meat, milk, and fiber.
```

## Basic usage

To use llama-cli, specify a pre-trained GPT-based model, an input text, and an instruction for text generation. llama-cli takes the following arguments when running from the CLI:

```
llama-cli --model <model_path> --instruction <instruction> [--input <input>] [--template <template_path>] [--tokens <num_tokens>] [--threads <num_threads>] [--temperature <temperature>] [--topp <top_p>] [--topk <top_k>]
```

| Parameter    | Environment Variable | Default Value | Description                            |
| ------------ | -------------------- | ------------- | -------------------------------------- |
| template     | TEMPLATE             |               | A file containing a template for output formatting (optional).  |
| instruction  | INSTRUCTION          |               | Input prompt text or instruction. "-" for STDIN.   |
| input        | INPUT                | -             | Path to text or "-" for STDIN.                    |
| model        | MODEL_PATH           |               | The path to the pre-trained GPT-based model.      |
| tokens       | TOKENS               | 128           | The maximum number of tokens to generate. |
| threads      | THREADS              | NumCPU()      | The number of threads to use for text generation. |
| temperature  | TEMPERATURE          | 0.95          | Sampling temperature for model output. ( values between `0.1` and `1.0` )  |
| top_p        | TOP_P                | 0.85          | The cumulative probability for top-p sampling. |
| top_k        | TOP_K                | 20            | The number of top-k tokens to consider for text generation.  |
| context-size | CONTEXT_SIZE         | 512           | Default token context size. |
| alpaca       | ALPACA               | true          | Set to true for alpaca models. |
| gpt4all       | GPT4ALL               | false          | Set to true for gpt4all models. |

Here's an example of using `llama-cli`:

```
llama-cli --model ~/ggml-alpaca-7b-q4.bin --instruction "What's an alpaca?"
```

This will generate text based on the given model and instruction.

## Advanced usage

`llama-cli` also provides an API for running text generation as a service. The model will be pre-loaded and kept in memory.

Example of starting the API with `docker`:

```bash
docker run -p 8080:8080 -ti --rm quay.io/go-skynet/llama-cli:v0.4 api --context-size 700 --threads 4
```

And you'll see:
```
┌───────────────────────────────────────────────────┐ 
│                   Fiber v2.42.0                   │ 
│               http://127.0.0.1:8080               │ 
│       (bound on host 0.0.0.0 and port 8080)       │ 
│                                                   │ 
│ Handlers ............. 1  Processes ........... 1 │ 
│ Prefork ....... Disabled  PID ................. 1 │ 
└───────────────────────────────────────────────────┘ 
```

You can control the API server options with command line arguments:

```
llama-cli api --model <model_path> [--address <address>] [--threads <num_threads>]
```

The API takes takes the following:

| Parameter    | Environment Variable | Default Value | Description                            |
| ------------ | -------------------- | ------------- | -------------------------------------- |
| model        | MODEL_PATH           |               | The path to the pre-trained GPT-based model.      |
| threads      | THREADS              | CPU cores     | The number of threads to use for text generation. |
| address      | ADDRESS              | :8080         | The address and port to listen on. |
| context-size | CONTEXT_SIZE         | 512           | Default token context size. |
| alpaca       | ALPACA               | true          | Set to true for alpaca models. |
| gpt4all       | GPT4ALL               | false          | Set to true for gpt4all models. |


Once the server is running, you can start making requests to it using HTTP. For example, to generate text based on an instruction, you can send a POST request to the `/predict` endpoint with the instruction as the request body:

```
curl --location --request POST 'http://localhost:8080/predict' --header 'Content-Type: application/json' --data-raw '{
    "text": "What is an alpaca?",
    "topP": 0.8,
    "topK": 50,
    "temperature": 0.7,
    "tokens": 100
}'
```

There is also available a simple web interface (for instance, http://localhost:8080/) which can be used as a playground.

Note: The API doesn't inject a template for talking to the instance, while the CLI does. You have to use a prompt similar to what's described in the standford-alpaca docs: https://github.com/tatsu-lab/stanford_alpaca#data-release, for instance:

```
Below is an instruction that describes a task. Write a response that appropriately completes the request.

### Instruction:
{instruction}

### Response:
```

## Using other models

You can specify a model binary to be used for inference with `--model`.

13B and 30B alpaca models are known to work:

```
# Download the model image, extract the model
# Use the model with llama-cli
docker run -v $PWD:/models -p 8080:8080 -ti --rm quay.io/go-skynet/llama-cli:v0.4 api --model /models/model.bin
```

gpt4all (https://github.com/nomic-ai/gpt4all) works as well, however the original model needs to be converted (same applies for old alpaca models, too):

```bash
wget -O tokenizer.model https://huggingface.co/decapoda-research/llama-30b-hf/resolve/main/tokenizer.model
mkdir models
cp gpt4all.. models/
git clone https://gist.github.com/eiz/828bddec6162a023114ce19146cb2b82
pip install sentencepiece
python 828bddec6162a023114ce19146cb2b82/gistfile1.txt models tokenizer.model
# There will be a new model with the ".tmp" extension, you have to use that one!
```

### Golang client API

The `llama-cli` codebase has also a small client in go that can be used alongside with the api:

```golang
package main

import (
	"fmt"

	client "github.com/go-skynet/llama-cli/client"
)

func main() {

	cli := client.NewClient("http://ip:30007")

	out, err := cli.Predict("What's an alpaca?")
	if err != nil {
		panic(err)
	}

	fmt.Println(out)
}
```

### Windows compatibility

It should work, however you need to make sure you give enough resources to the container. See https://github.com/go-skynet/llama-cli/issues/2

### Kubernetes

You can run the API directly in Kubernetes:

```bash
kubectl apply -f https://raw.githubusercontent.com/go-skynet/llama-cli/master/kubernetes/deployment.yaml
```

### Build locally

Pre-built images might fit well for most of the modern hardware, however you can and might need to build the images manually.

In order to build the `llama-cli` container image locally you can use `docker`:

```
# build the image as "alpaca-image"
docker run --privileged -v /var/run/docker.sock:/var/run/docker.sock --rm -t -v "$(pwd)":/workspace -v earthly-tmp:/tmp/earthly:rw earthly/earthly:v0.7.2 +image --IMAGE=alpaca-image
# run the image
docker run alpaca-image --instruction "What's an alpaca?"
```

Or build the binary with:

```
# build the image as "alpaca-image"
docker run --privileged -v /var/run/docker.sock:/var/run/docker.sock --rm -t -v "$(pwd)":/workspace -v earthly-tmp:/tmp/earthly:rw earthly/earthly:v0.7.2 +build
# run the binary
./llama-cli --instruction "What's an alpaca?"
```

## Short-term roadmap

- Mimic OpenAI API (https://github.com/go-skynet/llama-cli/issues/10)
- Binary releases (https://github.com/go-skynet/llama-cli/issues/6)
- Upstream our golang bindings to llama.cpp (https://github.com/ggerganov/llama.cpp/issues/351)

## License

MIT

## Acknowledgements

- [llama.cpp](https://github.com/ggerganov/llama.cpp)
- https://github.com/tatsu-lab/stanford_alpaca
- https://github.com/cornelk/llama-go for the initial ideas
- https://github.com/antimatter15/alpaca.cpp for the light model version (this is compatible and tested only with that checkpoint model!)
