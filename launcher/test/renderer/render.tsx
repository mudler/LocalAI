// Helpers for the renderer component tests (jsdom). Each test file starts with
// a `@vitest-environment jsdom` comment; the rest of the suite stays on Node.
import { act, cleanup, render, screen } from '@testing-library/react'
import { afterEach } from 'vitest'
import { App } from '../../src/renderer/App'

import { fakeBridge, type FakeBridge, type FakeState, type OptionalName } from '../../src/renderer/dev/fakeBridge'
import { fixture, type FixtureName } from '../../src/renderer/dev/fixtures'

afterEach(() => cleanup())

export const NOW = new Date(2026, 10, 14, 14, 30).getTime()

/** Renders the shell on a fixture and waits until the bridge answers are on screen. */
export async function renderShell(
  what: FixtureName | FakeState,
  opts: { extras?: boolean | OptionalName[]; edit?: (s: FakeState) => void } = {},
): Promise<FakeBridge> {
  const state = typeof what === 'string' ? fixture(what, NOW) : what
  opts.edit?.(state)
  const bridge = fakeBridge(state, { extras: opts.extras })
  render(<App bridge={bridge} now={NOW} />)
  await settle()
  return bridge
}

/** Lets the fake bridge's promises and timers run. */
export async function settle() {
  await act(async () => {
    await new Promise((r) => setTimeout(r, 5))
  })
}

export const called = (b: FakeBridge, name: string) => b.calls.filter((c) => c.name === name)

export { screen }
