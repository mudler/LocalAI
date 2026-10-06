+++
disableToc = false
title = "Customizing the Model"
weight = 5
url = "/getting-started/customize-model"
icon = "rocket_launch"

+++

To customize the prompt template or the default settings of the model, a configuration file is utilized. This file must adhere to the LocalAI YAML configuration standards. For comprehensive syntax details, refer to the [advanced documentation]({{%relref "advanced" %}}). The configuration file can be located either remotely (such as in a Github Gist) or within the local filesystem or a remote URL.

LocalAI can be initiated using either its container image or binary, with a command that includes URLs of model config files or utilizes a shorthand format (like `huggingface://` or `github://`), which is then expanded into complete URLs.

The configuration can also be set via an environment variable. For instance:

```
local-ai github://owner/repo/file.yaml@branch

MODELS="github://owner/repo/file.yaml@branch,github://owner/repo/file.yaml@branch" local-ai
```

Here's an example to initiate the **phi-2** model:

```bash
docker run -p 8080:8080 localai/localai:{{< version >}} https://gist.githubusercontent.com/mudler/ad601a0488b497b69ec549150d9edd18/raw/a8a8869ef1bb7e3830bf5c0bae29a0cce991ff8d/phi-2.yaml
```

You can also check all the embedded models configurations [here](https://github.com/mudler/LocalAI/tree/master/gallery).

{{% notice tip %}}
The model configurations used in the quickstart are accessible here: [https://github.com/mudler/LocalAI/tree/master/gallery](https://github.com/mudler/LocalAI/tree/master/gallery). Contributions are welcome; please feel free to submit a Pull Request.

The `phi-2` model configuration from the quickstart is expanded from [https://github.com/mudler/LocalAI-examples/blob/main/configurations/phi-2.yaml](https://github.com/mudler/LocalAI-examples/blob/main/configurations/phi-2.yaml).
 {{% /notice %}}

## Example: Customizing the Prompt Template

To modify the prompt template, create a Github gist or a Pastebin file, and copy the content from [https://github.com/mudler/LocalAI-examples/blob/main/configurations/phi-2.yaml](https://github.com/mudler/LocalAI-examples/blob/main/configurations/phi-2.yaml). Alter the fields as needed:

```yaml
name: phi-2
context_size: 2048
f16: true
threads: 11
gpu_layers: 90
mmap: true
parameters:
  # Use a Hugging Face file shorthand or a local filename here
  model: huggingface://TheBloke/phi-2-GGUF/phi-2.Q8_0.gguf
  temperature: 0.2
  top_k: 40
  top_p: 0.95
template:
  
  chat: &template |
    Instruct: {{.Input}}
    Output:
  # Modify the prompt template here ^^^ as per your requirements
  completion: *template
```

Then, launch LocalAI using your gist's URL:

```bash
## Important! Substitute with your gist's URL!
docker run -p 8080:8080 localai/localai:{{< version >}} https://gist.githubusercontent.com/xxxx/phi-2.yaml
```

## Download a model from a direct URL

For a model weight file served over HTTP(S), put its URL in `download_files`.
Set `parameters.model` to the local filename, as in this configuration:

```yaml
name: story
backend: llama-cpp
parameters:
  model: ggml-org-stories260K.gguf
download_files:
  - filename: ggml-org-stories260K.gguf
    uri: https://huggingface.co/ggml-org/models/resolve/main/tinyllamas/stories260K.gguf
```

Save this configuration as `story.yaml` in your models directory, then restart LocalAI.
LocalAI downloads the file during model configuration preload if the file is missing.
Choose a filename that does not belong to another model.
You can add the file's complete SHA-256 checksum as `sha256` to check its contents.

{{% notice note %}}
LocalAI does not automatically download direct `http://` or `https://` URLs placed in `parameters.model`.
For file-based backends such as `llama-cpp`, use the `download_files` configuration above or a supported shorthand such as `huggingface://`.
The HTTP(S) URL passed to `local-ai` in the earlier examples points to a YAML configuration, not a model weight file.
{{% /notice %}}

## Next Steps

- Visit the [advanced section]({{%relref "advanced" %}}) for more insights on prompt templates and configuration files.
- To learn about fine-tuning an LLM model, check out the [fine-tuning section]({{%relref "features/fine-tuning" %}}).
