
+++
disableToc = false
title = "LinGoose"
weight = 2
+++

**LinGoose** (_Lingo + Go + Goose_ 🪿) aims to be a complete Go framework for creating LLM apps. 🤖 ⚙️

![lin](https://lingoose.io/assets/img/lingoose-small.png)

Github Link - https://github.com/henomis/lingoose

## Overview

**LinGoose** is a powerful Go framework for developing Large Language Model (LLM) based applications using pipelines. It is designed to be a complete solution and provides multiple components, including Prompts, Templates, Chat, Output Decoders, LLM, Pipelines, and Memory. With **LinGoose**, you can interact with LLM AI through prompts and generate complex templates. Additionally, it includes a chat feature, allowing you to create chatbots. The Output Decoders component enables you to extract specific information from the output of the LLM, while the LLM interface allows you to send prompts to various AI, such as the ones provided by OpenAI. You can chain multiple LLM steps together using Pipelines and store the output of each step in Memory for later retrieval. **LinGoose** also includes a Document component, which is used to store text, and a Loader component, which is used to load Documents from various sources. Finally, it includes TextSplitters, which are used to split text or Documents into multiple parts, Embedders, which are used to embed text or Documents into embeddings, and Indexes, which are used to store embeddings and documents and to perform searches.

## Components

**LinGoose** is composed of multiple components, each one with its own purpose.

| Component         | Package                       | Description                                                                                                                                                                                                                                                                                            |
| ----------------- | ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Prompt**        | [prompt](prompt/)             | Prompts are the way to interact with LLM AI. They can be simple text, or more complex templates. Supports **Prompt Templates** and **[Whisper](https://openai.com) prompt**                                                                                                                            |
| **Chat Prompt**   | [chat](chat/)                 | Chat is the way to interact with the chat LLM AI. It can be a simple text prompt, or a more complex chatbot.                                                                                                                                                                                           |
| **Decoders**      | [decoder](decoder/)           | Output decoders are used to decode the output of the LLM. They can be used to extract specific information from the output. Supports **JSONDecoder** and **RegExDecoder**                                                                                                                              |
| **LLMs**          | [llm](llm/)                   | LLM is an interface to various AI such as the ones provided by OpenAI. It is responsible for sending the prompt to the AI and retrieving the output. Supports **[LocalAI](https://localai.io/howtos/)**, **[HuggingFace](https://huggingface.co)** and **[Llama.cpp](https://github.com/ggerganov/llama.cpp)**. |
| **Pipelines**     | [pipeline](pipeline/)         | Pipelines are used to chain multiple LLM steps together.                                                                                                                                                                                                                                               |
| **Memory**        | [memory](memory/)             | Memory is used to store the output of each step. It can be used to retrieve the output of a previous step. Supports memory in **Ram**                                                                                                                                                                  |
| **Document**      | [document](document/)         | Document is used to store a text                                                                                                                                                                                                                                                                       |
| **Loaders**       | [loader](loader/)             | Loaders are used to load Documents from various sources. Supports **TextLoader**, **DirectoryLoader**, **PDFToTextLoader** and **PubMedLoader** .                                                                                                                                                      |
| **TextSplitters** | [textsplitter](textsplitter/) | TextSplitters are used to split text or Documents into multiple parts. Supports **RecursiveTextSplitter**.                                                                                                                                                                                             |
| **Embedders**     | [embedder](embedder/)         | Embedders are used to embed text or Documents into embeddings. Supports **[OpenAI](https://openai.com)**                                                                                                                                                                                               |
| **Indexes**       | [index](index/)               | Indexes are used to store embeddings and documents and to perform searches. Supports **SimpleVectorIndex**, **[Pinecone](https://pinecone.io)** and **[Qdrant](https://qdrant.tech)**                                                                                                                  |

## Usage

Please refer to the documentation at [lingoose.io](https://lingoose.io/docs/) to understand how to use LinGoose. If you prefer the 👉 [examples directory](examples/) contains a lot of examples 🚀.
However, here is a **powerful** example of what **LinGoose** is capable of:

_Talk is cheap. Show me the [code](examples/)._ - Linus Torvalds

```go
package main

import (
	"context"

	openaiembedder "github.com/henomis/lingoose/embedder/openai"
	"github.com/henomis/lingoose/index/option"
	simplevectorindex "github.com/henomis/lingoose/index/simpleVectorIndex"
	"github.com/henomis/lingoose/llm/openai"
	"github.com/henomis/lingoose/loader"
	qapipeline "github.com/henomis/lingoose/pipeline/qa"
	"github.com/henomis/lingoose/textsplitter"
)

func main() {
	docs, _ := loader.NewPDFToTextLoader("./kb").WithPDFToTextPath("/opt/homebrew/bin/pdftotext").WithTextSplitter(textsplitter.NewRecursiveCharacterTextSplitter(2000, 200)).Load(context.Background())
	index := simplevectorindex.New("db", ".", openaiembedder.New(openaiembedder.AdaEmbeddingV2))
	index.LoadFromDocuments(context.Background(), docs)
	qapipeline.New(openai.NewChat().WithVerbose(true)).WithIndex(index).Query(context.Background(), "What is the NATO purpose?", option.WithTopK(1))
}
```

This is the _famous_ 4-lines **lingoose** knowledge base chatbot. 🤖

## Installation

Be sure to have a working Go environment, then run the following command:

```shell
go get github.com/henomis/lingoose
```
