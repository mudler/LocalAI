import { settingsModel, draftOf, rows, toggle, move, setKey, clearKey, change, testRows, KEYLESS, KEYLESS_ENTRY } from './lib/model.js'

// The settings section of the Web add-on, drawn on the Settings tab of the
// add-on's details for an administrator: the search providers in the order
// they are tried, a field for each key (a saved key is never shown, only that
// it is set), the Google search engine ID, the SearXNG address, and a test of
// each provider. It is drawn with the host's settings kit (sdk.ui).
export function createSettings(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState, useCallback } = React
  // eslint-disable-next-line no-unused-vars
  const { Section, List, ListRow, StatusChip, Callout, Field, TextInput, Switch, Button, Actions } = sdk.ui

  const call = (args, options) => sdk.api.read({ arguments: args }, options)
  const name = (n) => t(`web.provider.${n}`)
  // A page of the keyless chain, in a test row or a cool-down note.
  const pageName = (n) => (KEYLESS.includes(n) ? t(`web.keyless.${n}`) : name(n))

  function ProviderRow({ p, draft, busy, onDraft }) {
    const keyShown = p.keySet && !draft.clear.includes(p.name)
    return (
      <ListRow
        testId="web-provider"
        title={(
          <span className="web-settings__name" data-provider={p.name} data-on={p.on ? 'true' : 'false'}>
            <Switch checked={p.on} disabled={busy} label={t('web.settings.use').replace('{{name}}', name(p.name))} onChange={() => onDraft(toggle(draft, p.name))} testId={`web-use-${p.name}`} />
            <span>{p.on ? `${p.position + 1}. ` : ''}{name(p.name)}</span>
          </span>
        )}
        status={<StatusChip tone={p.ready ? 'ok' : 'idle'}>{p.ready ? t('web.settings.ready') : t('web.settings.notReady')}</StatusChip>}
        meta={!p.ready && p.missing ? p.missing : (p.name === KEYLESS_ENTRY ? t('web.settings.keylessChain') : null)}
      >
        {p.on && (
          <Actions>
            <Button size="sm" variant="ghost" icon="chevron-up" disabled={busy || p.position === 0} onClick={() => onDraft(move(draft, p.name, -1))} aria-label={t('web.settings.up').replace('{{name}}', name(p.name))}>{t('web.settings.upShort')}</Button>
            <Button size="sm" variant="ghost" icon="chevron-down" disabled={busy || p.position === draft.order.length - 1} onClick={() => onDraft(move(draft, p.name, 1))} aria-label={t('web.settings.down').replace('{{name}}', name(p.name))}>{t('web.settings.downShort')}</Button>
          </Actions>
        )}
        {p.keyed && (
          <Field label={t('web.settings.key')} help={keyShown ? t('web.settings.keySet') : t('web.settings.keyNotSet')}>
            <TextInput type="password" value={draft.keys[p.name]} disabled={busy} label={t('web.settings.keyOf').replace('{{name}}', name(p.name))} onChange={v => onDraft(setKey(draft, p.name, v))} testId={`web-key-${p.name}`} />
            {keyShown && <Button size="sm" variant="ghost" disabled={busy} onClick={() => onDraft(clearKey(draft, p.name))} testId={`web-clear-${p.name}`}>{t('web.settings.removeKey')}</Button>}
          </Field>
        )}
        {p.name === 'google' && (
          <Field label={t('web.settings.googleEngine')} help={t('web.settings.googleEngineHelp')}>
            <TextInput value={draft.googleEngine} disabled={busy} onChange={v => onDraft({ ...draft, googleEngine: v })} testId="web-google-engine" mono />
          </Field>
        )}
        {p.name === 'searxng' && (
          <Field label={t('web.settings.searxngUrl')} help={t('web.settings.searxngHelp')}>
            <TextInput type="url" value={draft.searxngUrl} disabled={busy} placeholder="https://searx.example.org" onChange={v => onDraft({ ...draft, searxngUrl: v })} testId="web-searxng-url" mono />
          </Field>
        )}
      </ListRow>
    )
  }

  function WebSettingsView({ model, draft, busy, error, saved, tests, onDraft, onSave, onTest }) {
    if (model.hidden) return null
    const list = rows(model, draft)
    return (
      <div className="web-settings" data-testid="web-settings">
        {model.note && <Callout tone="info" role="status">{model.note}</Callout>}
        <Section
          title={t('web.settings.providersTitle')}
          description={t('web.settings.intro')}
        >
          <p className="web-settings__serving" data-testid="web-serving">
            {model.serving.length > 0
              ? t('web.settings.serving').replace('{{list}}', model.serving.map(name).join(', '))
              : t('web.settings.servingNone')}
          </p>
          {model.cooling.length > 0 && (
            <ul className="web-settings__cooling" data-testid="web-cooling">
              {model.cooling.map(c => (
                <li key={c.name}>{t('web.settings.cooling').replace('{{name}}', pageName(c.name)).replace('{{reason}}', c.reason)}</li>
              ))}
            </ul>
          )}
          <List label={t('web.settings.providersTitle')}>
            {list.map(p => <ProviderRow key={p.name} p={p} draft={draft} busy={busy} onDraft={onDraft} />)}
          </List>
          {error && <Callout tone="error" role="alert" testId="web-error">{error}</Callout>}
          {saved && <Callout tone="ok" role="status" testId="web-saved">{t('web.settings.saved')}</Callout>}
          <Actions>
            <Button variant="primary" size="sm" onClick={onSave} disabled={busy || draft.order.length === 0} testId="web-save">{t('web.settings.save')}</Button>
            <Button size="sm" icon="refresh" onClick={onTest} disabled={busy} testId="web-test">{t('web.settings.test')}</Button>
          </Actions>
        </Section>
        {tests.length > 0 && (
          <Section title={t('web.settings.testsTitle')}>
            <List label={t('web.settings.testsTitle')} testId="web-tests">
              {tests.map(r => (
                <ListRow
                  key={r.name}
                  testId="web-test-row"
                  title={pageName(r.name)}
                  status={<StatusChip tone={r.ok ? 'ok' : 'error'}>{r.ok ? t('web.settings.testPassed') : t('web.settings.testFailed')}</StatusChip>}
                  meta={r.ok ? t('web.settings.testOk').replace('{{count}}', String(r.results)).replace('{{host}}', r.host) : r.error}
                />
              ))}
            </List>
          </Section>
        )}
      </div>
    )
  }

  function WebSettings() {
    const [model, setModel] = useState(null)
    const [draft, setDraft] = useState(null)
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const [saved, setSaved] = useState(false)
    const [tests, setTests] = useState([])
    const refresh = useCallback(async () => {
      try {
        const m = settingsModel(await call({ tool: 'status' }))
        setModel(m)
        if (!m.hidden) setDraft(draftOf(m))
      } catch {
        setModel({ hidden: true })
      }
    }, [])
    useEffect(() => { refresh() }, [refresh])
    if (model === null) return <p aria-busy="true" data-testid="web-loading">{t('web.settings.loading')}</p>
    if (model.hidden || !draft) return null
    const onDraft = (d) => { setDraft(d); setSaved(false); setError('') }
    const onSave = async () => {
      setBusy(true)
      setError('')
      setSaved(false)
      try {
        const done = await call({ tool: 'set-settings', ...change(draft) })
        if (!done || done.ok !== true) {
          setError((done && done.error) || t('web.settings.failed'))
        } else {
          setSaved(true)
          await refresh()
        }
      } catch {
        setError(t('web.settings.failed'))
      } finally {
        setBusy(false)
      }
    }
    const onTest = async () => {
      setBusy(true)
      setError('')
      try {
        const answer = await call({ tool: 'test-search' })
        if (!answer || answer.ok !== true) setError((answer && answer.error) || t('web.settings.failed'))
        setTests(testRows(answer))
      } catch {
        setError(t('web.settings.failed'))
      } finally {
        setBusy(false)
      }
    }
    return h(WebSettingsView, { model, draft, busy, error, saved, tests, onDraft, onSave, onTest })
  }

  return { WebSettings, WebSettingsView }
}
