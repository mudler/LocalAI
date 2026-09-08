/**
 * Normalize a chat UI system prompt for the /v1/chat/completions payload.
 * Empty / whitespace-only values must be omitted so the backend can apply the
 * model's configured system_prompt (and tokenizer chat templates are not fed
 * an explicit blank system turn).
 */
export function effectiveSystemPrompt(prompt) {
  if (typeof prompt !== 'string') return ''
  return prompt.trim()
}

/**
 * True when the UI should include a system message in the request.
 */
export function shouldSendSystemPrompt(prompt) {
  return effectiveSystemPrompt(prompt) !== ''
}
