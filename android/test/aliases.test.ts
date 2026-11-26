// SPDX-License-Identifier: MIT
import { describe, expect, it } from 'vitest'

// The app reaches the launcher code only through these aliases. If one breaks,
// the bundle would fail late in `vite build`; this fails first.
describe('launcher aliases', () => {
  it('resolves @launcher/core', async () => {
    const m = await import('@launcher/core/p2pInvite')
    expect(typeof m.readInvite).toBe('function')
  })

  it('resolves @launcher/renderer', async () => {
    const m = await import('@launcher/renderer/App')
    expect(typeof m.App).toBe('function')
  })
})
