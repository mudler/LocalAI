/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { RouteProvider } from 'react-router-dom'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Check, Field, Hint, Notice } from '../../../internal/connector-ui/src/controls'
import { WriteModeChip, WriteModeField } from '../../../internal/connector-ui/src/addonparts'
import './i18n'
import ProposalCard from './ProposalCard'
import { ALL_FOLDERS, extraBody, extraForm, extraValid, foldersIssue, parseFolders, OFFERED_MODES, writeModeOf } from './folders'

// The folders the agent may use. The host refuses any other one before the
// add-on is called, and the add-on refuses a file outside the folder. All
// folders is the list *: the host still holds the connection to its list, and
// an empty list still means no access.
function GdriveFields({ form, setForm, t }) {
  const all = Boolean(form.all_folders)
  const issue = all ? null : foldersIssue(form.folders_text)
  const count = parseFolders(form.folders_text).length
  return (
    <>
      <div className="dk-field" data-testid="gdrive-all">
        <Check checked={all} testId="gdrive-all-folders" onChange={(on) => setForm({ ...form, all_folders: on, all_ack: false })}>{t('allFolders.label')}</Check>
        <Hint>{t('allFolders.hint')}</Hint>
      </div>
      {all ? (
        <Notice tone="warn" role="alert" testId="gdrive-all-warning">
          <p className="cn-notice__text">{t('allFolders.warning')}</p>
          {!form.all_was && <Check checked={Boolean(form.all_ack)} testId="gdrive-all-ack" onChange={(on) => setForm({ ...form, all_ack: on })}>{t('allFolders.confirm')}</Check>}
        </Notice>
      ) : (
        <Field label={t('folders')} hint={t('foldersHelp', { count })} error={issue ? t(issue.key, { entry: issue.entry }) : undefined}>
          {(p) => (
            <textarea {...p} className="cn-input cn-input--mono" rows={5} data-testid="gdrive-folders" value={form.folders_text || ''}
              placeholder="1AbCdEfG..." onChange={(e) => setForm({ ...form, folders_text: e.target.value })} />
          )}
        </Field>
      )}
      <WriteModeField ns="gdrive" form={form} setForm={setForm} t={t} modes={OFFERED_MODES} modeOf={writeModeOf} />
    </>
  )
}

function Help({ t }) {
  return (
    <div className="cn-addon-help">
      <p className="cn-muted" data-testid="gdrive-limits">{t('limits')}</p>
      <details className="cn-help" data-testid="gdrive-setup">
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
  const folders = c.resources || []
  const all = folders.includes(ALL_FOLDERS)
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      <span className="cn-muted" data-testid="gdrive-row-folders">{all ? t('allFolders.row') : folders.length === 0 ? t('noFolders') : t('foldersOf', { count: folders.length, list: folders.join(', ') })}</span>
      <span><WriteModeChip ns="gdrive" mode={mode} t={t} /></span>
    </>
  )
}

// There is no token field: the host logs the person in (the Connect with
// Google button on this page), so the page has no New sheet for a token.
const NoToken = () => null

export function createGdrive() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="gdrive" ns="gdrive" testId="gdrive-page" TokenFields={NoToken} tokenOf={() => ''} messaging={false} login
          ExtraFields={GdriveFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
