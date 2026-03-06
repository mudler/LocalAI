# Investigation Report for Issue #8620

## Issue Summary
User reports that when using the mistral model with MCP, the model returns the tool call JSON (`pick_tool{...}`) directly to the user instead of executing the tool and returning the final result.

## Root Cause Analysis

After examining the codebase:

1. **The Problem**: The OpenAI chat endpoint (`core/http/endpoints/openai/chat.go`) does NOT implement an agent loop for tool execution.

2. **How it currently works**:
   - When tools are detected in the response, it sets `FinishReasonToolCalls`
   - Returns the tool call to the client immediately
   - Does NOT continue to execute the tool or return the final result

3. **Where agent loop IS implemented**:
   - MCP endpoint (`core/http/endpoints/localai/mcp.go`) - uses cogito
   - Open Responses endpoint (`core/http/endpoints/openresponses/responses.go`) - uses cogito  
   - Agent jobs service (`core/services/agent_jobs.go`) - uses cogito

4. **The user's configuration** (from issue):
   - Uses `json_regex_match` to extract tool calls: `(?s)\[TOOL_CALLS\](.*)`
   - Has MCP configured with remote server
   - Has agent config with `max_attempts: 3`, `max_iterations: 3`, `loop_detection: 3`
   - The model correctly formats tool calls but agent doesn't execute them

## Previous PR #8687 Analysis
The PR was closed by the contributor (localai-bot) without merging, suggesting:
- The fix approach may have been incorrect
- Or there were concerns about breaking changes
- The PR only modified `pkg/functions/parse.go` (82 additions)

## Recommended Fix Approach

The proper fix should:
1. Detect when MCP is enabled in the model configuration
2. Detect when agent configuration is present (max_attempts, max_iterations)
3. When both conditions are met AND tools are called, use cogito's ExecuteTools to:
   - Execute the tool
   - Handle the result
   - Continue the loop until max_attempts or final result
4. Return only the final result to the user

This is a significant change to the OpenAI chat endpoint and needs careful consideration to:
- Not break existing behavior for users without MCP
- Properly handle streaming vs non-streaming modes
- Respect the agent configuration settings

## Next Steps
This investigation reveals the issue is real and requires a substantial fix. The previous approach (PR #8687) was closed, so a new approach is needed that properly integrates the agent loop into the OpenAI chat endpoint.
