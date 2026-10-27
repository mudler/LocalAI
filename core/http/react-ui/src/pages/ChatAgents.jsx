/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link, useNavigate } from 'react-router-dom'
import ChatDeleteConfirm from '../components/ChatDeleteConfirm'
import Icon from '../components/Icon'
import ModelSelector from '../components/ModelSelector'
import { useAddonToolAddons } from '../addons/useAddonRegistry'
import { agentCollectionsApi, chatAgentsApi, chatSkillsApi } from '../utils/api'
import { CAP_CHAT } from '../utils/capabilities'
import NameList from '../components/ChatNameList'
import { agentPayload, emptyAgent, grantSummary } from '../utils/chatAgents'
import './chatskills.css'
import './chatauto.css'

export default function ChatAgents() {
  const { t } = useTranslation('chats')
  const navigate = useNavigate()
  const addonList = useAddonToolAddons()
  const [agents, setAgents] = useState([])
  const [off, setOff] = useState('')
  const [error, setError] = useState('')
  const [form, setForm] = useState(null)
  // The model selector picks a model on its own, so the agent has a fixed model only when asked.
  const [fixModel, setFixModel] = useState(false)
  const [busy, setBusy] = useState(false)
  const [toDelete, setToDelete] = useState(null)
  const [skillNames, setSkillNames] = useState([])
  const [collectionNames, setCollectionNames] = useState([])

  const fail = useCallback((e) => {
    if (e?.status === 409 && e.body?.error?.type === 'agents_off') { setOff(e.body.error.message); return }
    setError(e?.message || t('errors.send'))
  }, [t])

  const load = useCallback(async () => {
    try {
      const res = await chatAgentsApi.list()
      setAgents(res.agents || [])
      setOff('')
    } catch (e) { fail(e) }
  }, [fail])

  useEffect(() => { load() }, [load])
  // Suggestions are best effort: a failure leaves the field free text.
  useEffect(() => {
    chatSkillsApi.list().then((r) => setSkillNames([...new Set((r.skills || []).filter((s) => !s.invalid).map((s) => s.name))])).catch(() => {})
    agentCollectionsApi.list().then((r) => setCollectionNames((r.collections || []).map((c) => (typeof c === 'string' ? c : c.name)).filter(Boolean))).catch(() => {})
  }, [])

  const save = async (e) => {
    e.preventDefault()
    setBusy(true)
    setError('')
    try {
      const body = agentPayload(form)
      if (form.id) await chatAgentsApi.update(form.id, body)
      else await chatAgentsApi.create(body)
      setForm(null)
      await load()
    } catch (err) { fail(err) } finally { setBusy(false) }
  }

  const remove = async (a) => {
    setError('')
    try {
      await chatAgentsApi.remove(a.id)
      await load()
    } catch (e) { fail(e) }
  }

  const confirmDelete = (a) => { setToDelete(null); remove(a) }

  const openForm = (a) => { setForm(a); setFixModel(Boolean(a.model)) }

  const startChat = (a) => navigate('/app/chats', { state: { agentId: a.id } })

  if (off) {
    return (
      <div className="cs-page" data-testid="chat-agents-page">
        <h1 className="cs-title">{t('agentsPage.title')}</h1>
        <div className="cs-notice" role="note">{off}</div>
      </div>
    )
  }

  return (
    <div className="cs-page" data-testid="chat-agents-page">
      <header className="cs-head">
        <h1 className="cs-title">{t('agentsPage.title')}</h1>
        <button type="button" className="btn btn-primary btn-sm" onClick={() => openForm(emptyAgent())}>
          <Icon name="plus" /> {t('agentsPage.new')}
        </button>
      </header>
      <p className="cs-muted">{t('agentsPage.intro')}</p>

      {error && (
        <div className="cs-error" role="alert">
          <span>{error}</span>
          <button type="button" className="cs-link" onClick={() => setError('')}>{t('errors.dismiss')}</button>
        </div>
      )}

      {form && (
        <form className="cs-form" onSubmit={save} aria-label={form.id ? t('agentsPage.edit') : t('agentsPage.new')}>
          <label className="cs-field">
            <span>{t('agentsPage.name')}</span>
            <input className="input" value={form.name} required maxLength={80} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </label>
          <label className="cs-field">
            <span>{t('agentsPage.description')}</span>
            <input className="input" value={form.description} maxLength={400} onChange={(e) => setForm({ ...form, description: e.target.value })} />
          </label>
          <label className="cs-field">
            <span>{t('agentsPage.instructions')}</span>
            <textarea className="input cs-body" rows={8} value={form.instructions} onChange={(e) => setForm({ ...form, instructions: e.target.value })} />
          </label>
          <div className="cs-field">
            <span>{t('agentsPage.model')}</span>
            <label className="ca-radio">
              <input type="checkbox" checked={fixModel}
                onChange={(e) => { setFixModel(e.target.checked); if (!e.target.checked) setForm({ ...form, model: '' }) }} />
              <span>{t('agentsPage.fixModel')}</span>
            </label>
            {fixModel && <ModelSelector value={form.model} onChange={(m) => setForm({ ...form, model: m })} capability={CAP_CHAT} />}
          </div>
          <p className="cs-muted">{t('agentsPage.grants')}</p>
          <NameList label={t('agentsPage.addons')} hint={t('agentsPage.addonsHint')} value={form.addons}
            suggestions={addonList.map((a) => a.id)} onChange={(v) => setForm({ ...form, addons: v })} testId="agent-addons" />
          <NameList label={t('agentsPage.mcp')} hint={t('agentsPage.mcpHint')} value={form.mcp_servers}
            onChange={(v) => setForm({ ...form, mcp_servers: v })} testId="agent-mcp" />
          <NameList label={t('agentsPage.collections')} hint={t('agentsPage.collectionsHint')} value={form.collections}
            suggestions={collectionNames} onChange={(v) => setForm({ ...form, collections: v })} testId="agent-collections" />
          <NameList label={t('agentsPage.skills')} hint={t('agentsPage.skillsHint')} value={form.skills}
            suggestions={skillNames} onChange={(v) => setForm({ ...form, skills: v })} testId="agent-skills" />
          <div className="cs-actions">
            <button type="submit" className="btn btn-primary btn-sm" disabled={busy}>{t('agentsPage.save')}</button>
            <button type="button" className="btn btn-secondary btn-sm" onClick={() => setForm(null)}>{t('agentsPage.cancel')}</button>
          </div>
        </form>
      )}

      <ul className="cs-list">
        {agents.map((a) => (
          <li key={a.id} className="cs-row" data-testid="agent-row">
            <div className="cs-row__text">
              <span className="cs-row__name">{a.name}</span>
              <span className="cs-muted">{a.description || grantSummary(a) || t('agentsPage.noGrants')}</span>
              {a.description && grantSummary(a) && <span className="cs-muted">{grantSummary(a)}</span>}
            </div>
            <span className="cs-row__actions">
              <button type="button" className="btn btn-primary btn-sm" onClick={() => startChat(a)}>{t('agentsPage.chat')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => openForm({ ...emptyAgent(), ...a })}>{t('agentsPage.editButton')}</button>
              <button type="button" className="btn btn-secondary btn-sm" onClick={() => setToDelete(a)}>{t('agentsPage.delete')}</button>
            </span>
          </li>
        ))}
      </ul>
      {agents.length === 0 && !form && <p className="cs-muted">{t('agentsPage.empty')}</p>}
      <p className="cs-muted"><Link to="/app/chats">{t('skillsPage.back')}</Link></p>
      <ChatDeleteConfirm item={toDelete} kind="agent" onConfirm={confirmDelete} onCancel={() => setToDelete(null)} />
    </div>
  )
}
