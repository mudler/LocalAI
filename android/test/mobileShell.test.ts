// SPDX-License-Identifier: MIT
// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest'
import { IME_CLASS, MOBILE_CLASS, installMobileShell, onKeyboardChange } from '../src/mobileShell'

/** A visual viewport the test resizes by hand (jsdom has none). */
class FakeViewport extends EventTarget {
  constructor(
    public width: number,
    public height: number,
  ) {
    super()
  }
  resize(width: number, height: number) {
    this.width = width
    this.height = height
    this.dispatchEvent(new Event('resize'))
  }
}

const root = () => document.documentElement.classList
let undo: (() => void) | undefined

afterEach(() => {
  undo?.()
  undo = undefined
  delete (window as { visualViewport?: unknown }).visualViewport
})

function withViewport(width: number, height: number): FakeViewport {
  const vv = new FakeViewport(width, height)
  Object.defineProperty(window, 'visualViewport', { value: vv, configurable: true })
  return vv
}

describe('installMobileShell', () => {
  it('marks the document as the phone layout and removes the mark on undo', () => {
    undo = installMobileShell(window)
    expect(root().contains(MOBILE_CLASS)).toBe(true)
    undo()
    undo = undefined
    expect(root().contains(MOBILE_CLASS)).toBe(false)
  })

  it('sets lnr-ime while the soft keyboard takes the bottom of the screen', () => {
    const vv = withViewport(360, 800)
    undo = installMobileShell(window)
    expect(root().contains(IME_CLASS)).toBe(false)
    vv.resize(360, 480)
    expect(root().contains(IME_CLASS)).toBe(true)
    vv.resize(360, 800)
    expect(root().contains(IME_CLASS)).toBe(false)
  })

  it('ignores small changes, such as a toolbar that hides', () => {
    const vv = withViewport(360, 800)
    undo = installMobileShell(window)
    vv.resize(360, 744)
    expect(root().contains(IME_CLASS)).toBe(false)
  })

  it('takes a rotation as a new full height, not as a keyboard', () => {
    const vv = withViewport(360, 800)
    undo = installMobileShell(window)
    vv.resize(800, 360)
    expect(root().contains(IME_CLASS)).toBe(false)
    vv.resize(800, 160)
    expect(root().contains(IME_CLASS)).toBe(true)
    vv.resize(360, 800)
    expect(root().contains(IME_CLASS)).toBe(false)
  })

  it('stops listening on undo', () => {
    const vv = withViewport(360, 800)
    undo = installMobileShell(window)
    undo()
    undo = undefined
    vv.resize(360, 400)
    expect(root().contains(IME_CLASS)).toBe(false)
  })

  it('works without a visual viewport (no keyboard detection)', () => {
    undo = installMobileShell(window)
    expect(root().contains(MOBILE_CLASS)).toBe(true)
    expect(root().contains(IME_CLASS)).toBe(false)
  })
})

describe('onKeyboardChange', () => {
  const tick = () => new Promise((r) => setTimeout(r, 0))

  it('reports the keyboard opening and closing once each, and stops on undo', async () => {
    const vv = withViewport(360, 800)
    undo = installMobileShell(window)
    const seen: boolean[] = []
    const stop = onKeyboardChange(window, (open) => seen.push(open))
    vv.resize(360, 480)
    await tick()
    vv.resize(360, 470)
    await tick()
    vv.resize(360, 800)
    await tick()
    expect(seen).toEqual([true, false])
    stop()
    vv.resize(360, 480)
    await tick()
    expect(seen).toEqual([true, false])
  })
})
