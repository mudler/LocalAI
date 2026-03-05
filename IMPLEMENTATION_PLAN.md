# WebSocket Mode Implementation Plan for OpenAI Responses API

## Overview
Implement WebSocket support for LocalAI's OpenAI API-compatible Responses endpoint, enabling persistent WebSocket connections for long-running, tool-call-heavy agentic workflows.

## Technical Requirements

### 1. WebSocket Endpoint
- **Endpoint**: `ws://<host>:<port>/v1/responses`
- **Upgrade**: HTTP upgrade from POST /v1/responses when `Upgrade: websocket` header is present

### 2. Message Types (Client → Server)

#### response.create (Initial Turn)
```json
{
  "type": "response.create",
  "model": "gpt-4o",
  "store": false,
  "input": [...],
  "tools": []
}
```

#### response.create with Continuation (Subsequent Turns)
```json
{
  "type": "response.create",
  "model": "gpt-4o",
  "store": false,
  "previous_response_id": "resp_123",
  "input": [...],
  "tools": []
}
```

### 3. Response Events (Server → Client)

1. **response.created** - Response object created
2. **response.progress** - Incremental output
3. **response.function_call_arguments.delta** - Streaming function arguments
4. **response.function_call_arguments.done** - Function call complete
5. **response.done** - Final response

### 4. Connection Management
- Track active connections with 60-minute timeout
- Connection-local cache for responses (when store=false)
- One in-flight response at a time per connection

### 5. Error Handling
- `previous_response_not_found` (400)
- `websocket_connection_limit_reached` (400)

## Implementation Steps

### Step 1: Add WebSocket Schema Types
- Add WebSocket message types to `core/schema/openresponses.go`
- Add connection-related types

### Step 2: Add WebSocket Route
- Modify `core/http/routes/openresponses.go` to handle WebSocket upgrade
- Add GET /v1/responses WebSocket endpoint

### Step 3: Create WebSocket Handler
- Create `core/http/endpoints/openresponses/websocket.go`
- Implement connection handling
- Implement message parsing
- Implement event streaming

### Step 4: Add Connection Store
- Implement connection management in store
- Add 60-minute timeout
- Add connection-local cache

## Files to Modify/Create
1. `core/schema/openresponses.go` - Add WebSocket types
2. `core/http/routes/openresponses.go` - Add WebSocket route
3. `core/http/endpoints/openresponses/websocket.go` - New WebSocket handler (create)
4. `core/http/endpoints/openresponses/store.go` - Add connection management
