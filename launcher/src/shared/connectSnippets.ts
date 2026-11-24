// The "Set up an app" examples of the Connect screen. Plain code with no
// imports: the renderer shows them with a masked key, and the main process
// builds the same text with the real key for copyApiKey, so the key never
// passes through the renderer.

export type AppKind = 'openai' | 'anthropic' | 'curl'

/** The example for an app type, with `key` as given ("not-needed" when the server has no key). */
export function snippet(kind: AppKind, endpoint: string, key: string | undefined): string {
  const k = key ?? 'not-needed'
  const base = endpoint.replace(/\/+$/, '')
  switch (kind) {
    case 'openai':
      return `export OPENAI_BASE_URL=${base}/v1\nexport OPENAI_API_KEY=${k}`
    case 'anthropic':
      return `export ANTHROPIC_BASE_URL=${base}\nexport ANTHROPIC_API_KEY=${k}`
    case 'curl':
      return [
        `curl ${base}/v1/chat/completions \\`,
        ...(key === undefined ? [] : [`  -H "Authorization: Bearer ${k}" \\`]),
        `  -H "Content-Type: application/json" \\`,
        `  -d '{"model": "<model>", "messages": [{"role": "user", "content": "Hello"}]}'`,
      ].join('\n')
  }
}
