/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { RouteProvider } from 'react-router-dom'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Field, Notice, RadioCards } from '../../../internal/connector-ui/src/controls'
import en from './i18n/gdrive.en.json'
import it from './i18n/gdrive.it.json'
import es from './i18n/gdrive.es.json'
import de from './i18n/gdrive.de.json'
import fr from './i18n/gdrive.fr.json'
import zh from './i18n/gdrive.zh-CN.json'
import id from './i18n/gdrive.id.json'
import ko from './i18n/gdrive.ko.json'
import pt from './i18n/gdrive.pt-BR.json'
import ProposalCard from './ProposalCard'
import { extraBody, extraForm, extraValid, foldersIssue, parseFolders, OFFERED_MODES, writeModeOf } from './folders'
import './gdrive.css'

// The words of the page, in every language of the web UI; a key a language lacks reads in English.
registerLocales({ gdrive: { en, it, es, de, fr, 'zh-CN': zh, id, ko, 'pt-BR': pt } })

// The folders the agent may use. The host refuses any other one before the
// add-on is called, and the add-on refuses a file outside the folder.
function GdriveFields({ form, setForm, t }) {
  const issue = foldersIssue(form.folders_text)
  const count = parseFolders(form.folders_text).length
  return (
    <>
      <Field label={t('folders')} hint={t('foldersHelp', { count })} error={issue ? t(issue.key, { entry: issue.entry }) : undefined}>
        {(p) => (
          <textarea {...p} className="cn-input cn-input--mono" rows={5} data-testid="gdrive-folders" value={form.folders_text || ''}
            placeholder="1AbCdEfG..." onChange={(e) => setForm({ ...form, folders_text: e.target.value })} />
        )}
      </Field>
      <WriteMode form={form} setForm={setForm} t={t} />
    </>
  )
}

// The write mode: the host enforces it for every write tool of this add-on.
function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="gdrive-write-mode" className="gd-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={OFFERED_MODES.map((m) => ({
              value: m,
              label: (<span className="gd-modecard"><span className="gd-modecard__title">{t(`writeMode.${m}`)}</span><span className="gd-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="gdrive-write-note" className="gd-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

function Help({ t }) {
  return (
    <div className="gd-help">
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
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      <span className="cn-muted" data-testid="gdrive-row-folders">{folders.length === 0 ? t('noFolders') : t('foldersOf', { count: folders.length, list: folders.join(', ') })}</span>
      <span><span className={`gd-chip gd-chip--${mode}`} data-testid="gdrive-row-write">{t(`writeMode.row.${mode}`)}</span></span>
    </>
  )
}

// There is no token field: the login is made on the Connectors page, so this
// page edits the connections that exist and has no New button.
const NoToken = () => null

export function createGdrive() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="gdrive" ns="gdrive" testId="gdrive-page" TokenFields={NoToken} tokenOf={() => ''} messaging={false} canAdd={false}
          ExtraFields={GdriveFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
