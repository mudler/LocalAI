# Spike Report: Canvas/Artifact Support in LocalAI Chat UI

**Date:** 2026-03-09
**Status:** Research Complete
**Author:** Spike Analysis

---

## 1. Current State Analysis

### Frontend Architecture

LocalAI has a **React 19-based SPA** (`core/http/react-ui/`) built with Vite, alongside a legacy Alpine.js UI (`core/http/static/chat.js`, `core/http/views/chat.html`).

**Key technology stack:**
- React 19.1.0 + React Router 7.6.1
- Vite 6.3.5 build system
- `marked` 15.0.7 for markdown parsing
- `highlight.js` 11.11.1 for code syntax highlighting
- `dompurify` 3.2.5 for XSS prevention
- Custom CSS with CSS variables for theming (no component library)
- LocalStorage for chat persistence (key: `localai_chats_data`)

**Core UI files:**
| File | Purpose | Size |
|------|---------|------|
| `react-ui/src/pages/Chat.jsx` | Main chat page component | ~34KB, 853 lines |
| `react-ui/src/hooks/useChat.js` | Chat state management hook | ~19KB |
| `react-ui/src/utils/markdown.js` | Markdown rendering + highlight | Small utility |
| `react-ui/src/utils/api.js` | API client functions | ~13KB |
| `react-ui/src/App.css` | All application styles | ~41KB |

### Current Chat Layout

The chat interface is a **single-column message stream** with:
- **Left sidebar:** Chat list with search, rename, delete, export
- **Center:** Message history (user, assistant, thinking, tool_call, tool_result roles)
- **Right drawers:** Settings panel and model info panel (slide-out overlays, not persistent panels)

### Streaming Implementation

**Backend (Go):** SSE via `text/event-stream` in `core/http/endpoints/openai/chat.go`
- Goroutine-based async processing with `responses` and `ended` channels
- Format: `data: <JSON>\n\n` with `data: [DONE]\n\n` terminator
- WebSocket alternative exists at `/v1/responses` (Open Responses API)

**Frontend (React):** `useChat.js` hook manages streaming state
- Tracks `streamingContent`, `streamingReasoning`, `streamingToolCalls` separately
- Supports MCP mode with typed events (`reasoning`, `tool_call`, `tool_result`, `assistant`)
- Regular mode parses `delta.content` from OpenAI-compatible SSE chunks
- Extracts `<thinking>`/`<think>` tags client-side when reasoning isn't API-level

### Message Schema (Backend)

```go
// core/schema/message.go
type Message struct {
    Role         string      `json:"role,omitempty"`
    Name         string      `json:"name,omitempty"`
    Content      interface{} `json:"content"`
    FunctionCall interface{} `json:"function_call,omitempty"`
    ToolCalls    []ToolCall  `json:"tool_calls,omitempty"`
    Reasoning    *string     `json:"reasoning,omitempty"`
}
```

### Existing Split-Content Features

The UI already has precedent for rendering content outside the main message flow:
1. **Thinking/Reasoning blocks** - Collapsible `<details>`-style boxes extracted from streaming
2. **Tool calls & results** - Expandable boxes with wrench icons showing JSON
3. **Code blocks** - Syntax-highlighted via highlight.js within markdown rendering
4. **Settings drawer** - Slides out from the right side
5. **CodeEditor component** (`components/CodeEditor.jsx`) - YAML editor with syntax highlighting (used in model config, not chat)

### What Does NOT Exist

- No canvas, artifact, or side-panel content rendering
- No split-pane layout for chat + content
- No interactive code editing within chat messages
- No document preview or live-render panel
- No content type system beyond message roles

---

## 2. Technical Requirements for Canvas Support

### 2.1 Content Detection & Classification

The system needs to identify content that should be rendered in a canvas panel. Two approaches:

**A. Backend-driven (Recommended):** Detect artifacts during streaming via markers/tags
- Parse `<artifact>` or similar XML tags in LLM output (like thinking tag extraction)
- Add artifact metadata to streaming events (type, title, language)
- Extend SSE events to include artifact deltas

**B. Client-driven:** Post-process rendered messages to extract code blocks or documents
- Simpler but less flexible; can't stream artifacts separately
- Would use heuristics (e.g., large code blocks > N lines)

### 2.2 New UI Components Required

| Component | Purpose |
|-----------|---------|
| `CanvasPanel.jsx` | Right-side panel for rendering artifacts |
| `CanvasSplitLayout.jsx` | Resizable split-pane layout (chat + canvas) |
| `ArtifactRenderer.jsx` | Renders different artifact types (code, markdown, HTML, SVG) |
| `ArtifactToolbar.jsx` | Copy, download, edit, version controls |
| `ArtifactCodeEditor.jsx` | Interactive code editor (extend existing CodeEditor beyond YAML) |

### 2.3 State Management Extensions

The `useChat.js` hook needs:
- `activeArtifact` - currently displayed artifact in canvas
- `artifacts[]` - list of artifacts in current chat (with versions)
- `streamingArtifact` - artifact content being streamed
- Artifact persistence in localStorage alongside chat history

### 2.4 Streaming Protocol Extension

Current SSE format:
```
data: {"choices":[{"delta":{"content":"..."}}]}
```

Needed extension (option A - metadata in delta):
```
data: {"choices":[{"delta":{"content":"","artifact":{"id":"a1","type":"code","language":"python","title":"sort.py","content":"def sort..."}}}]}
```

Or (option B - separate events, similar to MCP mode):
```
event: artifact_start
data: {"id":"a1","type":"code","language":"python","title":"sort.py"}

event: artifact_delta
data: {"id":"a1","content":"def sort(arr):\n"}

event: artifact_end
data: {"id":"a1"}
```

### 2.5 Backend Changes

| Area | Change |
|------|--------|
| `core/schema/message.go` | Add `Artifact` struct and field to Message |
| `core/schema/openai.go` | Add artifact fields to Delta/Choice |
| `core/http/endpoints/openai/chat.go` | Parse artifact tags during streaming, emit artifact events |
| `useChat.js` hook | Handle artifact streaming events |
| Chat.jsx | Split-pane layout, artifact click-to-open |

---

## 3. Proposed Architecture

### 3.1 Layout

```
┌──────────┬─────────────────────────┬──────────────────────┐
│          │                         │                      │
│  Chat    │   Chat Messages         │   Canvas Panel       │
│  List    │                         │                      │
│  Sidebar │   [User message]        │   ┌──────────────┐   │
│          │   [Assistant msg        │   │ Artifact      │   │
│          │    with artifact link]  │   │ Toolbar       │   │
│          │   [User message]        │   ├──────────────┤   │
│          │                         │   │              │   │
│          │                         │   │  Content     │   │
│          │                         │   │  Renderer    │   │
│          │                         │   │              │   │
│          │   ┌──────────────────┐  │   │              │   │
│          │   │ Input box        │  │   └──────────────┘   │
│          │   └──────────────────┘  │                      │
└──────────┴─────────────────────────┴──────────────────────┘
```

### 3.2 Artifact Detection Strategy

Since LocalAI works with many different LLM backends and models (not just models trained to emit artifact tags), a hybrid approach is best:

1. **Explicit tags** (primary): If a model outputs `<artifact>` tags (configurable per model template), parse and extract them server-side during streaming — same pattern as `<thinking>` tag extraction
2. **Client-side heuristic** (fallback): Detect large fenced code blocks (```lang ... ```) in rendered messages and offer a "Open in Canvas" button
3. **User-initiated**: "Open in Canvas" action on any code block or message content

### 3.3 Artifact Types

| Type | Renderer | Edit Support |
|------|----------|-------------|
| `code` | Syntax-highlighted editor | Yes (extend CodeEditor) |
| `markdown` | Rendered markdown preview | Yes (split edit/preview) |
| `html` | Sandboxed iframe preview | Yes |
| `svg` | Inline SVG render | View only |
| `mermaid` | Diagram renderer (new dep) | View only |
| `text` | Plain text | Yes |

### 3.4 Data Model

```typescript
interface Artifact {
  id: string
  type: 'code' | 'markdown' | 'html' | 'svg' | 'text'
  title: string
  language?: string          // for code type
  content: string
  version: number
  versions: ArtifactVersion[]
  messageId: string          // link back to chat message
  createdAt: number
  updatedAt: number
}

interface ArtifactVersion {
  version: number
  content: string
  timestamp: number
}
```

### 3.5 Implementation Phases

**Phase 1: Client-side canvas (lowest effort, highest immediate value)**
- Add resizable split-pane layout to Chat.jsx
- Add "Open in Canvas" button to code blocks in rendered messages
- Canvas panel with syntax highlighting, copy, download
- No backend changes needed

**Phase 2: Streaming artifact support**
- Add artifact tag parsing to streaming pipeline (mirrors thinking tag pattern)
- Extend useChat.js to track streaming artifacts
- Auto-open canvas when artifact is detected during streaming
- Artifact versioning (each model response creates a new version)

**Phase 3: Interactive editing**
- Full code editor in canvas panel (multi-language CodeEditor)
- "Apply edit" sends artifact back as context in next message
- Diff view between versions

**Phase 4: Advanced features**
- HTML/SVG live preview
- Mermaid diagram rendering
- Artifact sharing/export
- Model-specific artifact tag configuration

---

## 4. Implementation Complexity Estimate

| Phase | Scope | Effort | Files Changed |
|-------|-------|--------|--------------|
| Phase 1: Client-side canvas | New components + CSS | **Medium** (2-3 days) | 4-6 new files, modify Chat.jsx + App.css |
| Phase 2: Streaming artifacts | Backend parsing + frontend events | **Medium-High** (3-5 days) | chat.go, message.go, openai.go, useChat.js, Chat.jsx |
| Phase 3: Interactive editing | Editor component + context threading | **Medium** (2-3 days) | CodeEditor.jsx expansion, useChat.js, Chat.jsx |
| Phase 4: Advanced features | New renderers + deps | **Medium** (2-4 days) | New components, package.json |

**Total estimate: 9-15 days for full implementation**

### Risk Factors

| Risk | Impact | Mitigation |
|------|--------|-----------|
| Model compatibility | Most models don't emit artifact tags | Client-side heuristic fallback (Phase 1) |
| LocalStorage limits | Artifacts increase storage size | Implement cleanup/pruning; consider IndexedDB for large artifacts |
| Legacy UI parity | Alpine.js UI won't get canvas | Accept divergence; React UI is the future |
| Mobile layout | Split-pane doesn't work on small screens | Canvas as overlay/modal on mobile breakpoints |
| Streaming complexity | Artifact tags may span multiple SSE chunks | Use same incremental parsing approach as thinking tags |

---

## 5. Recommendations

### Start with Phase 1

Phase 1 (client-side canvas) delivers the most value with the least risk:
- **No backend changes** — purely frontend work
- **Works with all models** — uses heuristic detection on rendered code blocks
- **Reversible** — easy to remove if the approach doesn't work
- **Immediate UX improvement** — code blocks get a dedicated viewing/copying experience

### Key Design Decisions to Make Before Implementation

1. **Canvas position:** Right panel (recommended, matches Claude/ChatGPT patterns) vs. bottom panel vs. modal
2. **Persistence:** Should artifacts survive chat reload? (Recommendation: yes, store with chat data)
3. **Legacy UI:** Should the Alpine.js UI get canvas support? (Recommendation: no, focus on React UI)
4. **Artifact tag format:** If implementing Phase 2, choose a tag format. Recommendation: `<artifact type="code" language="python" title="example.py">...</artifact>` — matches the existing `<thinking>` tag pattern

### Dependencies to Add (by phase)

- **Phase 1:** None (use existing highlight.js + CSS)
- **Phase 3:** Consider `monaco-editor` or `codemirror` for full editor experience (~2-5MB bundle increase)
- **Phase 4:** `mermaid` for diagram rendering (~1MB)

### Existing Code to Leverage

- **`ThinkingMessage` component pattern** in Chat.jsx — directly analogous to artifact rendering
- **`extractThinking()` in useChat.js** — pattern for tag extraction during streaming
- **`CodeEditor.jsx`** — existing editor component to extend beyond YAML
- **MCP mode event handling** in useChat.js — pattern for typed streaming events
- **Settings drawer CSS** — animation and positioning patterns for side panels

---

## Appendix: Key File References

| File | Relevance |
|------|-----------|
| `core/http/react-ui/src/pages/Chat.jsx` | Main chat UI — primary modification target |
| `core/http/react-ui/src/hooks/useChat.js` | Chat state/streaming — add artifact state here |
| `core/http/react-ui/src/components/CodeEditor.jsx` | Existing code editor to extend |
| `core/http/react-ui/src/utils/markdown.js` | Markdown rendering — add artifact link injection |
| `core/http/react-ui/src/App.css` | All styles — add canvas layout styles |
| `core/http/endpoints/openai/chat.go` | SSE streaming endpoint — Phase 2 changes |
| `core/schema/message.go` | Message struct — Phase 2 artifact field |
| `core/schema/openai.go` | Response schema — Phase 2 artifact delta |
| `core/http/react-ui/package.json` | Dependencies — Phase 3+ additions |
