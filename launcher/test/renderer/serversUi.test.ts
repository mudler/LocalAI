import { describe, expect, it } from 'vitest'
import { tunnelLine } from '../../src/renderer/serversUi'

describe('tunnelLine', () => {
  it('says that the server needs a relay instead of the library text', () => {
    const line = tunnelLine({ state: 'down', error: 'p2pnet: no direct connection', code: 'needs_relay' })
    expect(line).toMatch(/^P2P not reachable: Cannot connect directly to this server\./)
    expect(line).toContain('local-ai p2p relay')
    expect(line).not.toContain('p2pnet')
  })

  it('keeps any other error as it is', () => {
    expect(tunnelLine({ state: 'down', error: 'timeout' })).toBe('P2P not reachable: timeout')
  })
})
