+++
disableToc = false
title = "Customizing the Model"
weight = 4
icon = "rocket_launch"

+++

To customize the prompt template or the default settings of the model, a configuration file is utilized. This file must adhere to the LocalAI YAML configuration standards. For comprehensive syntax details, refer to the [advanced documentation]({{%relref "docs/advanced" %}}). The configuration file can be located either remotely (such as in a Github Gist) or within the local filesystem or a remote URL.

LocalAI can be initiated using either its container image or binary, with a command that includes URLs of model config files or utilizes a shorthand format (like `huggingface://` or `github://`), which is then expanded into complete URLs.

The configuration can also be set via an environment variable. For instance:

```
# Command-Line Arguments
local-ai github://owner/repo/file.yaml@branch

# Environment Variable
MODELS="github://owner/repo/file.yaml@branch,github://owner/repo/file.yaml@branch" local-ai
```

Here's an example to initiate the **phi-2** model:

```bash
docker run -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core https://gist.githubusercontent.com/mudler/ad601a0488b497b69ec549150d9edd18/raw/a8a8869ef1bb7e3830bf5c0bae29a0cce991ff8d/phi-2.yaml
```

You can also check all the embedded models configurations [here](https://github.com/mudler/LocalAI/tree/master/embedded/models).

{{% alert icon="" %}}
The model configurations used in the quickstart are accessible here: [https://github.com/mudler/LocalAI/tree/master/embedded/models](https://github.com/mudler/LocalAI/tree/master/embedded/models). Contributions are welcome; please feel free to submit a Pull Request.

The `phi-2` model configuration from the quickstart is expanded from [https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml](https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml).
{{% /alert %}}

## Example: Customizing the Prompt Template

To modify the prompt template, create a Github gist or a Pastebin file, and copy the content from [https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml](https://github.com/mudler/LocalAI/blob/master/examples/configurations/phi-2.yaml). Alter the fields as needed:

```yaml
name: phi-2
context_size: 2048
f16: true
threads: 11
gpu_layers: 90
mmap: true
parameters:
  # Reference any HF model or a local file here
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
docker run -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core https://gist.githubusercontent.com/xxxx/phi-2.yaml
```

## Next Steps

- Visit the [advanced section]({{%relref "docs/advanced" %}}) for more insights on prompt templates and configuration files.
- To learn about fine-tuning an LLM model, check out the [fine-tuning section]({{%relref "docs/advanced/fine-tuning" %}}).