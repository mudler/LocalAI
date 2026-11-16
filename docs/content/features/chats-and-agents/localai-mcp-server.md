+++
url = "/features/localai-mcp-server/"
disableToc = false
title = "LocalAI MCP server"
description = "Serve the LocalAI tools over stdio to control a LocalAI instance from an MCP host such as a desktop app or an editor."
weight = 62
devbuild = "feature"
+++

Run the LocalAI tools as a stdio MCP server and point it at any LocalAI instance. An MCP host, such as a desktop app or an editor, can then manage that instance. The management tools need an administrator key. The client tools need a user key.

## Standalone stdio MCP server

You can run the same admin tool surface as a stdio MCP server pointed at any LocalAI HTTP API:

```bash
local-ai mcp-server --target http://remote.localai:8080 --api-key <admin-key>
# read-only mode - skips registration of every mutating tool and of the client tools that write files or make content
local-ai mcp-server --target http://remote.localai:8080 --read-only
```

Useful for hooking LocalAI admin into Claude Desktop, Cursor, or any MCP host. The tool catalog is identical to the in-process variant, including the [client tools]({{% relref "/features/chats-and-agents/localai-client-tools#client-tools" %}}).
