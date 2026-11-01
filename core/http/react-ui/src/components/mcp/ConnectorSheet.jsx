/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import SideSheet from '../SideSheet'
import ConfirmDialog from '../ConfirmDialog'
import UnsavedChangesGuard from '../UnsavedChangesGuard'
import { Button, Callout, LoadingSpinner } from '../ui'
import { canSaveMCP, formFingerprint, isAuthError, nameProblem, sentence, urlProblem } from '../../utils/chatMCP'
import { PairRows, SecretInput, ToolList } from './ConnectorBits'

// Add or edit a connector in the shared side sheet, in two steps:
//   1. the form: the kind first, then the fields in groups, checked as the
//      person types, with the secrets masked;
//   2. the test: the server is saved, then tested with what was saved, and the
//      sheet shows the tools it found or why it failed, with Turn on.
// The test runs on the saved definition (the API tests a stored connector), so
// Save comes first. Escape, the close button and Cancel ask before they drop
// typed changes; Enter in a field saves.
export default function ConnectorSheet({ initial, isAdmin, items, focus, onSave, onTest, onTurnOn, onClose }) {
  const { t } = useTranslation('chats')
  const editing = Boolean(initial.id)
  const [form, setForm] = useState(initial)
  const [step, setStep] = useState('form')
  const [saved, setSaved] = useState(null)
  const [check, setCheck] = useState(null)
  const [testing, setTesting] = useState(false)
  const [saving, setSaving] = useState(false)
  const [turning, setTurning] = useState(false)
  const [error, setError] = useState('')
  const [touched, setTouched] = useState({})
  const [tried, setTried] = useState(false)
  const [asking, setAsking] = useState(false)
  const tokenRef = useRef(null)
  const start = useRef(formFingerprint(initial))

  const stdio = form.transport === 'stdio'
  // The names of the other connectors: a name must be unique per user.
  const taken = (items || []).filter(x => x.id !== form.id).map(x => x.name)
  const dirty = step === 'form' && formFingerprint(form) !== start.current
  const set = (patch) => setForm(f => ({ ...f, ...patch }))
  const touch = (k) => setTouched(x => ({ ...x, [k]: true }))

  useEffect(() => {
    if (focus === 'token') requestAnimationFrame(() => tokenRef.current?.focus())
  }, [focus])

  const nameErr = nameProblem(form.name, taken)
  const urlErr = stdio ? '' : urlProblem(form.url)
  const cmdErr = stdio && !(form.command || '').trim() ? 'commandRequired' : ''
  const show = (k, err) => (err && (touched[k] || tried) ? t(`mcpPage.errors.${err}`) : '')

  const close = () => { if (dirty) setAsking(true); else onClose(saved) }

  const runTest = async (c) => {
    setTesting(true)
    setCheck(null)
    try { setCheck(await onTest(c)) } finally { setTesting(false) }
  }

  const submit = async (e) => {
    e?.preventDefault()
    setTried(true)
    if (!canSaveMCP(form, taken)) return
    setSaving(true)
    setError('')
    try {
      const c = await onSave(form)
      setSaved(c)
      start.current = formFingerprint(form)
      setStep('test')
      runTest(c)
    } catch (err) {
      setError(err?.message || t('errors.send'))
    } finally {
      setSaving(false)
    }
  }

  // Back to the form of what was just saved: secrets are not read back, so
  // their fields start empty and the saved flags say what is stored.
  const backToForm = () => {
    const next = { ...form, id: saved.id, token: '', headers: [], env: [], clearToken: false, clearHeaders: false, clearEnv: false,
      has_token: saved.has_token, has_headers: saved.has_headers, has_env: saved.has_env, enabled: saved.enabled, updated: saved.updated }
    start.current = formFingerprint(next)
    setForm(next)
    setCheck(null)
    setError('')
    setStep('form')
  }

  const turnOn = async () => {
    setTurning(true)
    try {
      const c = await onTurnOn(saved)
      onClose(c || saved)
    } catch (err) {
      setError(err?.message || t('errors.send'))
    } finally {
      setTurning(false)
    }
  }

  const title = editing ? t('mcpPage.sheet.editTitle', { name: initial.name }) : t('mcpPage.sheet.addTitle')
  const description = step === 'test' ? t('mcpPage.sheet.testDesc') : editing ? t('mcpPage.sheet.editDesc') : t('mcpPage.sheet.addDesc')

  let footer
  if (step === 'form') {
    footer = (
      <>
        <Button variant="ghost" onClick={close}>{t('mcpPage.sheet.cancel')}</Button>
        <Button type="submit" form="mcp-sheet-form" variant="primary" icon="plug" busy={saving} data-testid="mcp-save">
          {saving ? t('mcpPage.sheet.saving') : t('mcpPage.sheet.saveAndTest')}
        </Button>
      </>
    )
  } else {
    const ok = check?.ok
    footer = (
      <>
        <Button variant="ghost" icon="pencil" disabled={testing} onClick={backToForm} data-testid="mcp-sheet-back">
          {t('mcpPage.sheet.back')}
        </Button>
        {!testing && check && !ok && <Button icon="refresh" onClick={() => runTest(saved)} data-testid="mcp-sheet-retry">{t('mcpPage.sheet.retry')}</Button>}
        {saved?.enabled ? (
          <Button variant={ok ? 'primary' : 'secondary'} disabled={testing} onClick={() => onClose(saved)} data-testid="mcp-sheet-done">{t('mcpPage.sheet.done')}</Button>
        ) : (
          <>
            <Button variant={ok ? 'secondary' : 'ghost'} disabled={testing} onClick={() => onClose(saved)} data-testid="mcp-sheet-keep-off">{t('mcpPage.sheet.keepOff')}</Button>
            {ok && <Button variant="primary" icon="plug" busy={turning} onClick={turnOn} data-testid="mcp-sheet-turn-on">{t('mcpPage.sheet.turnOn')}</Button>}
          </>
        )}
      </>
    )
  }

  return (
    <>
      <SideSheet
        title={title}
        description={description}
        onClose={close}
        closeLabel={t('mcpPage.sheet.close')}
        testId="mcp-sheet"
        labelId="mcp-sheet-title"
        footer={footer}
      >
        {error && <Callout tone="error" role="alert" className="mcp-sheet__error" testId="mcp-sheet-error">{error}</Callout>}

        {step === 'form' && (
          <form id="mcp-sheet-form" className="mcp-form" onSubmit={submit} noValidate>
            {isAdmin && !form.id && (
              <div className="mcp-group">
                <h3 className="mcp-group__title" id="mcp-g-kind">{t('mcpPage.sheet.kind')}</h3>
                <div className="mcp-kinds" role="radiogroup" aria-labelledby="mcp-g-kind">
                  {['http', 'stdio'].map(k => (
                    <label key={k} className="mcp-kind" data-checked={form.transport === k ? 'true' : 'false'}>
                      <input type="radio" name="mcp-kind" className="dk-radio" value={k} checked={form.transport === k} onChange={() => set({ transport: k })} />
                      <Icon name={k === 'http' ? 'globe' : 'terminal'} className="mcp-kind__icon" aria-hidden="true" />
                      <span className="mcp-kind__text">
                        <span className="mcp-kind__name">{k === 'http' ? t('mcpPage.sheet.remote') : t('mcpPage.sheet.local')}</span>
                        <span className="mcp-kind__help">{k === 'http' ? t('mcpPage.sheet.remoteHelp') : t('mcpPage.sheet.localHelp')}</span>
                      </span>
                    </label>
                  ))}
                </div>
              </div>
            )}

            <div className="mcp-group" role="group" aria-labelledby="mcp-g-basics">
              <h3 className="mcp-group__title" id="mcp-g-basics">{t('mcpPage.sheet.basics')}</h3>
              <div className="dk-field">
                <label className="dk-label" htmlFor="mcp-name">{t('mcpPage.sheet.name')}</label>
                <input id="mcp-name" className="dk-input" value={form.name} maxLength={80} autoComplete="off" required
                  aria-invalid={Boolean(show('name', nameErr)) || undefined} aria-describedby="mcp-name-hint"
                  onBlur={() => touch('name')} onChange={(e) => set({ name: e.target.value })} />
                {show('name', nameErr)
                  ? <p className="dk-field-error" id="mcp-name-hint"><Icon name="alert-circle" />{show('name', nameErr)}</p>
                  : <p className="dk-hint" id="mcp-name-hint">{t('mcpPage.sheet.nameHelp')}</p>}
              </div>
              {!stdio && (
                <div className="dk-field">
                  <label className="dk-label" htmlFor="mcp-url">{t('mcpPage.sheet.url')}</label>
                  <input id="mcp-url" className="dk-input dk-input--mono" type="url" inputMode="url" value={form.url} placeholder="https://" autoComplete="off" spellCheck={false} required
                    aria-invalid={Boolean(show('url', urlErr)) || undefined} aria-describedby="mcp-url-hint"
                    onBlur={() => touch('url')} onChange={(e) => set({ url: e.target.value })} />
                  {show('url', urlErr)
                    ? <p className="dk-field-error" id="mcp-url-hint"><Icon name="alert-circle" />{show('url', urlErr)}</p>
                    : <p className="dk-hint" id="mcp-url-hint">{t('mcpPage.sheet.urlHelp')}</p>}
                </div>
              )}
              {stdio && (
                <>
                  <div className="dk-field">
                    <label className="dk-label" htmlFor="mcp-command">{t('mcpPage.sheet.command')}</label>
                    <input id="mcp-command" className="dk-input dk-input--mono" value={form.command} autoComplete="off" spellCheck={false} required
                      aria-invalid={Boolean(show('command', cmdErr)) || undefined} aria-describedby="mcp-command-hint"
                      onBlur={() => touch('command')} onChange={(e) => set({ command: e.target.value })} />
                    {show('command', cmdErr)
                      ? <p className="dk-field-error" id="mcp-command-hint"><Icon name="alert-circle" />{show('command', cmdErr)}</p>
                      : <p className="dk-hint" id="mcp-command-hint">{t('mcpPage.sheet.commandHelp')}</p>}
                  </div>
                  <div className="dk-field">
                    <label className="dk-label" htmlFor="mcp-args" data-optional>{t('mcpPage.sheet.args')}</label>
                    <textarea id="mcp-args" className="dk-textarea dk-input--mono" rows={3} value={form.argsText} aria-describedby="mcp-args-hint"
                      onChange={(e) => set({ argsText: e.target.value })} />
                    <p className="dk-hint" id="mcp-args-hint">{t('mcpPage.sheet.argsHelp')}</p>
                  </div>
                </>
              )}
            </div>

            <div className="mcp-group" role="group" aria-labelledby="mcp-g-signin">
              <h3 className="mcp-group__title" id="mcp-g-signin">{t('mcpPage.sheet.signIn')}</h3>
              <p className="mcp-group__help">{stdio ? t('mcpPage.sheet.envIntro') : t('mcpPage.sheet.signInHelp')}</p>
              {!stdio && (
                <>
                  <div className="dk-field">
                    <label className="dk-label" htmlFor="mcp-token" data-optional>{t('mcpPage.sheet.token')}</label>
                    <SecretInput id="mcp-token" inputRef={tokenRef} value={form.token} disabled={form.clearToken} describedBy="mcp-token-hint"
                      placeholder={form.has_token ? t('mcpPage.sheet.tokenSavedShort') : ''} onChange={(e) => set({ token: e.target.value })} />
                    <p className="dk-hint" id="mcp-token-hint">{form.has_token ? t('mcpPage.sheet.tokenSaved') : t('mcpPage.sheet.tokenHelp')}</p>
                    {form.has_token && (
                      <label className="dk-choice mcp-choice">
                        <input type="checkbox" className="dk-check" checked={form.clearToken} onChange={(e) => set({ clearToken: e.target.checked })} />
                        <span>{t('mcpPage.sheet.clearToken')}</span>
                      </label>
                    )}
                  </div>
                  <PairRows label={t('mcpPage.sheet.headers')} help={form.has_headers ? t('mcpPage.sheet.headersSaved') : t('mcpPage.sheet.headersHelp')} rows={form.headers}
                    onChange={(rows) => set({ headers: rows })} nameLabel={t('mcpPage.sheet.headerName')} valueLabel={t('mcpPage.sheet.headerValue')}
                    addLabel={t('mcpPage.sheet.addHeader')} testId="mcp-headers" />
                  {form.has_headers && (
                    <label className="dk-choice mcp-choice">
                      <input type="checkbox" className="dk-check" checked={form.clearHeaders} onChange={(e) => set({ clearHeaders: e.target.checked })} />
                      <span>{t('mcpPage.sheet.clearHeaders')}</span>
                    </label>
                  )}
                </>
              )}
              {stdio && (
                <>
                  <PairRows label={t('mcpPage.sheet.env')} help={form.has_env ? t('mcpPage.sheet.envSaved') : ''} rows={form.env}
                    onChange={(rows) => set({ env: rows })} nameLabel={t('mcpPage.sheet.envName')} valueLabel={t('mcpPage.sheet.headerValue')}
                    addLabel={t('mcpPage.sheet.addEnv')} testId="mcp-env" />
                  {form.has_env && (
                    <label className="dk-choice mcp-choice">
                      <input type="checkbox" className="dk-check" checked={form.clearEnv} onChange={(e) => set({ clearEnv: e.target.checked })} />
                      <span>{t('mcpPage.sheet.clearEnv')}</span>
                    </label>
                  )}
                </>
              )}
              <p className="mcp-note"><Icon name="lock" aria-hidden="true" />{t('mcpPage.sheet.encrypted')}</p>
            </div>
          </form>
        )}

        {step === 'test' && saved && (
          <div className="mcp-test" data-testid="mcp-sheet-test">
            <div className="mcp-test__head">
              <span className="mcp-test__name">{saved.name}</span>
              <span className="mcp-test__saved"><Icon name="check" aria-hidden="true" />{saved.enabled ? t('mcpPage.sheet.savedOn') : t('mcpPage.sheet.savedOff')}</span>
            </div>
            {testing && (
              <div className="mcp-test__wait" role="status">
                <LoadingSpinner size="sm" />
                <span>{t('mcpPage.sheet.testing', { name: saved.name })}</span>
              </div>
            )}
            {!testing && check?.ok && (
              <>
                <Callout tone="ok" role="status" title={t('mcpPage.sheet.okTitle')} testId="mcp-sheet-ok">
                  {check.tools.length ? t('mcpPage.sheet.okBody', { name: saved.name, count: check.tools.length }) : t('mcpPage.sheet.okNoTools')}
                </Callout>
                {check.tools.length > 0 && <ToolList tools={check.tools} testId="mcp-sheet-tools" />}
              </>
            )}
            {!testing && check && !check.ok && (
              <Callout tone="error" role="alert" title={t('mcpPage.sheet.failTitle')} testId="mcp-sheet-fail">
                <span className="mcp-reason">{sentence(check.error)}</span>{' '}
                {isAuthError(check.error) ? t('mcpPage.sheet.failAuth') : t('mcpPage.sheet.failHint')}
              </Callout>
            )}
          </div>
        )}
      </SideSheet>
      <UnsavedChangesGuard when={dirty} />
      <ConfirmDialog
        open={asking}
        danger
        title={t('mcpPage.sheet.discardTitle')}
        message={t('mcpPage.sheet.discardBody')}
        confirmLabel={t('mcpPage.sheet.discard')}
        cancelLabel={t('mcpPage.sheet.keepEditing')}
        onConfirm={() => { setAsking(false); onClose(saved) }}
        onCancel={() => setAsking(false)}
      />
    </>
  )
}
