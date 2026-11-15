import { useEffect, useId, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import Button from '../ui/Button'
import Callout from '../ui/Callout'
import Segmented from '../ui/Segmented'
import { KINDS, PRESETS } from '../../utils/remotes'

// The form of "connect a remote" and its test: the kind, the provider (for an
// OpenAI-compatible service), the address, the key and a Test button. The
// answer of the test is a live region that takes focus when it arrives, so a
// keyboard or screen-reader user hears it without hunting for it. The models
// it found are listed by RemoteChooseStep.
//
//   remote   the object useRemoteConnect returns
export default function RemoteConnectStep({ remote }) {
  const { t } = useTranslation('remotes')
  const uid = useId()
  const resultRef = useRef(null)
  const { kind, preset, phase, failure, models } = remote
  const cloud = kind !== 'localai'

  // Move to the answer once the test has one.
  useEffect(() => {
    if (phase === 'ok' || phase === 'failed') resultRef.current?.focus()
  }, [phase])

  const kindOptions = KINDS.map(id => ({ id, label: t(`kinds.${id}`) }))
  const presetOptions = PRESETS.map(p => ({ id: p.id, label: t(`presets.${p.id}`) }))
  const submit = (e) => {
    e.preventDefault()
    if (remote.testable && phase !== 'testing') remote.test()
  }

  return (
    <form className="remotes-form" onSubmit={submit} noValidate data-locked={remote.locked || undefined} data-testid="remote-connect-step">
      <fieldset className="remotes-fields" disabled={remote.locked}>
      <div className="dk-field">
        <span className="dk-label" id={`${uid}-kind`}>{t('connect.kind')}</span>
        <Segmented mode="radio" labelledBy={`${uid}-kind`} options={kindOptions} value={kind} onChange={remote.setKind} testId="remote-kind" itemTestId="remote-kind-" />
      </div>

      {kind === 'openai' && (
        <div className="dk-field">
          <span className="dk-label" id={`${uid}-preset`}>{t('connect.provider')}</span>
          <Segmented mode="radio" labelledBy={`${uid}-preset`} options={presetOptions} value={preset} onChange={remote.setPreset} testId="remote-preset" itemTestId="remote-preset-" />
        </div>
      )}

      {kind !== 'anthropic' && (
        <div className="dk-field">
          <label className="dk-label" htmlFor={`${uid}-url`}>{t('connect.address')}</label>
          <input
            id={`${uid}-url`}
            className="dk-input"
            type="url"
            inputMode="url"
            autoComplete="off"
            spellCheck={false}
            placeholder={kind === 'localai' ? 'https://localai.example.com' : 'https://api.example.com/v1'}
            value={remote.url}
            onChange={e => remote.setUrl(e.target.value)}
            aria-describedby={`${uid}-url-hint`}
            data-testid="remote-url"
          />
          <p className="dk-hint" id={`${uid}-url-hint`}>{t(kind === 'localai' ? 'connect.addressHintLocalai' : 'connect.addressHintOpenai')}</p>
        </div>
      )}

      <div className="dk-field">
        <label className="dk-label" htmlFor={`${uid}-key`}>
          {t('connect.key')}{kind === 'localai' && <span className="remotes-optional"> ({t('connect.optional')})</span>}
        </label>
        <input
          id={`${uid}-key`}
          className="dk-input"
          type="password"
          autoComplete="new-password"
          spellCheck={false}
          value={remote.key}
          onChange={e => remote.setKey(e.target.value)}
          aria-describedby={`${uid}-key-hint`}
          data-testid="remote-key"
        />
        <p className="dk-hint" id={`${uid}-key-hint`}>{t('connect.keyHint')}</p>
      </div>

      {cloud && <p className="remotes-note" data-testid="remote-pii">{t('connect.pii')}</p>}

      <div className="remotes-actions">
        <Button type="submit" variant="secondary" icon="plug" busy={phase === 'testing'} disabled={!remote.testable} data-testid="remote-test">
          {phase === 'testing' ? t('connect.testing') : t('connect.test')}
        </Button>
      </div>
      </fieldset>

      <div className="remotes-result" ref={resultRef} tabIndex={-1} aria-live="polite" data-testid="remote-result">
        {phase === 'ok' && (
          <Callout tone="ok" role="status" testId="remote-ok">{t('connect.ok', { count: models.length })}</Callout>
        )}
        {phase === 'ok' && remote.notice && (
          <Callout tone="warn" testId="remote-notice">{remote.notice}</Callout>
        )}
        {phase === 'failed' && failure && (
          <Callout tone="error" role="alert" title={t('connect.failTitle')} testId="remote-fail" data-step={failure.step}>
            {t(`connect.fail.${failure.step === 'auth' && kind === 'localai' ? 'authLocalai' : failure.step}`)}
            {failure.message && <span className="remotes-detail"> {failure.message}</span>}
          </Callout>
        )}
      </div>
    </form>
  )
}
