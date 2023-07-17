# LocalAI functions

Example of using LocalAI functions, see the [OpenAI](https://openai.com/blog/function-calling-and-other-api-updates) blog post.

## Run

```bash
# Clone LocalAI
git clone https://github.com/go-skynet/LocalAI

cd LocalAI/examples/functions

docker-compose run --rm functions
```

Note: The example automatically downloads the `openllama` model as it is under a permissive license.

See the `.env` configuration file to set a different model with the [model-gallery](https://github.com/go-skynet/model-gallery) by editing `PRELOAD_MODELS`.
