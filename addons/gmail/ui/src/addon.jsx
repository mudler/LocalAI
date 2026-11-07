/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { RouteProvider } from 'react-router-dom'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Field, Notice } from '../../../internal/connector-ui/src/controls'
import { WriteModeChip, WriteModeField } from '../../../internal/connector-ui/src/addonparts'
import './i18n'
import ProposalCard from './ProposalCard'
import { extraBody, extraForm, extraValid, hasAll, labelsIssue, parseLabels, OFFERED_MODES, writeModeOf } from './labels'

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
      {hasAll(list) && <Notice tone="warn" role="note" testId="gmail-all-note" className="cn-note">{t('allNote')}</Notice>}
      <WriteModeField ns="gmail" form={form} setForm={setForm} t={t} modes={OFFERED_MODES} modeOf={writeModeOf} />
    </>
  )
}

function Help({ t }) {
  return (
    <div className="cn-addon-help">
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
      <span className={labels.length === 0 ? 'cn-muted' : hasAll(labels) ? 'cn-warn' : 'cn-muted'} data-testid="gmail-row-labels">
        {labels.length === 0 ? t('noLabels') : hasAll(labels) ? t('allMailbox') : t('labelsOf', { count: labels.length, list: labels.join(', ') })}
      </span>
      <span><WriteModeChip ns="gmail" mode={mode} t={t} /></span>
    </>
  )
}

// There is no token field: the host logs the person in (the Connect with
// Google button of the page), so the page has no New sheet for a token.
const NoToken = () => null

export function createGmail() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="gmail" ns="gmail" testId="gmail-page" TokenFields={NoToken} tokenOf={() => ''} messaging={false} login
          ExtraFields={GmailFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
