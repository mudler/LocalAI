 
+++
disableToc = false
title = "Customize model"
weight = 2
icon = "rocket_launch"

+++

LocalAI can be started (either the container image or the binary) with a list of model config files URLs or our short-handed format (e.g. `huggingface://`. `github://`). It works by passing the urls as arguments or environment variable, for example:

```
local-ai github://owner/repo/file.yaml@branch

# Env
MODELS="github://owner/repo/file.yaml@branch,github://owner/repo/file.yaml@branch" local-ai

# Args
local-ai --models github://owner/repo/file.yaml@branch --models github://owner/repo/file.yaml@branch
```

For example, to start localai with phi-2, it's possible for instance to also use a full config file from gists:

```bash
docker run -p 8080:8080 localai/localai:{{< version >}}-ffmpeg-core https://gist.githubusercontent.com/mudler/ad601a0488b497b69ec549150d9edd18/raw/a8a8869ef1bb7e3830bf5c0bae29a0cce991ff8d/phi-2.yaml
```

The file should be a valid LocalAI YAML configuration file, for the full syntax see [advanced]({{%relref "docs/advanced" %}}).