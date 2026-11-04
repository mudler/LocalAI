/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useState } from 'react'
import { RouteProvider } from 'react-router-dom'
import { getSdk, } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import Icon from '../../../internal/host-compat/src/components/Icon'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { connectorsApi } from '../../../internal/connector-ui/src/api'
import { Field, Notice, RadioCards, TextInput } from '../../../internal/connector-ui/src/controls'
import en from './i18n/github.en.json'
import ProposalCard from './ProposalCard'
import { checkFailure, checkItem, extraBody, extraForm, extraValid, followName, githubSecret, parseRepos, PERMISSIONS, reposIssue, tokenProblem, WRITE_MODES, writeModeOf } from './repos'
import { finishTest, resultOf, startTest, subscribe } from './results'
import './github.css'

registerLocales({ github: { en } })

const TOKEN_URL = 'https://github.com/settings/personal-access-tokens/new'

function TokenLink({ t, className, label }) {
  return (
    <a className={className} href={TOKEN_URL} target="_blank" rel="noopener noreferrer" aria-label={t('setup.linkLabel')} data-testid="github-token-link">
      {label || t('tokenLink')}
    </a>
  )
}

// The fine-grained token. It is typed only to replace it.
function GitHubToken({ form, setForm, t }) {
  const problem = tokenProblem(form)
  const hint = <>{t('tokenHelp')}{!form.id && <>{' '}<TokenLink t={t} className="gh-link" /></>}</>
  return (
    <Field label={t('token')} hint={hint} error={problem ? t(problem) : undefined}>
      {(p) => (
        <TextInput {...p} mono type="password" autoComplete="off" value={form.token || ''} required={!form.id}
          placeholder={form.id ? t('tokenSaved') : ''}
          onChange={(e) => setForm({ ...form, token: e.target.value })} />
      )}
    </Field>
  )
}

// The repositories the agent may use. The host refuses any other one before
// the add-on is called.
function GitHubFields({ form, setForm, t }) {
  const issue = reposIssue(form.repos_text)
  const count = parseRepos(form.repos_text).length
  const change = (text) => setForm({ ...form, repos_text: text, name: followName(form.name, form.repos_text, text) })
  return (
    <>
      <Field label={t('repos')} hint={t('reposHelp', { count })} error={issue ? t(issue.key, { entry: issue.entry }) : undefined}>
        {(p) => (
          <textarea {...p} className="cn-input cn-input--mono" rows={5} data-testid="github-repos" value={form.repos_text || ''}
            placeholder="owner/name" onChange={(e) => change(e.target.value)} />
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
    <div data-testid="github-write-mode" className="gh-mode-block">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({
              value: m,
              label: (<span className="gh-modecard"><span className="gh-modecard__title">{t(`writeMode.${m}`)}</span><span className="gh-modecard__line">{t(`writeMode.line.${m}`)}</span></span>),
            }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="github-write-note" className="gh-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

// What the page does not do, always shown; and how to make the token, open
// while there is no connection yet.
function Help({ t }) {
  const [open, setOpen] = useState(null)
  useEffect(() => {
    let live = true
    connectorsApi.list().then((r) => { if (live && open === null) setOpen(!(r.connectors || []).some((c) => c.kind === 'github')) }).catch(() => {})
    return () => { live = false }
    // eslint-disable-next-line react-hooks/exhaustive-deps -- runs once; the person's own toggle wins afterwards
  }, [])
  const level = (k) => t(`setup.table.levels.${k}`)
  return (
    <div className="gh-help">
      <p className="cn-muted" data-testid="github-limits">{t('limits')}</p>
      <details className="cn-help" data-testid="github-setup" open={open === true} onToggle={(e) => setOpen(e.currentTarget.open)}>
        <summary>{t('setup.title')}</summary>
        <p><TokenLink t={t} className="dk-btn dk-btn--secondary dk-btn--sm" label={t('setup.link')} /></p>
        <ol>
          <li>{t('setup.step1')}</li>
          <li>{t('setup.step2')}</li>
          <li>{t('setup.step3')}</li>
          <li>{t('setup.step4')}</li>
        </ol>
        <table className="gh-perms" data-testid="github-perms">
          <caption>{t('setup.table.caption')}</caption>
          <thead>
            <tr><th scope="col">{t('setup.table.permission')}</th><th scope="col">{t('setup.table.read')}</th><th scope="col">{t('setup.table.write')}</th></tr>
          </thead>
          <tbody>
            {PERMISSIONS.map((p) => (
              <tr key={p.id}><th scope="row">{t(`setup.table.${p.id}`)}</th><td>{level(p.read)}</td><td>{level(p.write)}</td></tr>
            ))}
          </tbody>
        </table>
      </details>
    </div>
  )
}

function useResult(id) {
  const [, tick] = useState(0)
  useEffect(() => subscribe(() => tick((n) => n + 1)), [])
  return resultOf(id)
}

// The Test result of one connection: a line for each repository, drawn in
// its row, with a tone.
function Checks({ c, t }) {
  const r = useResult(c.id)
  if (!r) return null
  if (r.busy) return <p className="cn-muted" role="status" aria-busy="true" data-testid="github-checking">{t('check.running')}</p>
  return (
    <div data-testid="github-checks">
      <ul className="gh-checks" aria-label={t('check.title', { name: c.name })}>
        {r.items.map((i) => (
          <li key={i.repo} className={`gh-check gh-check--${i.tone}`}>
            <Icon name={i.tone === 'ok' ? 'check-circle' : 'warning'} />
            <span>{i.text}</span>
          </li>
        ))}
      </ul>
      {r.at > 0 && <p className="cn-muted">{t('check.at', { time: new Date(r.at).toLocaleTimeString() })}</p>}
    </div>
  )
}

function RowInfo({ c, t }) {
  const repos = c.resources || []
  const shown = Boolean(resultOf(c.id)) && repos.length > 0
  const mode = writeModeOf(c.write_mode)
  return (
    <>
      {!shown && <span className="cn-muted" data-testid="github-row-repos">{repos.length === 0 ? t('noRepos') : t('reposOf', { count: repos.length, list: repos.join(', ') })}</span>}
      <span><span className={`gh-chip gh-chip--${mode}`} data-testid="github-row-write">{t(`writeMode.row.${mode}`)}</span></span>
      <Checks c={c} t={t} />
    </>
  )
}

// The Test button: the host asks who the token is; this asks, for each listed
// repository, whether the token reaches it. The result goes to the row, so the
// text it returns for the top notice is empty.
async function testExtra(c, t) {
  startTest(c.id)
  // The check runs from this page, not from a chat, so the host has no chat
  // to read a choice from and uses the connection only when there is exactly
  // one. With several, it would be a guess, and the host does not guess.
  try {
    const all = await connectorsApi.list()
    if ((all.connectors || []).filter((x) => x.kind === 'github').length > 1) {
      finishTest(c.id, [])
      return t('checkSeveral')
    }
  } catch (_e) { /* the repository checks below say what they can */ }
  const items = []
  for (const repo of c.resources || []) {
    let r
    try {
      const res = await getSdk().api.read({ arguments: { tool: 'check', repo } })
      r = res?.data ? { ...res.data, repo } : { repo, reachable: false }
    } catch (e) {
      r = { repo, failed: checkFailure(e) }
    }
    const i = checkItem(r)
    items.push({ repo, tone: i.tone, text: t(i.key, i.vars) })
  }
  finishTest(c.id, items)
  return ''
}

export function createGitHub() {
  function Component({ page, path, search }) {
    if (page !== 'apps') return null
    return (
      <RouteProvider value={{ page, path: path || '', search: search || '' }}>
        <ConnectorPage kind="github" ns="github" testId="github-page" TokenFields={GitHubToken} tokenOf={githubSecret} messaging={false}
          ExtraFields={GitHubFields} extraBody={extraBody} extraForm={extraForm} extraValid={extraValid} testExtra={testExtra}
          Help={Help} RowInfo={RowInfo} />
      </RouteProvider>
    )
  }
  return { Component, components: { ProposalCard }, chatActions: [], dispose() {} }
}
