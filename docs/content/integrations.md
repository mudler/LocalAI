+++
disableToc = false
title = "Integrations"
weight = 19
icon = "sync"

+++

## Community Integrations

The lists below cover software and community projects that integrate with LocalAI.

Feel free to open up a Pull request (by clicking at the "Edit page" below) to get your project added!

### Build & Deploy

- [aikit](https://github.com/sozercan/aikit) — Build and deploy custom LocalAI containers
- [Helm chart](https://github.com/go-skynet/helm-charts) — Deploy LocalAI on Kubernetes
- [GitHub Actions](https://github.com/marketplace/actions/start-localai) — Use LocalAI in CI/CD workflows

### Web UIs

- [localai-admin](https://github.com/Jirubizu/localai-admin)
- [LocalAI-frontend](https://github.com/go-skynet/LocalAI-frontend)
- [QA-Pilot](https://github.com/reid41/QA-Pilot) — Interactive chat for navigating GitHub code repositories
- [Big AGI](https://github.com/enricoros/big-agi) — Powerful web interface running entirely in the browser

### Agentic Libraries & Assistants

- [cogito](https://github.com/mudler/cogito) — Agentic library for Go
- [LocalAGI](https://github.com/mudler/LocalAGI) — Local smart assistant with autonomous agents

### MCP Servers

- [MCPs](https://github.com/mudler/MCPs) — Model Context Protocol servers

### OS Assistants

- [Keygeist](https://github.com/mudler/Keygeist) — AI-powered keyboard operator for Linux

### Voice

- [VoxInput](https://github.com/richiejp/VoxInput) — Use voice to control your desktop

### IDE & Editor Plugins

- [VSCode extension](https://github.com/badgooooor/localai-vscode-plugin)
- [GPTLocalhost (Word Add-in)](https://gptlocalhost.com/demo#LocalAI) — Run LocalAI in Microsoft Word locally

### Framework Integrations

- [Langchain (Python)](https://python.langchain.com/docs/integrations/providers/localai/) — [pypi](https://pypi.org/project/langchain-localai/)
- [langchain4j](https://github.com/langchain4j/langchain4j) — Java LangChain
- [lingoose](https://github.com/henomis/lingoose) — Go framework for LLM apps
- [LLPhant](https://github.com/theodo-group/LLPhant) — PHP library for LLMs and vector databases
- [FlowiseAI](https://github.com/FlowiseAI/Flowise) — Low-code LLM app builder
- [LLMStack](https://github.com/trypromptly/LLMStack)
- [Midori AI Subsystem Manager](https://io.midori-ai.xyz/subsystem/manager/)

### Terminal Tools

- [ShellOracle](https://github.com/djcopley/ShellOracle) — Terminal utility
- [Shell-Pilot](https://github.com/reid41/shell-pilot) — Interact with LLMs via pure shell scripts
- [Mods](https://github.com/charmbracelet/mods) — AI on the command line

### Chat Bots

- [Discord bot](https://github.com/mudler/LocalAGI/tree/main/examples/discord)
- [Slack bot](https://github.com/mudler/LocalAGI/tree/main/examples/slack)
- [Telegram bot](https://github.com/mudler/LocalAI/tree/master/examples/telegram-bot)
- [Hellper (Telegram)](https://github.com/JackBekket/Hellper)

### Home Automation

- [hass-openai-custom-conversation](https://github.com/drndos/hass-openai-custom-conversation) — Home Assistant integration
- [ha-llmvision](https://github.com/valentinfrlch/ha-llmvision) — Home Assistant LLM Vision
- [HA-LocalAI-Monitor](https://github.com/loryanstrant/HA-LocalAI-Monitor) — Home Assistant monitoring
- Nextcloud [integration plugin](https://apps.nextcloud.com/apps/integration_openai) and [AI assistant](https://apps.nextcloud.com/apps/assistant)

### Automation & DevOps

- [Reflexia](https://github.com/JackBekket/Reflexia) — Auto-documentation
- [GitHelper](https://github.com/JackBekket/GitHelper) — GitHub bot for issues with code and documentation context
- [kairos](https://github.com/kairos-io/kairos) — Immutable Linux OS

### Other Integrations

- [AnythingLLM](https://github.com/Mintplex-Labs/anything-llm)
- [Logseq GPT3 OpenAI plugin](https://github.com/briansunter/logseq-plugin-gpt3-openai)
- [CodeGPT (JetBrains)](https://plugins.jetbrains.com/plugin/21056-codegpt) — Custom OpenAI-compatible endpoints
- [Wave Terminal](https://docs.waveterm.dev/features/supportedLLMs/localai) — Native LocalAI support
- [Obsidian BMO Chatbot](https://github.com/longy2k/obsidian-bmo-chatbot)
- [spark](https://github.com/cedriking/spark)
- [openops (Mattermost)](https://github.com/mattermost/openops)
- [Model Gallery](https://github.com/go-skynet/model-gallery)
- [Examples](https://github.com/mudler/LocalAI/tree/master/examples/)

## Configuration Guides

This section provides step-by-step instructions for configuring specific software to work with LocalAI.

### OpenCode

[OpenCode](https://opencode.ai) is an AI-powered code editor that can be configured to use LocalAI as its backend provider.

#### Prerequisites

- LocalAI must be running and accessible (either locally or on a network)
- You need to know your LocalAI server's IP address/hostname and port (default is `8080`)

#### Configuration Steps

1. **Edit the OpenCode configuration file**

   Open the OpenCode configuration file located at `~/.config/opencode/opencode.json` in your editor.

2. **Add LocalAI provider configuration**

   Add the following configuration to your `opencode.json` file, replacing the values with your own:

   ```json
   {
     "$schema": "https://opencode.ai/config.json",
     "provider": {
       "LocalAI": {
         "npm": "@ai-sdk/openai-compatible",
         "name": "LocalAI (local)",
         "options": {
           "baseURL": "http://127.0.0.1:8080/v1"
         },
         "models": {
           "Qwen3-Coder-30B-A3B-Instruct-i1-GGUF": {
             "name": "Qwen3-Coder-30B-A3B-Instruct-i1-GGUF",
             "limit": {
               "context": 38000,
               "output": 65536
             }
           },
           "qwen_qwen3-30b-a3b-instruct-2507": {
             "name": "qwen_qwen3-30b-a3b-instruct-2507",
             "limit": {
               "context": 38000,
               "output": 65536
             }
           }
         }
       }
     }
   }
   ```

3. **Customize the configuration**

   - **baseURL**: Replace `http://127.0.0.1:8080/v1` with your LocalAI server's address and port.
   - **name**: Change "LocalAI (local)" to a descriptive name for your setup.
   - **models**: Replace the model names with the actual model names available in your LocalAI instance. You can find available models by checking your LocalAI models directory or using the LocalAI API.
   - **limit**: Adjust the `context` and `output` token limits based on your model's capabilities and available resources.

4. **Verify your models**

   Ensure that the model names in the configuration match exactly with the model names configured in your LocalAI instance. You can verify available models by checking your LocalAI configuration or using the `/v1/models` endpoint.

5. **Restart OpenCode**

   After saving the configuration file, restart OpenCode for the changes to take effect.


### Charm Crush

You can ask [Charm Crush](https://charm.land/crush) to generate your config by giving it this documentation's URL and your LocalAI instance URL. The configuration will look something like the following and goes in `~/.config/crush/crush.json`:
```json
{
  "$schema": "https://charm.land/crush.json",
  "providers": {
    "localai": {
      "name": "LocalAI",
      "base_url": "http://localai.lan:8081/v1",
      "type": "openai-compat",
      "models": [
        {
          "id": "qwen3-coder-480b-a35b-instruct",
          "name": "Qwen 3 Coder 480b",
          "context_window": 256000
        },
        {
          "id": "qwen3-30b-a3b",
          "name": "Qwen 3 30b a3b",
          "context_window": 32000
        }
      ]
    }
  }
}
```

A list of models can be fetched with `https://<server_address>/v1/models` by crush itself and appropriate models added to the provider list. Crush does not appear to be optimized for smaller models.

### GitHub Actions

You can use LocalAI in GitHub Actions workflows to perform AI-powered tasks like code review, diff summarization, or automated analysis. The [LocalAI GitHub Action](https://github.com/mudler/localai-github-action) makes it easy to spin up a LocalAI instance in your CI/CD pipeline.

#### Prerequisites

- A GitHub repository with Actions enabled
- A model name from [models.localai.io](https://models.localai.io) or a Hugging Face model reference

#### Example Workflow

This example workflow demonstrates how to use LocalAI to summarize pull request diffs and send notifications:

1. **Create a workflow file**

   Create a new file in your repository at `.github/workflows/localai.yml`:

```yaml
name: Use LocalAI in GHA
on:
  pull_request:
     types:
       - closed

jobs:
  notify-discord:
    if: ${{ (github.event.pull_request.merged == true) && (contains(github.event.pull_request.labels.*.name, 'area/ai-model')) }}
    env:
        MODEL_NAME: qwen_qwen3-4b-instruct-2507
    runs-on: ubuntu-latest
    steps:
    - uses: actions/checkout@v4
      with:
        fetch-depth: 0 # needed to checkout all branches for this Action to work
    # Starts the LocalAI container
    - id: foo
      uses: mudler/localai-github-action@v1.1
      with:
        model: 'qwen_qwen3-4b-instruct-2507' # Any from models.localai.io, or from huggingface.com with: "huggingface://<repository>/file"
    # Check the PR diff using the current branch and the base branch of the PR
    - uses: GrantBirki/git-diff-action@v2.7.0
      id: git-diff-action
      with:
            json_diff_file_output: diff.json
            raw_diff_file_output: diff.txt
            file_output_only: "true"
    # Ask to explain the diff to LocalAI
    - name: Summarize
      env:
        DIFF: ${{ steps.git-diff-action.outputs.raw-diff-path }}
      id: summarize
      run: |
            input="$(cat $DIFF)"

            # Define the LocalAI API endpoint
            API_URL="http://localhost:8080/chat/completions"

            # Create a JSON payload using jq to handle special characters
            json_payload=$(jq -n --arg input "$input" '{
            model: "'$MODEL_NAME'",
            messages: [
                {
                role: "system",
                content: "Write a message summarizing the change diffs"
                },
                {
                role: "user",
                content: $input
                }
            ]
            }')

            # Send the request to LocalAI
            response=$(curl -s -X POST $API_URL \
            -H "Content-Type: application/json" \
            -d "$json_payload")

            # Extract the summary from the response
            summary="$(echo $response | jq -r '.choices[0].message.content')"

            # Print the summary
            echo "Summary:"
            echo "$summary"
            echo "payload sent"
            echo "$json_payload"
            {
                echo 'message<<EOF'
                echo "$summary"
                echo EOF
              } >> "$GITHUB_OUTPUT"
    # Send the summary somewhere (e.g. Discord)
    - name: Discord notification
      env:
        DISCORD_WEBHOOK: ${{ secrets.DISCORD_WEBHOOK_URL }}
        DISCORD_USERNAME: "discord-bot"
        DISCORD_AVATAR: ""
      uses: Ilshidur/action-discord@master
      with:
        args: ${{ steps.summarize.outputs.message }}
```

#### Configuration Options

- **Model selection**: Replace `qwen_qwen3-4b-instruct-2507` with any model from [models.localai.io](https://models.localai.io). You can also use Hugging Face models by using the full huggingface model url`.
- **Trigger conditions**: Customize the `if` condition to control when the workflow runs. The example only runs when a PR is merged and has a specific label.
- **API endpoint**: The LocalAI container runs on `http://localhost:8080` by default. The action exposes the service on the standard port.
- **Custom prompts**: Modify the system message in the JSON payload to change what LocalAI is asked to do with the diff.

#### Use Cases

- **Code review automation**: Automatically review code changes and provide feedback
- **Diff summarization**: Generate human-readable summaries of code changes
- **Documentation generation**: Create documentation from code changes
- **Security scanning**: Analyze code for potential security issues
- **Test generation**: Generate test cases based on code changes

#### Additional Resources

- [LocalAI GitHub Action repository](https://github.com/mudler/localai-github-action)
- [Available models](https://models.localai.io)
- [LocalAI API documentation](/reference/)

### Realtime Voice Assistant

LocalAI supports realtime voice interactions , enabling voice assistant applications with real-time speech-to-speech communication. A complete example implementation is available in the [LocalAI-examples repository](https://github.com/mudler/LocalAI-examples/tree/main/realtime).

#### Overview

The realtime voice assistant example demonstrates how to build a voice assistant that:
- Captures audio input from the user in real-time
- Transcribes speech to text using LocalAI's transcription capabilities
- Processes the text with a language model
- Generates audio responses using text-to-speech
- Streams audio back to the user in real-time

#### Prerequisites

- A transcription model (e.g., Whisper) configured in LocalAI
- A text-to-speech model configured in LocalAI
- A language model for generating responses

#### Getting Started

1. **Clone the example repository**

   ```bash
   git clone https://github.com/mudler/LocalAI-examples.git
   cd LocalAI-examples/realtime
   ```

2. **Start LocalAI with Docker Compose**

   ```bash
   docker compose up -d
   ```

   The first time you start docker compose, it will take a while to download the available models. You can follow the model downloads in real-time:

   ```bash
   docker logs -f realtime-localai-1
   ```

3. **Install host dependencies**

   Install the required host dependencies (sudo is required):

   ```bash
   sudo bash setup.sh
   ```

4. **Run the voice assistant**

   Start the voice assistant application:

   ```bash
   bash run.sh
   ```

#### Configuration Notes

- **CPU vs GPU**: The example is optimized for CPU usage. However, you can run LocalAI with a GPU for better performance and to use bigger/better models.
- **Python client**: The Python part downloads PyTorch for CPU, but this is fine as computation is offloaded to LocalAI. The Python client only runs Silero VAD (Voice Activity Detection), which is fast, and handles audio recording.
- **Thin client architecture**: The Python client is designed to run on thin clients such as Raspberry PIs, while LocalAI handles the heavier computational workload on a more powerful machine.

#### Key Features

- **Real-time processing**: Low-latency audio streaming for natural conversations
- **Voice Activity Detection (VAD)**: Automatic detection of when the user is speaking
- **Turn-taking**: Handles conversation flow with proper turn detection
- **OpenAI-compatible API**: Uses LocalAI's OpenAI-compatible realtime API endpoints

#### Use Cases

- **Voice assistants**: Build custom voice assistants for home automation or productivity
- **Accessibility tools**: Create voice interfaces for accessibility applications
- **Interactive applications**: Add voice interaction to games, educational software, or entertainment apps
- **Customer service**: Implement voice-based customer support systems

#### Additional Resources

- [Realtime Voice Assistant Example](https://github.com/mudler/LocalAI-examples/tree/main/realtime)
- [LocalAI Realtime API documentation](/features/)
- [Audio features documentation](/features/text-to-audio/)
- [Transcription features documentation](/features/audio-to-text/)
