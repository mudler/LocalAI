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
  isOAuthLoginUrl,
  OAUTH_MAX_ORIGINS,
  OAUTH_PENDING_TIMEOUT_MS,
  OAUTH_SIGNIN_TIMEOUT_MS,
  OAuthSignInFlow,
  sanitizeFileName,
  type NavigationDecision,
  type PermissionDecision,
  type PermissionRequestInfo,
  type PermissionStore,
  type WindowOpenDecision,
} from '../src/core/webuiPolicy'
import { CASES, type OAuthFlowCase, type OAuthFlowStep } from './webuiPolicyCases'

interface OAuthFlowStepResult extends OAuthFlowStep {
  /** The decision of a navigate, redirect or request step; the phase after every step. */
  expect: { decision?: NavigationDecision; phase: OAuthSignInFlow['phase'] }
}

interface WebuiPolicyFixture {
  schema: 1
  navigation: { url: string; origin: string; frame: 'main' | 'sub'; allowedOrigins?: string[]; expect: NavigationDecision }[]
  windowOpen: { url: string; origin: string; expect: WindowOpenDecision }[]
  permission: { request: PermissionRequestInfo; origin: string; store: PermissionStore; expect: PermissionDecision }[]
  fileName: { name: string; fallback?: string; expect: string }[]
  oauthLimits: { pendingTimeoutMs: number; signInTimeoutMs: number; maxOrigins: number }
  oauthLogin: { url: string; origin: string; expect: boolean }[]
  oauthFlow: { name: string; origin: string; steps: OAuthFlowStepResult[] }[]
}

const FIXTURE = path.resolve(__dirname, 'fixtures/webui-policy.json')

/** Runs the steps of one sign-in through a fresh OAuthSignInFlow. */
function runFlow(c: OAuthFlowCase): OAuthFlowStepResult[] {
  const flow = new OAuthSignInFlow(c.origin)
  return c.steps.map((step) => {
    const url = step.url ?? ''
    const frame = step.frame ?? 'main'
    let decision: NavigationDecision | undefined
    switch (step.event) {
      case 'navigate':
        decision = flow.onNavigate(url, frame, step.at)
        break
      case 'redirect':
        decision = flow.onRedirect(url, frame, step.at)
        break
      case 'request':
        decision = flow.onBeforeRequest(url, frame, step.at)
        break
      case 'load':
        flow.onLoad(url, step.at)
        break
      case 'loadFailed':
        flow.onLoadFailed(step.at)
        break
      case 'reset':
        flow.reset()
        break
    }
    return { ...step, expect: decision ? { decision, phase: flow.phase } : { phase: flow.phase } }
  })
}

function build(): WebuiPolicyFixture {
  return {
    schema: 1,
    navigation: CASES.navigation.map((c) => ({
      ...c,
      expect: decideNavigation(c.url, c.origin, { frame: c.frame, allowedOrigins: c.allowedOrigins ? new Set(c.allowedOrigins) : undefined }),
    })),
    windowOpen: CASES.windowOpen.map((c) => ({ ...c, expect: decideWindowOpen(c.url, c.origin) })),
    permission: CASES.permission.map((c) => ({ ...c, expect: decidePermission(c.request, c.origin, c.store) })),
    fileName: CASES.fileName.map((c) => ({ ...c, expect: sanitizeFileName(c.name, c.fallback) })),
    oauthLimits: { pendingTimeoutMs: OAUTH_PENDING_TIMEOUT_MS, signInTimeoutMs: OAUTH_SIGNIN_TIMEOUT_MS, maxOrigins: OAUTH_MAX_ORIGINS },
    oauthLogin: CASES.oauthLogin.map((c) => ({ ...c, expect: isOAuthLoginUrl(c.url, c.origin) })),
    oauthFlow: CASES.oauthFlow.map((c) => ({ ...c, steps: runFlow(c) })),
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
    expect(built.oauthLogin.length).toBeGreaterThanOrEqual(10)
    expect(built.oauthFlow.length).toBeGreaterThanOrEqual(10)
    // Both answers of each question appear, so a port that always says yes or always says no fails.
    expect(new Set(built.oauthLogin.map((c) => c.expect))).toEqual(new Set([true, false]))
    const phases = new Set(built.oauthFlow.flatMap((c) => c.steps.map((s) => s.expect.phase)))
    expect(phases).toEqual(new Set(['idle', 'pending', 'active']))
    const sub = built.navigation.filter((c) => c.allowedOrigins && c.frame === 'sub')
    expect(sub.some((c) => c.expect.action === 'deny')).toBe(true)
  })
})
