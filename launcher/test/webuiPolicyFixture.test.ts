// Writes the WebUI policy decisions to test/fixtures/webui-policy.json, so a
// port of the policy to another platform can run the same cases and compare.
// The decisions come from src/core/webuiPolicy.ts; this test only fails when
// the JSON and the code no longer agree. Regenerate the JSON after a deliberate
// policy change with:
//   UPDATE_WEBUI_POLICY_FIXTURE=1 npx vitest run test/webuiPolicyFixture.test.ts
import fs from 'node:fs'
import path from 'node:path'
import { describe, expect, it } from 'vitest'
import {
  decideNavigation,
  decidePermission,
  decideWindowOpen,
  sanitizeFileName,
  type NavigationDecision,
  type PermissionDecision,
  type PermissionRequestInfo,
  type PermissionStore,
  type WindowOpenDecision,
} from '../src/core/webuiPolicy'
import { CASES } from './webuiPolicyCases'

interface WebuiPolicyFixture {
  schema: 1
  navigation: { url: string; origin: string; frame: 'main' | 'sub'; expect: NavigationDecision }[]
  windowOpen: { url: string; origin: string; expect: WindowOpenDecision }[]
  permission: { request: PermissionRequestInfo; origin: string; store: PermissionStore; expect: PermissionDecision }[]
  fileName: { name: string; fallback?: string; expect: string }[]
}

const FIXTURE = path.resolve(__dirname, 'fixtures/webui-policy.json')

function build(): WebuiPolicyFixture {
  return {
    schema: 1,
    navigation: CASES.navigation.map((c) => ({ ...c, expect: decideNavigation(c.url, c.origin, { frame: c.frame }) })),
    windowOpen: CASES.windowOpen.map((c) => ({ ...c, expect: decideWindowOpen(c.url, c.origin) })),
    permission: CASES.permission.map((c) => ({ ...c, expect: decidePermission(c.request, c.origin, c.store) })),
    fileName: CASES.fileName.map((c) => ({ ...c, expect: sanitizeFileName(c.name, c.fallback) })),
  }
}

/** 2-space JSON with every character outside printable ASCII escaped, so bidi and control characters stay visible in a diff. */
function serialize(f: WebuiPolicyFixture): string {
  const json = JSON.stringify(f, null, 2).replace(/[\u007f-\uffff]/g, (ch) => `\\u${ch.charCodeAt(0).toString(16).padStart(4, '0')}`)
  return `${json}\n`
}

describe('WebUI policy fixture', () => {
  it('matches the decisions of src/core/webuiPolicy.ts', () => {
    const built = build()
    if (process.env.UPDATE_WEBUI_POLICY_FIXTURE === '1') fs.writeFileSync(FIXTURE, serialize(built))
    expect(JSON.parse(fs.readFileSync(FIXTURE, 'utf8'))).toEqual(built)
  })

  it('covers enough cases of each kind', () => {
    const built = build()
    expect(built.navigation.length).toBeGreaterThanOrEqual(30)
    expect(built.windowOpen.length).toBeGreaterThanOrEqual(8)
    expect(built.permission.length).toBeGreaterThanOrEqual(10)
    expect(built.fileName.length).toBeGreaterThanOrEqual(12)
  })
})
