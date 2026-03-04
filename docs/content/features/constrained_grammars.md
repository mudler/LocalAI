+++
disableToc = false
title = "✍️ Constrained Grammars"
weight = 15
url = "/features/constrained_grammars/"
+++

## Overview

The `chat` endpoint supports the `grammar` parameter, which allows users to specify a grammar in Backus-Naur Form (BNF). This feature enables the Large Language Model (LLM) to generate outputs adhering to a user-defined schema, such as `JSON`, `YAML`, or any other format that can be defined using BNF. For more details about BNF, see [Backus-Naur Form on Wikipedia](https://en.wikipedia.org/wiki/Backus%E2%80%93Naur_form).

{{% notice note %}}
**Compatibility Notice:** This feature is only supported by models that use the [llama.cpp](https://github.com/ggerganov/llama.cpp) backend. For a complete list of compatible models, refer to the [Model Compatibility]({{%relref "reference/compatibility-table" %}}) page. For technical details, see the related pull requests: [PR #1773](https://github.com/ggerganov/llama.cpp/pull/1773) and [PR #1887](https://github.com/ggerganov/llama.cpp/pull/1887).
 {{% /notice %}}

## Setup

To use this feature, follow the installation and setup instructions on the [LocalAI Functions]({{%relref "features/openai-functions" %}}) page. Ensure that your local setup meets all the prerequisites specified for the llama.cpp backend.

## 💡 Usage Example

The following example demonstrates how to use the `grammar` parameter to constrain the model's output to either "yes" or "no". This can be particularly useful in scenarios where the response format needs to be strictly controlled.

### Example: Binary Response Constraint

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "gpt-4",
  "messages": [{"role": "user", "content": "Do you like apples?"}],
  "grammar": "root ::= (\"yes\" | \"no\")"
}'
```

In this example, the `grammar` parameter is set to a simple choice between "yes" and "no", ensuring that the model's response adheres strictly to one of these options regardless of the context.

### Example: JSON Output Constraint

You can also use grammars to enforce JSON output format:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "gpt-4",
  "messages": [{"role": "user", "content": "Generate a person object with name and age"}],
  "grammar": "root ::= \"{\" \"\\\"name\\\":\" string \",\\\"age\\\":\" number \"}\"\nstring ::= \"\\\"\" [a-z]+ \"\\\"\"\nnumber ::= [0-9]+"
}'
```

### Example: YAML Output Constraint

Similarly, you can enforce YAML format:

```bash
curl http://localhost:8080/v1/chat/completions -H "Content-Type: application/json" -d '{
  "model": "gpt-4",
  "messages": [{"role": "user", "content": "Generate a YAML list of fruits"}],
  "grammar": "root ::= \"fruits:\" newline (\"  - \" string newline)+\nstring ::= [a-z]+\nnewline ::= \"\\n\""
}'
```

## Advanced Usage

For more complex grammars, you can define multi-line BNF rules. The grammar parser supports:
- Alternation (`|`)
- Repetition (`*`, `+`)
- Optional elements (`?`)
- Character classes (`[a-z]`)
- String literals (`"text"`)

## Related Features

- [OpenAI Functions]({{%relref "features/openai-functions" %}}) - Function calling with structured outputs
- [Text Generation]({{%relref "features/text-generation" %}}) - General text generation capabilities