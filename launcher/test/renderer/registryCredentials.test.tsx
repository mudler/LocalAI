// @vitest-environment jsdom
import { act, fireEvent, render, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { App } from '../../src/renderer/App'
import { fakeBridge, type FakeBridge } from '../../src/renderer/dev/fakeBridge'
import { fixture } from '../../src/renderer/dev/fixtures'
import type {
  RegistryCredentialRemoveResult,
  RegistryCredentialSaveResult,
  RegistryCredentialTestResult,
  RegistryCredentialsView,
} from '../../src/shared/types'
import { NOW, called, screen, settle } from './render'

const SECRET = 'hunter2-not-for-display'
const rule = { id: '0123456789abcdef', match: 'registry.example/team', kind: 'basic' as const, username: 'ci-user', secretSet: true }
const bearer = { id: 'fedcba9876543210', match: 'tokens.example', kind: 'bearer' as const, secretSet: true }

async function open(
  over: {
    view?: Partial<RegistryCredentialsView>
    save?: RegistryCredentialSaveResult
    remove?: RegistryCredentialRemoveResult
    test?: RegistryCredentialTestResult | 'throw' | 'none'
    /** The list call fails after this many good answers. */
    listFailsAfter?: number
    saveDelay?: number
  } = {},
): Promise<FakeBridge & { unmount: () => void }> {
  const base = fakeBridge(fixture('settings', NOW))
  const view: RegistryCredentialsView = { rules: [rule, bearer], userOverride: false, restartRequired: false, ...over.view }
  let lists = 0
  const bridge: FakeBridge = {
    ...base,
    getRegistryCredentials: async () => {
      lists++
      if (over.listFailsAfter !== undefined && lists > over.listFailsAfter) throw new Error('list failed')
      return view
    },
    saveRegistryCredential: async (input) => {
      base.calls.push({ name: 'saveRegistryCredential', args: [input] })
      if (over.saveDelay) await new Promise((r) => setTimeout(r, over.saveDelay))
      return over.save ?? { ok: true, id: 'aaaaaaaaaaaaaaaa', restartRequired: false }
    },
    removeRegistryCredential: async (id) => {
      base.calls.push({ name: 'removeRegistryCredential', args: [id] })
      return over.remove ?? { restartRequired: false }
    },
    testRegistryCredential: async (id) => {
      base.calls.push({ name: 'testRegistryCredential', args: [id] })
      if (over.test === 'throw') throw new Error('boom')
      if (over.test === 'none') return undefined as unknown as RegistryCredentialTestResult
      return over.test ?? { result: 'ok', status: 200 }
    },
  }
  const { unmount } = render(<App bridge={bridge} now={NOW} />)
  await settle()
  fireEvent.click(within(screen.getByRole('contentinfo', { name: 'LocalAI status bar' })).getByRole('button', { name: 'Menu' }))
  fireEvent.click(screen.getByRole('menuitem', { name: 'Settings' }))
  await settle()
  fireEvent.click(screen.getByRole('tab', { name: 'Advanced' }))
  await settle()
  return Object.assign(bridge, { unmount })
}

const type = (label: string, value: string) => fireEvent.change(screen.getByLabelText(label), { target: { value } })
const click = async (name: string | RegExp) => {
  fireEvent.click(screen.getByRole('button', { name }))
  await settle()
}

describe('Settings, registry credentials', () => {
  it('lists match, kind and user name with "Secret set" and no secret value', async () => {
    await open()
    const rows = screen.getAllByTestId('registry-rule')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.textContent).toContain('registry.example/team')
    expect(rows[0]!.textContent).toContain('Basic')
    expect(rows[0]!.textContent).toContain('ci-user')
    expect(rows[0]!.textContent).toContain('Secret set')
    expect(rows[1]!.textContent).toContain('Bearer token')
    expect(screen.getByTestId('registry-credentials').textContent).not.toContain(SECRET)
    expect(screen.getByText(/replace the credentials\.yaml/)).toBeTruthy()
  })

  it('shows the override notice and not the replacement note', async () => {
    await open({ view: { userOverride: true } })
    expect(screen.getByText(/LOCALAI_CREDENTIALS_FILE is set by you/)).toBeTruthy()
    expect(screen.queryByText(/replace the credentials\.yaml/)).toBeNull()
  })

  it.each([
    ['userinfo', 'user:pw@registry.example'],
    ['query', 'registry.example/x?a=b'],
    ['spaces', 'registry .example'],
    ['empty', '   '],
    ['plain http', 'http://registry.example'],
  ])('refuses a match with %s and does not call save', async (_n, match) => {
    const b = await open()
    await click('Add rule')
    type('Registry', match)
    type('User name', 'u')
    type('Password', SECRET)
    await click('Save')
    expect(screen.getByRole('alert').textContent).toMatch(/registry|user name/i)
    expect(called(b, 'saveRegistryCredential')).toHaveLength(0)
  })

  it('says plain http is not supported', async () => {
    const b = await open()
    await click('Add rule')
    type('Registry', 'http://registry.example')
    type('User name', 'u')
    type('Password', SECRET)
    await click('Save')
    expect(screen.getByRole('alert').textContent).toBe('Use a registry address without http://; plain http is not supported.')
    expect(called(b, 'saveRegistryCredential')).toHaveLength(0)
  })

  it.each([['a:b'], ['a\u0085b'], ['a\ud800b']])('refuses the user name %j and does not call save', async (username) => {
    const b = await open()
    await click('Add rule')
    type('Registry', 'new.example')
    type('User name', username)
    type('Password', SECRET)
    await click('Save')
    expect(screen.getByRole('alert').textContent).toBe('The user name is not valid.')
    expect(called(b, 'saveRegistryCredential')).toHaveLength(0)
  })

  it.each([
    ['the kind', () => type('Type', 'bearer'), 'Token'],
    ['the host', () => type('Registry', 'other.example/team'), 'Password'],
    ['the port', () => type('Registry', 'registry.example:5000/team'), 'Password'],
  ])('needs the secret again when an edit changes %s', async (_n, change, secretLabel) => {
    const b = await open()
    await click('Edit the rule for registry.example/team')
    change()
    expect((screen.getByLabelText(secretLabel) as HTMLInputElement).placeholder).toBe('')
    await click('Save')
    expect(screen.getByRole('alert').textContent).toBe('Enter the secret again: the kind or the registry changed.')
    expect(called(b, 'saveRegistryCredential')).toHaveLength(0)
    type(secretLabel, SECRET)
    await click('Save')
    expect(called(b, 'saveRegistryCredential')).toHaveLength(1)
    expect(called(b, 'saveRegistryCredential')[0]?.args[0]).toMatchObject({ id: rule.id, secret: SECRET })
  })

  it('keeps the stored secret when an edit only changes the path', async () => {
    const b = await open()
    await click('Edit the rule for registry.example/team')
    type('Registry', 'https://registry.example/other')
    expect((screen.getByLabelText('Password') as HTMLInputElement).placeholder).toBe('Leave empty to keep the stored secret')
    await click('Save')
    expect(called(b, 'saveRegistryCredential')[0]?.args).toEqual([{ id: rule.id, match: 'https://registry.example/other', kind: 'basic', username: 'ci-user' }])
  })

  it('needs a secret for a new rule', async () => {
    const b = await open()
    await click('Add rule')
    type('Registry', 'new.example')
    type('User name', 'u')
    await click('Save')
    expect(screen.getByRole('alert').textContent).toBe('Enter a secret.')
    expect(called(b, 'saveRegistryCredential')).toHaveLength(0)
  })

  it('sends the secret once, uses a password input with no reveal, and clears the input', async () => {
    const b = await open({ save: { ok: false, reason: 'Your system has no keyring, so this secret cannot be kept.' } })
    await click('Add rule')
    const input = screen.getByLabelText('Password') as HTMLInputElement
    expect(input.type).toBe('password')
    expect(screen.queryByRole('button', { name: /show|reveal/i })).toBeNull()
    type('Registry', 'new.example')
    type('User name', 'u')
    type('Password', SECRET)
    await click('Save')
    expect(called(b, 'saveRegistryCredential')[0]?.args).toEqual([{ match: 'new.example', kind: 'basic', username: 'u', secret: SECRET }])
    // A refused save keeps the form open, shows the reply as is and empties the secret.
    expect(screen.getByRole('alert').textContent).toBe('Your system has no keyring, so this secret cannot be kept.')
    expect((screen.getByLabelText('Password') as HTMLInputElement).value).toBe('')
    expect(document.body.innerHTML).not.toContain(SECRET)
  })

  it('closes the form after a good save and the secret is gone from the page', async () => {
    await open()
    await click('Add rule')
    type('Registry', 'new.example')
    type('Type', 'bearer')
    type('Token', SECRET)
    await click('Save')
    expect(screen.queryByLabelText('Token')).toBeNull()
    expect(document.body.innerHTML).not.toContain(SECRET)
  })

  it('keeps the stored secret when an edit leaves the secret empty', async () => {
    const b = await open()
    await click('Edit the rule for registry.example/team')
    type('User name', 'other')
    await click('Save')
    expect(called(b, 'saveRegistryCredential')[0]?.args).toEqual([{ id: rule.id, match: rule.match, kind: 'basic', username: 'other' }])
  })

  it.each([
    ['ok', 'Signed in'],
    ['unauthorized', 'Rejected'],
    ['token', 'Cannot check here: this registry uses token sign-in.'],
    ['unreachable', 'Not reachable'],
    ['tls', 'Certificate problem'],
    ['other', 'Unexpected answer'],
  ] as const)('Test shows only the label for %s', async (result, label) => {
    const b = await open({ test: { result, status: 503 } })
    await click('Test the rule for registry.example/team')
    expect(called(b, 'testRegistryCredential')[0]?.args).toEqual([rule.id])
    expect(screen.getAllByTestId('registry-test-result')[0]!.textContent).toBe(label)
  })

  it.each([['throw'], ['none']] as const)('shows "Test failed" when the Test action gives %s', async (test) => {
    await open({ test })
    await click('Test the rule for registry.example/team')
    expect(screen.getAllByTestId('registry-test-result')[0]!.textContent).toBe('Test failed')
  })

  it('removes a rule only after the confirm', async () => {
    const b = await open()
    await click('Remove the rule for registry.example/team')
    expect(called(b, 'removeRegistryCredential')).toHaveLength(0)
    await click('Cancel')
    expect(called(b, 'removeRegistryCredential')).toHaveLength(0)
    await click('Remove the rule for registry.example/team')
    await click('Remove rule')
    expect(called(b, 'removeRegistryCredential')[0]?.args).toEqual([rule.id])
  })

  it('says a restart is needed after a save', async () => {
    await open({ save: { ok: true, id: rule.id, restartRequired: true } })
    expect(screen.queryByTestId('registry-restart')).toBeNull()
    await click('Add rule')
    type('Registry', 'new.example')
    type('User name', 'u')
    type('Password', SECRET)
    await click('Save')
    expect(screen.getByTestId('registry-restart').textContent).toContain('Restart LocalAI')
  })

  it('says a restart is needed after a remove', async () => {
    await open({ remove: { restartRequired: true } })
    await click('Remove the rule for tokens.example')
    await click('Remove rule')
    expect(screen.getByTestId('registry-restart').textContent).toContain('Restart LocalAI')
  })

  it('shows no restart note when a remove needs none', async () => {
    const b = await open({ remove: { restartRequired: false } })
    await click('Remove the rule for tokens.example')
    await click('Remove rule')
    expect(called(b, 'removeRegistryCredential')).toHaveLength(1)
    expect(screen.queryByTestId('registry-restart')).toBeNull()
  })

  it('sends a new secret on an edit, and the form is emptied', async () => {
    const b = await open()
    await click('Edit the rule for registry.example/team')
    type('Password', SECRET)
    await click('Save')
    expect(called(b, 'saveRegistryCredential')[0]?.args).toEqual([{ id: rule.id, match: rule.match, kind: 'basic', username: 'ci-user', secret: SECRET }])
    expect(document.body.innerHTML).not.toContain(SECRET)
  })

  it('sends one save when Enter is pressed twice in a row', async () => {
    const b = await open({ saveDelay: 30 })
    await click('Add rule')
    type('Registry', 'new.example')
    type('User name', 'u')
    type('Password', SECRET)
    const input = screen.getByLabelText('Password')
    fireEvent.keyDown(input, { key: 'Enter' })
    fireEvent.keyDown(input, { key: 'Enter' })
    await act(async () => {
      await new Promise((r) => setTimeout(r, 60))
    })
    expect(called(b, 'saveRegistryCredential')).toHaveLength(1)
  })

  it('closes the form and does not say the save failed when only the refresh fails', async () => {
    await open({ listFailsAfter: 1, save: { ok: true, id: 'aaaaaaaaaaaaaaaa', restartRequired: true } })
    await click('Add rule')
    type('Registry', 'new.example')
    type('User name', 'u')
    type('Password', SECRET)
    await click('Save')
    expect(screen.queryByLabelText('Password')).toBeNull()
    expect(screen.getByTestId('registry-restart')).toBeTruthy()
    expect(screen.getByRole('alert').textContent).toBe('Saved, but the list could not be refreshed.')
    expect(screen.queryByText('The rule could not be saved.')).toBeNull()
  })
})
