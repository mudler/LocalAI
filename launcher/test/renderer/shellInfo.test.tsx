// @vitest-environment jsdom
// A desktop bridge whose info() fails: the shell uses the desktop set and
// still shows its screen, as it did before info() decided the mode.
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { App } from '../../src/renderer/App'
import { fakeBridge } from '../../src/renderer/dev/fakeBridge'
import { fixture } from '../../src/renderer/dev/fixtures'
import { NOW, screen, settle } from './render'

describe('info() fails', () => {
  it('renders the desktop shell, not a blank busy area', async () => {
    const b = fakeBridge(fixture('ready', NOW))
    b.info = () => Promise.reject(new Error('no info channel'))
    render(<App bridge={b} now={NOW} />)
    await settle()
    const shell = document.querySelector('.lnr-shell')
    expect(shell?.getAttribute('aria-busy')).toBeNull()
    expect(shell?.getAttribute('data-base')).toBe('web')
    expect(screen.getByTestId('bar-status').textContent).toContain('Ready')
  })
})
