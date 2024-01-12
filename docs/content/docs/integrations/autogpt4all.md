
+++
disableToc = false
title = "AutoGPT4all"
weight = 2
+++

AutoGPT4All provides you with both bash and python scripts to set up and configure [AutoGPT](https://github.com/Significant-Gravitas/Auto-GPT.git) running with the [GPT4All](#) model on the [LocalAI](https://github.com/go-skynet/LocalAI) server. This setup allows you to run queries against an open-source licensed model without any limits, completely free and offline.

![photo](https://bafkreif7cbmuvhztfdlscnmgi3ob32d6ulkqgbjqy4cff2krth4dynwwhe.ipfs.nftstorage.link)

Github Link - https://github.com/aorumbayev/autogpt4all

## 🚀 Quickstart

## Using Bash Script:

```sh
git clone https://github.com/aorumbayev/autogpt4all.git
cd autogpt4all
chmod +x autogpt4all.sh
./autogpt4all.sh
```

### Using Python Script:

Make sure you have Python installed on your machine.

```sh
git clone https://github.com/aorumbayev/autogpt4all.git
cd autogpt4all
python autogpt4all.py
```

> ❗️ Please note this script has been primarily tested on MacOS with an M1 processor. It should work on Linux and Windows, but it has not been thoroughly tested on these platforms. If not on MacOS install git, go and make before running the script.

## 🎛️ Script Options

## For the bash script:

`--custom_model_url` - Specify a custom URL for the model download step. By default, the script will use https://gpt4all.io/models/ggml-gpt4all-l13b-snoozy.bin.

Example:

```
./autogpt4all.sh --custom_model_url "https://example.com/path/to/model.bin"
```

`--uninstall` - Uninstall the projects from your local machine by deleting the LocalAI and Auto-GPT directories.

Example:

```
./autogpt4all.sh --uninstall
```

> To recap the commands, a --help flag is also available for the bash script.

## For the Python Script:

You can use similar options as the bash script:

`--custom_model_url` - Specify a custom URL for the model download step.

Example:

```sh
python autogpt4all.py --custom_model_url "https://example.com/path/to/model.bin"
```

`--uninstall` - Uninstall the projects from your local machine.

Example:

```sh
python autogpt4all.py --uninstall
```
