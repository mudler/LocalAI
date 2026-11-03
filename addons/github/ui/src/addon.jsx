/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { RouteProvider } from 'react-router-dom'
import { getSdk, } from '../../../internal/host-compat/src/runtime.js'
import { registerLocales } from '../../../internal/host-compat/src/i18n.js'
import ConnectorPage from '../../../internal/connector-ui/src/ConnectorPage'
import { Field, FieldError, Notice, RadioCards, TextInput } from '../../../internal/connector-ui/src/controls'
import en from './i18n/github.en.json'
import { checkLine, extraBody, extraForm, extraValid, githubSecret, parseRepos, reposProblem, tokenProblem, WRITE_MODES, writeModeOf } from './repos'

registerLocales({ github: { en } })

// The fine-grained token. It is typed only to replace it.
function GitHubToken({ form, setForm, t }) {
  const problem = tokenProblem(form)
  return (
    <Field label={t('token')} hint={t('tokenHelp')}>
      {(p) => (
        <TextInput {...p} mono type="password" autoComplete="off" value={form.token || ''} required={!form.id}
          placeholder={form.id ? t('tokenSaved') : ''} aria-invalid={problem ? 'true' : undefined}
          onChange={(e) => setForm({ ...form, token: e.target.value })} />
      )}
    </Field>
  )
}

// The repositories the agent may read. The host refuses any other one before
// the add-on is called.
function GitHubFields({ form, setForm, t }) {
  const problem = reposProblem(form.repos_text)
  const count = parseRepos(form.repos_text).length
  return (
    <>
      {tokenProblem(form) && <FieldError role="status" data-testid="token-problem">{t(tokenProblem(form))}</FieldError>}
      <Field label={t('repos')} hint={t('reposHelp', { count })}>
        {(p) => (
          <textarea {...p} className="cn-input cn-input--mono" rows={5} data-testid="github-repos" value={form.repos_text || ''}
            placeholder="owner/name" aria-invalid={problem ? 'true' : undefined}
            onChange={(e) => setForm({ ...form, repos_text: e.target.value })} />
        )}
      </Field>
      {problem && <FieldError role="status" data-testid="repos-problem">{t(problem)}</FieldError>}
      <WriteMode form={form} setForm={setForm} t={t} />
    </>
  )
}

// The write mode: the host enforces it for every write tool of this add-on.
function WriteMode({ form, setForm, t }) {
  const mode = writeModeOf(form.write_mode)
  return (
    <div data-testid="github-write-mode">
      <Field label={t('writeMode.label')} hint={t('writeMode.hint')}>
        {() => (
          <RadioCards name="write-mode" legend={t('writeMode.label')} value={mode} onChange={(m) => setForm({ ...form, write_mode: m })}
            options={WRITE_MODES.map((m) => ({ value: m, label: t(`writeMode.${m}`) }))} />
        )}
      </Field>
      <Notice tone={mode === 'off' ? 'info' : 'warn'} role="note" testId="github-write-note">{t(`writeMode.note.${mode}`)}</Notice>
    </div>
  )
}

function Help({ t }) {
  return (
    <details className="cn-help" data-testid="github-setup">
      <summary>{t('setup.title')}</summary>
      <ol>
        <li>{t('setup.step1')}</li>
        <li>{t('setup.step2')}</li>
        <li>{t('setup.step3')}</li>
      </ol>
    </details>
  )
}

function RowInfo({ c, t }) {
  const repos = c.resources || []
  return (
    <>
      <span className="cn-muted" data-testid="github-row-repos">{repos.length === 0 ? t('noRepos') : t('reposOf', { count: repos.length, list: repos.join(', ') })}</span>
      <span className="cn-muted" data-testid="github-row-write">{t(`writeMode.row.${writeModeOf(c.write_mode)}`)}</span>
    </>
  )
}

// The Test button: the host asks who the token is; this asks, for each listed
// repository, whether the token reaches it.
async function testExtra(c, t) {
  const lines = []
  for (const repo of c.resources || []) {
    try {
      const r = await getSdk().api.read({ arguments: { tool: 'check', repo } })
      lines.push(checkLine(r?.data ? { ...r.data, repo } : { repo, reachable: false }, t))
    } catch (_e) {
      lines.push(checkLine({ repo, reachable: false }, t))
    }
  }
  return lines.join(' ')
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
  return { Component, components: {}, chatActions: [], dispose() {} }
}
