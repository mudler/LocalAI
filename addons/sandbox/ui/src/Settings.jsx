import { settingsModel, GVISOR_DOCS } from './lib/model.js'

// The settings section of the Sandbox add-on, drawn on the Settings tab of the
// add-on's details for an administrator. It shows each backend with its state
// (the reason a backend does not work is collapsed under Details), the choice
// with a line for each option, and, for the host backend, the plain warning
// and the phrase that has to be typed. It is drawn with the host's settings
// kit (sdk.ui), so it looks like the host's own settings in both themes.
export function createSettings(sdk, t) {
  const { React } = sdk
  const h = React.createElement
  // eslint-disable-next-line no-unused-vars
  const { useEffect, useState, useCallback } = React
  // eslint-disable-next-line no-unused-vars
  const { Section, List, ListRow, StatusChip, Callout, Code, Choice, Field, TextInput, Button, Actions, ExternalLink } = sdk.ui

  const call = (args, options) => sdk.api.read({ arguments: args }, options)
  const fill = (key, vars) => Object.entries(vars).reduce((s, [k, v]) => s.replace(`{{${k}}}`, v), t(key))

  function BackendRow({ b, active }) {
    const inUse = b.usable && b.name === active
    return (
      <ListRow
        testId="sandbox-backend"
        title={b.name}
        status={(
          <StatusChip tone={inUse ? 'accent' : b.usable ? 'ok' : 'idle'} testId="sandbox-backend-state">
            {inUse ? t('sandbox.settings.inUse') : b.usable ? t('sandbox.settings.usable') : t('sandbox.settings.unavailable')}
          </StatusChip>
        )}
        meta={t(`sandbox.settings.about.${['kvm', 'gvisor', 'host'].includes(b.name) ? b.name : 'other'}`)}
        details={!b.usable && b.reason ? <Code testId="sandbox-backend-reason">{b.reason}</Code> : null}
        detailsLabel={t('sandbox.settings.details')}
      >
        {b.namespaceHint && (
          <p className="sandbox-settings__hint" data-testid="sandbox-gvisor-hint">
            {t('sandbox.settings.gvisorHint')}{' '}
            <ExternalLink href={GVISOR_DOCS} testId="sandbox-gvisor-docs">{t('sandbox.settings.gvisorDocs')}</ExternalLink>
          </p>
        )}
      </ListRow>
    )
  }

  function SandboxSettingsView({ model, draft, phrase, busy, error, onDraft, onPhrase, onSave }) {
    if (model.hidden) return null
    const usable = new Map(model.backends.map(b => [b.name, b.usable]))
    const options = model.options.map(o => ({
      value: o.value,
      label: t(`sandbox.settings.option.${o.value}`),
      description: t(`sandbox.settings.explain.${o.value}`),
      chip: o.value === model.setting
        ? <StatusChip tone="accent">{t('sandbox.settings.saved')}</StatusChip>
        : usable.get(o.value) === false ? <StatusChip tone="idle">{t('sandbox.settings.unavailable')}</StatusChip> : null,
    }))
    return (
      <div className="sandbox-settings" data-testid="sandbox-settings">
        {model.hostNotice && (
          <Callout tone="warn" role="alert" testId="sandbox-host-notice">{t('sandbox.settings.hostNotice')}</Callout>
        )}
        <Section
          title={t('sandbox.settings.backendsTitle')}
          description={model.canRun ? fill('sandbox.settings.runsOn', { backend: model.active }) : t('sandbox.settings.cannotRun')}
          testId="sandbox-backends"
        >
          <List label={t('sandbox.settings.backendsTitle')}>
            {model.backends.map(b => <BackendRow key={b.name} b={b} active={model.active} />)}
          </List>
        </Section>
        <Section title={t('sandbox.settings.choice')} description={t('sandbox.settings.choiceIntro')}>
          <Choice legend={t('sandbox.settings.choice')} hideLegend value={draft} onChange={onDraft} options={options} disabled={busy} testId="sandbox-choice" />
          {draft === 'host' && (
            <Callout tone="warn" title={t('sandbox.settings.hostTitle')} testId="sandbox-host-warning">
              {t('sandbox.settings.hostWarning')}
            </Callout>
          )}
          {draft === 'host' && model.needsHostConfirmation && (
            <Field label={t('sandbox.settings.hostPhraseLabel')} help={t('sandbox.settings.hostPhrase')}>
              <TextInput value={phrase} onChange={onPhrase} disabled={busy} testId="sandbox-host-phrase" />
            </Field>
          )}
          {error && <Callout tone="error" role="alert" testId="sandbox-error">{error}</Callout>}
          <Actions>
            <Button variant="primary" size="sm" busy={busy} disabled={busy || draft === model.setting} onClick={onSave} testId="sandbox-save">{t('sandbox.settings.save')}</Button>
          </Actions>
        </Section>
      </div>
    )
  }

  function SandboxSettings() {
    const [model, setModel] = useState(null)
    const [draft, setDraft] = useState('auto')
    const [phrase, setPhrase] = useState('')
    const [busy, setBusy] = useState(false)
    const [error, setError] = useState('')
    const refresh = useCallback(async () => {
      try {
        const m = settingsModel(await call({ tool: 'status' }))
        setModel(m)
        if (!m.hidden) setDraft(m.setting)
      } catch {
        setModel({ hidden: true })
      }
    }, [])
    useEffect(() => { refresh() }, [refresh])
    if (model === null) return <p className="sandbox-settings__loading" aria-busy="true" data-testid="sandbox-loading">{t('sandbox.settings.loading')}</p>
    const save = async () => {
      setBusy(true)
      setError('')
      try {
        if (draft === 'host' && model.needsHostConfirmation) {
          const ok = await call({ tool: 'confirm-host', phrase })
          if (ok?.ok !== true) throw new Error(ok?.error || t('sandbox.settings.failed'))
        }
        const done = await call({ tool: 'set-backend', backend: draft })
        if (done?.ok !== true) throw new Error(done?.error || t('sandbox.settings.failed'))
        setPhrase('')
        await refresh()
      } catch (e) {
        setError(e?.message || t('sandbox.settings.failed'))
      } finally {
        setBusy(false)
      }
    }
    return <SandboxSettingsView model={model} draft={draft} phrase={phrase} busy={busy} error={error} onDraft={setDraft} onPhrase={setPhrase} onSave={save} />
  }

  return { SandboxSettings, SandboxSettingsView }
}
