/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Field, Notice, RadioCards } from '../../../internal/connector-ui/src/controls'
import en from './i18n/gmail.en.json'
import it from './i18n/gmail.it.json'
import es from './i18n/gmail.es.json'
import de from './i18n/gmail.de.json'
import fr from './i18n/gmail.fr.json'
import zh from './i18n/gmail.zh-CN.json'
import id from './i18n/gmail.id.json'
import ko from './i18n/gmail.ko.json'
import pt from './i18n/gmail.pt-BR.json'
import ProposalCard from './ProposalCard'
import { extraBody, extraForm, extraValid, hasAll, labelsIssue, parseLabels, OFFERED_MODES, writeModeOf } from './labels'
import './gmail.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ gmail: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The labels the agent may use. The host refuses any other one before the
// add-on is called, and the add-on refuses a message without the label. The
// word ALL gives the whole mailbox.
function GmailFields({ form, setForm, t }) {
  const issue = labelsIssue(form.labels_text)
  const list = parseLabels(form.labels_text)
  return (
    <>
      <Field label={t('labels')} hint={t('labelsHelp', { count: list.length })} error={issue ? t(issue.key, { entry: issue.entry }) : undefined}>
        {(p) => (
          <textarea {...p} className="cn-input cn-input--mono" rows={5} data-testid="gmail-labels" value={form.labels_text || ''}
            placeholder={'Work\nINBOX'} onChange={(e) => setForm({ ...form, labels_text: e.target.value })} />
        )}
      </Field>
      {hasAll(list) && <Notice tone="warn" role="note" testId="gmail-all-note" className="gm-note">{t('allNote')}</Notice>}
      <WriteMode form={form} setForm={setForm} t={t} />
    </>
  )
}

// The write mode: the host enforces it for every write tool of this add-on.
function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="gmail-write-mode" className="gm-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={OFFERED_MODES.map((m) => ({
              value: m,
              label: (<span className="gm-modecard"><span className="gm-modecard__title">{t(`writeMode.${m}`)}</span><span className="gm-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="gmail-write-note" className="gm-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

function Help({ t }) {
  return (
    <div className="gm-help">
      <p className="cn-muted" data-testid="gmail-limits">{t('limits')}</p>
      <details className="cn-help" data-testid="gmail-setup">
        <summary>{t('setup.title')}</summary>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
        </ol>
      </details>
    </div>
  )
}

function RowInfo({ c, t }) {
  const labels = c.resources || []
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      <span className={labels.length === 0 ? 'cn-muted' : hasAll(labels) ? 'gm-warn' : 'cn-muted'} data-testid="gmail-row-labels">
        {labels.length === 0 ? t('noLabels') : hasAll(labels) ? t('allMailbox') : t('labelsOf', { count: labels.length, list: labels.join(', ') })}
      </span>
      <span><span className={`gm-chip gm-chip--${mode}`} data-testid="gmail-row-write">{t(`writeMode.row.${mode}`)}</span></span>
    </>
  )
}

// There is no token field: the login is made on the Connectors page, so this
// page edits the connections that exist and has no New button.
const NoToken = () => null

export function createGmail() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="gmail" ns="gmail" testId="gmail-page" TokenFields={NoToken} tokenOf={() => ''} messaging={false} canAdd={false}
          ExtraFields={GmailFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
