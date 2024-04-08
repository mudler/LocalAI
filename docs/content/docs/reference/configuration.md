+++
disableToc = false
title = "Configuration"
weight = 27
+++

LocalAI has several options for configuration available to allow for flexability within multiple deployment styles and environments. In addition to the standard `./local-ai run --some-flag=foo` syntax, there are a few other options available to you.

{{% alert note %}}

This documentation page is about the various options that can be used to provide configuration to LocalAI.

The configuration values themselves are documented within the `--help` text of the application.  Please ensure you reference `local-ai run --help` for the most up to date documentation about what configuraiton settings are available.

{{% /alert %}}

## Environment Variables

All CLI flags can be provided via environment variables.  For example, to set LocalAI to load model files from the path `/storage/models` you can invoke it with the following example:

```bash
LOCALAI_LOG_LEVEL=debug ./local-ai run
```

Many flags have two options for environment variables and the option prefixed with `LOCALAI_` is preferred. The non-prefixed versions are present for backwards compatibility and should be considered deprecated.

## .env files

Any settings being provided by an Environment Variable can also be provided from within .env files.  There are several locations that will be checked for relevant .env files. In order of precedence they are:

- .env within the current directory
- localai.env within the current directory
- localai.env within the home directory
- .config/localai.env within the home directory
- /etc/localai.env

Environment variables within files earlier in the list will take precedence over environment variables defined in files later in the list.

An example .env file is:

```
LOCALAI_THREADS=10
LOCALAI_MODELS_PATH=/mnt/storage/localai/models
LOCALAI_F16=true
```