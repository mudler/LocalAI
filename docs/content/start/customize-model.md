+++
disableToc = false
title = "Customizing the Model"
icon = "rocket_launch"

weight = 120
aliases = ["/getting-started/customize-model/"]
description = "Edit the configuration and the prompt template of an installed model."
keywords = ["model configuration","prompt template","yaml"]
+++

A model configuration file sets the prompt template and the default settings of a model. It uses the LocalAI YAML format. For the syntax, see [Advanced usage]({{%relref "/models/advanced-usage" %}}). The file can be on the local file system, or at a URL such as a GitHub Gist.

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

{{% callout style="tip" %}}
The model configurations used in the quickstart are accessible here: [https://github.com/mudler/LocalAI/tree/master/gallery](https://github.com/mudler/LocalAI/tree/master/gallery). Contributions are welcome; please feel free to submit a Pull Request.

The `phi-2` model configuration from the quickstart is expanded from [https://github.com/mudler/LocalAI-examples/blob/main/configurations/phi-2.yaml](https://github.com/mudler/LocalAI-examples/blob/main/configurations/phi-2.yaml).
 {{% /callout %}}

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
docker run -p 8080:8080 localai/localai:{{< version >}} https://gist.githubusercontent.com/xxxx/phi-2.yaml
```

## Next Steps

- Visit the [advanced section]({{%relref "/models/advanced-usage" %}}) for more insights on prompt templates and configuration files.
- To learn about fine-tuning an LLM model, check out the [fine-tuning section]({{%relref "/features/model-tools/fine-tuning" %}}).