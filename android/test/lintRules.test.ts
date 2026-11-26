// SPDX-License-Identifier: MIT
import path from 'node:path'
import { ESLint } from 'eslint'
import { describe, expect, it } from 'vitest'

// The import rules of eslint.config.mjs keep the shared launcher surface to the
// three @launcher/* roots. Lint small snippets as if they were app sources.
const appRoot = path.resolve(__dirname, '..')
const eslint = new ESLint({ cwd: appRoot })

async function ruleIds(code: string): Promise<string[]> {
  const [result] = await eslint.lintText(code, { filePath: path.join(appRoot, 'src/probe.ts') })
  return result.messages.map((m) => m.ruleId ?? m.message)
}

describe('import rules', () => {
  it('allows the launcher roots', async () => {
    expect(await ruleIds("export { readInvite } from '@launcher/core/p2pInvite'\n")).toEqual([])
  })

  it('refuses a relative path that leaves android/', async () => {
    expect(await ruleIds("export * from '../../launcher/src/core/address'\n")).toContain('local/stays-in-app')
  })

  it('refuses the desktop main and preload code', async () => {
    expect(await ruleIds("export * from '../../launcher/src/main/index'\n")).toContain('no-restricted-imports')
    expect(await ruleIds("export * from '../../launcher/src/preload/index'\n")).toContain('no-restricted-imports')
  })

  it('refuses an unknown @launcher root', async () => {
    expect(await ruleIds("export * from '@launcher/main/index'\n")).toContain('no-restricted-imports')
  })

  it('refuses .. through an alias', async () => {
    expect(await ruleIds("export * from '@launcher/core/../main/index'\n")).toContain('no-restricted-imports')
    expect(await ruleIds("export * from '@launcher/renderer/../../src/main/index'\n")).toContain('no-restricted-imports')
  })
})
