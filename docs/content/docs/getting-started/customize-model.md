 
+++
disableToc = false
title = "Customize model"
weight = 2
icon = "rocket_launch"

+++

In order to customize the prompt template or the model default settings, it's possible to use a configuration file. The file should be a valid LocalAI YAML configuration file, for the full syntax see [advanced]({{%relref "docs/advanced" %}}), and it can be hosted remotely (for instance as a Github Gist). The configuration file can also refer to a model either on the local filesystem or a remote URL.

LocalAI can be started (either the container image or the binary) with a list of model config files URLs or a short-handed format (e.g. `huggingface://`. `github://` that will automatically expand to full URLs). 

You can also pass it via Environment variable, for example:

```
local-ai github://owner/repo/file.yaml@branch

# Env
MODELS="github://owner/repo/file.yaml@branch,github://owner/repo/file.yaml@branch" local-ai

# Args
local-ai --models github://owner/repo/file.yaml@branch --models github://owner/repo/file.yaml@branch
```

This is an example, to start **phi-2**:

```bash
docker run -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core https://gist.githubusercontent.com/mudler/ad601a0488b497b69ec549150d9edd18/raw/a8a8869ef1bb7e3830bf5c0bae29a0cce991ff8d/phi-2.yaml
```

{{% alert icon="" %}}
The list of models configuration used in the quickstart are available here: [https://github.com/mudler/LocalAI/tree/master/embedded/models](https://github.com/mudler/LocalAI/tree/master/embedded/models), if you want to help and contribute feel free to open up a Pull Request.

The `phi-2` model example used in the quickstart is automatically expanded to [https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml](https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml).
{{% /alert %}}

## Example: Customize the prompt template

Create a Github gist, or a pastebin file, copy the content of [https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml](https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml). And modify the template field:

```yaml
name: phi-2
context_size: 2048
f16: true
threads: 11
gpu_layers: 90
mmap: true
parameters:
  # You can refer here to any HF model, or a local file
  model: huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
  temperature: 0.2
  top_k: 40
  top_p: 0.95
template:
  
  chat: &template |
    Instruct: {{.Input}}
    Output:
  # Modify the prompt template here ^^^ accordingly to your needs
  completion: *template
```

Then start LocalAI with the URL of the gist:

```bash
## Attention! replace with your gists URL!
docker run -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core https://gist.githubusercontent.com/xxxx/phi-2.yaml
```

### What's next

- See the [advanced section]({{%relref "docs/advanced" %}}) to learn more about the prompt template and the configuration files.
- If you want to fine-tune an LLM model, see the [fine-tuning section]({{%relref "docs/advanced/fine-tuning" %}}).