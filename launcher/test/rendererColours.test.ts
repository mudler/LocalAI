import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'

// The WebUI's colour gate (core/http/react-ui, npm run lint:colours) does not
// scan the launcher. This keeps the same rule for the shell renderer: colour
// comes from the theme roles (--dk-*, --color-*), never a literal.
const ROOT = path.resolve(__dirname, '../src/renderer')
const LITERAL = /#[0-9a-fA-F]{3,8}\b|\b(?:rgba?|hsla?)\(/

function files(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name)
    if (e.isDirectory()) return files(p)
    return /\.(css|tsx?|html)$/.test(e.name) ? [p] : []
  })
}

describe('renderer colours', () => {
  it('has no literal colour in the renderer sources', () => {
    const hits: string[] = []
    for (const f of files(ROOT)) {
      fs.readFileSync(f, 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (LITERAL.test(line)) hits.push(`${path.relative(ROOT, f)}:${i + 1}: ${line.trim()}`)
        })
    }
    expect(hits).toEqual([])
  })
})
