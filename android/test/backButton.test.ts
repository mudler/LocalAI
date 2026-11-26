// @vitest-environment jsdom
// SPDX-License-Identifier: MIT
import { afterEach, describe, expect, it } from 'vitest'
import { decideBack, runBack, type BackState } from '../src/bridge/backButton'

const none: BackState = { overlayOpen: false, shellScreenOpen: false, webuiVisible: false, webuiCanGoBack: false }

describe('decideBack', () => {
  it('closes an overlay first, even over a WebUI with history', () => {
    expect(decideBack({ ...none, overlayOpen: true, webuiVisible: true, webuiCanGoBack: true })).toBe('escape')
    expect(decideBack({ ...none, overlayOpen: true, shellScreenOpen: true })).toBe('escape')
  })

  it('goes back in the WebUI history next', () => {
    expect(decideBack({ ...none, webuiVisible: true, webuiCanGoBack: true })).toBe('webui-back')
  })

  it('closes a shell screen next', () => {
    expect(decideBack({ ...none, shellScreenOpen: true })).toBe('close-screen')
    expect(decideBack({ ...none, shellScreenOpen: true, webuiCanGoBack: true })).toBe('close-screen')
  })

  it('leaves the app last', () => {
    expect(decideBack(none)).toBe('minimize')
    expect(decideBack({ ...none, webuiVisible: true })).toBe('minimize')
  })
})

describe('runBack', () => {
  afterEach(() => {
    document.body.innerHTML = ''
  })

  const env = (o: { overlay?: boolean; screen?: boolean; webui?: boolean; history?: boolean } = {}) => {
    const log: string[] = []
    return {
      log,
      core: {
        webuiState: () => ({ overlay: !!o.overlay, screen: o.screen ? ('settings' as const) : null, layout: o.webui && !o.screen ? ('webui' as const) : ('shell-only' as const) }),
        webuiGoBack: async () => {
          log.push('goBack')
          return !!o.history
        },
      },
      minimize: async () => void log.push('minimize'),
    }
  }

  it('sends Escape to the focused element when a popover is open', async () => {
    const e = env({ overlay: true, webui: true, history: true })
    const keys: string[] = []
    document.body.addEventListener('keydown', (k) => keys.push(k.key))
    expect(await runBack(e.core, { doc: document, minimize: e.minimize })).toBe('escape')
    expect(keys).toEqual(['Escape'])
    expect(e.log).toEqual([])
  })

  it('treats an open dialog as an overlay', async () => {
    document.body.innerHTML = '<div role="dialog" aria-modal="true"></div><button data-testid="back"></button>'
    const e = env({ screen: true })
    expect(await runBack(e.core, { doc: document, minimize: e.minimize })).toBe('escape')
  })

  it('goes back in the WebUI when it has history', async () => {
    const e = env({ webui: true, history: true })
    expect(await runBack(e.core, { doc: document, minimize: e.minimize })).toBe('webui-back')
    expect(e.log).toEqual(['goBack'])
  })

  it('minimizes when the WebUI has no history', async () => {
    const e = env({ webui: true, history: false })
    expect(await runBack(e.core, { doc: document, minimize: e.minimize })).toBe('minimize')
    expect(e.log).toEqual(['goBack', 'minimize'])
  })

  it('clicks the screen back button', async () => {
    document.body.innerHTML = '<button data-testid="back"></button>'
    let clicks = 0
    document.querySelector('button')!.addEventListener('click', () => clicks++)
    const e = env({ screen: true })
    expect(await runBack(e.core, { doc: document, minimize: e.minimize })).toBe('close-screen')
    expect(clicks).toBe(1)
    expect(e.log).toEqual([])
  })
})
