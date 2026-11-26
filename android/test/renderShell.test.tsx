// @vitest-environment jsdom
// SPDX-License-Identifier: MIT
// The launcher shell on the Android bridge over a client core with fake
// plugins: what the app shows at first start, and adding a server.
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, describe, expect, it } from 'vitest'
import { App } from '@launcher/renderer/App'
import { createAndroidBridge, idleUpdater } from '../src/bridge/androidBridge'
import { ClientCore } from '../src/core/clientCore'
import { fakePlatform, flush } from './fakes/plugins'

;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true

const AI = 'https://ai.example.com'

let root: Root | undefined
let core: ClientCore | undefined

afterEach(async () => {
  await act(async () => root?.unmount())
  await core?.shutdown()
  root = undefined
  core = undefined
  document.body.innerHTML = ''
})

async function settle(): Promise<void> {
  await act(async () => {
    await flush()
  })
}

function byTestId(id: string): HTMLElement {
  const el = document.querySelector<HTMLElement>(`[data-testid="${id}"]`)
  if (!el) throw new Error(`no element with data-testid ${id}`)
  return el
}

function button(name: string): HTMLButtonElement {
  const b = [...document.querySelectorAll('button')].find((x) => x.textContent?.trim() === name || x.getAttribute('aria-label') === name)
  if (!b) throw new Error(`no button ${name}`)
  return b
}

/** Types into the input of a label, as a person would (React listens to "input"). */
function type(label: string, value: string): void {
  const l = [...document.querySelectorAll('label')].find((x) => x.textContent?.trim() === label)
  const input = l?.htmlFor ? (document.getElementById(l.htmlFor) as HTMLInputElement | null) : null
  if (!input) throw new Error(`no field ${label}`)
  const set = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!
  set.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

async function click(el: HTMLElement): Promise<void> {
  await act(async () => {
    el.click()
  })
  await settle()
}

describe('the shell on the Android bridge', () => {
  it('asks for a server, then adds one and shows it', async () => {
    const p = fakePlatform({ answers: { [AI]: 'ok' } })
    core = await ClientCore.open(p)
    const bridge = createAndroidBridge(core, { clipboard: async () => undefined, updater: idleUpdater(), info: { version: '5.0.2' } })
    const el = document.createElement('div')
    document.body.append(el)
    root = createRoot(el)
    await act(async () => root!.render(<App bridge={bridge} />))
    await settle()

    const h1 = document.querySelector('h1')
    expect(h1?.textContent).toBe('Connect to a LocalAI server')

    await click(byTestId('first-connect-address'))
    await act(async () => type('Server address', AI))
    await click(button('Test'))
    await act(async () => type('Name', 'GPU box'))
    await click(button('Connect'))

    const shows = p.webui.calls.filter((c) => c.name === 'show')
    expect(shows).toHaveLength(1)
    expect(shows[0].args[0]).toMatchObject({ origin: AI, url: `${AI}/app` })
    expect(document.querySelector('.lnr-shell')?.getAttribute('data-base')).toBe('web')
    const bar = document.querySelector('footer[aria-label="LocalAI status bar"]')
    expect(bar?.textContent).toContain('GPU box')
  })
})
