/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useEffect, useId, useMemo, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Dialog from '../components/Dialog'
import SideSheet from '../components/SideSheet'
import { HubHeader } from '../components/hub/HubHeader'
import P2PTabs from '../components/p2p/P2PTabs'
import { Button, Callout, EmptyState, LoadError, Segmented, SkeletonRows, StatusChip, Tag, useLoad } from '../components/ui'
import { federationApi } from '../utils/api'
import { candidateGroups, instancePath, memberTone, POLICIES, shortPeer } from '../utils/federation'
import '../components/p2p/p2p.css'

// How often the page reads the federation again: the requests in flight and
// the health of the members change while it is open.
const REFRESH_MS = 5_000

// Where a person adds another LocalAI: a remote, by invite link or address.
const ADD_INSTANCE_PATH = '/app/models?view=remotes'

// Swarm, P2P, Federation: the LocalAI instances that can serve a group (this
// server and its LocalAI remotes, usually over P2P) and the groups, each one
// model name served by several instances with least busy or round robin and
// failover. A group is a model config, so it also shows on the Failover page.
export default function Federation() {
  const { t } = useTranslation('p2p')
  const { addToast } = useOutletContext() || {}
  const fed = useLoad(() => federationApi.get(), [])
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState(null)

  const reload = fed.reload
  useEffect(() => {
    const id = setInterval(reload, REFRESH_MS)
    return () => clearInterval(id)
  }, [reload])

  const data = fed.status === 'ready' ? fed.data : null
  const groups = data?.groups || []
  const instances = data?.instances || []
  const empty = data && groups.length === 0

  return (
    <div className="page page--wide p2p-page" data-testid="federation-page">
      <h1 className="dk-sr-only">{t('federation.title')}</h1>
      <HubHeader
        supporting={t('supporting')}
        actions={data && !empty && (
          <Button variant="primary" icon="plus" onClick={() => setCreating(true)} data-testid="federation-new">{t('federation.groups.new')}</Button>
        )}
      />
      <P2PTabs />
      {fed.status === 'loading' && <SkeletonRows count={3} />}
      {fed.status === 'error' && <LoadError title={t('federation.loadFailed')} error={fed.error} onRetry={fed.retry} />}
      {data && (
        <>
          <GroupsSection groups={groups} instances={instances} onNew={() => setCreating(true)} onDelete={setDeleting} addToast={addToast} reload={reload} />
          <InstancesSection instances={instances} />
        </>
      )}
      {creating && (
        <NewGroupSheet
          instances={instances}
          onClose={() => setCreating(false)}
          onSaved={(g) => {
            addToast?.(t('federation.groups.created', { name: g.name }), 'success')
            setCreating(false)
            reload()
          }}
        />
      )}
      {deleting && (
        <DeleteDialog
          group={deleting}
          onClose={() => setDeleting(null)}
          onDone={() => {
            addToast?.(t('federation.groups.deleted', { name: deleting.name }), 'success')
            setDeleting(null)
            reload()
          }}
        />
      )}
    </div>
  )
}

function GroupsSection({ groups, instances, onNew, onDelete, addToast, reload }) {
  const { t } = useTranslation('p2p')
  return (
    <section className="p2p-section" aria-labelledby="federation-groups-title" data-testid="federation-groups">
      <div className="p2p-section__head">
        <h2 className="p2p-h2" id="federation-groups-title">{t('federation.groups.title')} <span className="p2p-count dk-mono">{groups.length}</span></h2>
        <p className="p2p-section__note">{t('federation.groups.note')}</p>
      </div>
      {groups.length === 0 && (
        <EmptyState
          variant="first-run" icon="shuffle" level={3} testId="federation-groups-empty"
          title={t('federation.groups.empty.title')} body={t('federation.groups.empty.body')}
          facts={[
            { text: t('federation.groups.empty.factInstances', { count: Math.max(instances.length - 1, 0) }), ok: instances.length > 1 },
            { text: t('federation.groups.empty.factFailover'), ok: true },
          ]}
          actions={<Button variant="primary" icon="plus" onClick={onNew} data-testid="federation-new">{t('federation.groups.new')}</Button>}
        />
      )}
      {groups.map(g => (
        <GroupCard key={g.name} group={g} onDelete={() => onDelete(g)} addToast={addToast} reload={reload} />
      ))}
    </section>
  )
}

// One group: its routing, which applies at once, and its members with the
// health and the load of each.
function GroupCard({ group, onDelete, addToast, reload }) {
  const { t } = useTranslation('p2p')
  const labelId = useId()
  const [busy, setBusy] = useState('')
  const serving = group.members.filter(m => !m.drained).length

  const run = async (key, fn, done) => {
    setBusy(key)
    try {
      await fn()
      if (done) addToast?.(done, 'success')
    } catch (err) {
      addToast?.(t('federation.groups.changeFailed', { message: err?.message || '' }), 'error')
    } finally {
      setBusy('')
      reload()
    }
  }
  const setPolicy = (policy) => {
    if (policy === group.policy) return
    run('policy', () => federationApi.update(group.name, { policy }))
  }
  const drain = (m, on) => run(`drain:${m.model}`, () => federationApi.drain(group.name, m.model, on),
    t(on ? 'federation.members.drained' : 'federation.members.undrained', { model: m.model }))

  return (
    <article className="dk-card dk-card--tight fed-group" aria-labelledby={`${labelId}-name`} data-testid={`federation-group-${group.name}`}>
      <div className="fed-group__head">
        <div className="fed-group__title">
          <h3 className="p2p-cell-title dk-mono" id={`${labelId}-name`}>{group.name}</h3>
          <StatusChip tone={group.state === 'degraded' ? 'error' : 'ok'} dot testId="federation-group-state">
            {t(group.state === 'degraded' ? 'federation.groups.degraded' : 'federation.groups.serving', { count: serving })}
          </StatusChip>
        </div>
        <Button size="sm" variant="danger-ghost" onClick={onDelete} data-testid="federation-group-delete">{t('federation.groups.delete')}</Button>
      </div>
      <div className="fed-group__routing">
        <span className="dk-label" id={`${labelId}-routing`}>{t('federation.routing.label')}</span>
        <Segmented
          mode="radio" labelledBy={`${labelId}-routing`} value={group.policy} onChange={setPolicy}
          testId="federation-policy" itemTestId="federation-policy-"
          options={POLICIES.map(p => ({ id: p, label: t(`federation.routing.${p}.title`), disabled: busy === 'policy' }))}
        />
        <p className="p2p-hint">{t(`federation.routing.${group.policy}.body`, { defaultValue: '' })}</p>
      </div>
      <div className="dk-table-wrap dk-table-wrap--flat">
        <table className="dk-table p2p-table">
          <caption className="dk-sr-only">{t('federation.members.caption', { name: group.name })}</caption>
          <thead>
            <tr>
              <th scope="col">{t('federation.members.col.instance')}</th>
              <th scope="col" className="dk-hide-phone">{t('federation.members.col.model')}</th>
              <th scope="col">{t('federation.members.col.status')}</th>
              <th scope="col" className="fed-num dk-hide-phone">{t('federation.members.col.inFlight')}</th>
              <th scope="col"><span className="dk-sr-only">{t('federation.members.col.actions')}</span></th>
            </tr>
          </thead>
          <tbody>
            {group.members.map(m => (
              <tr key={m.model} data-testid={`federation-member-${m.model}`} data-drained={m.drained || undefined}>
                <td>
                  <span className="p2p-cell-title">{memberInstanceName(m, t)}</span>
                  <span className="p2p-cell-sub dk-mono">{m.model}</span>
                </td>
                <td className="dk-hide-phone dk-mono">{m.upstream_id || m.model}</td>
                <td>
                  <span className="p2p-path">
                    <StatusChip tone={memberTone(m)} dot title={m.last_error || undefined}>
                      {m.drained ? t('federation.members.state.drained') : t(`federation.members.state.${m.state || 'unknown'}`, { defaultValue: m.state })}
                    </StatusChip>
                    {m.constrained && <Tag title={t('federation.members.constrainedHint')}>{t('federation.members.constrained')}</Tag>}
                  </span>
                </td>
                <td className="fed-num dk-mono dk-hide-phone">{m.in_flight}</td>
                <td className="p2p-cell-actions">
                  <Button
                    size="sm" variant="ghost" busy={busy === `drain:${m.model}`}
                    disabled={!m.drained && serving <= 1}
                    title={!m.drained && serving <= 1 ? t('federation.members.lastServing') : undefined}
                    onClick={() => drain(m, !m.drained)} data-testid={`federation-drain-${m.model}`}
                  >
                    {t(m.drained ? 'federation.members.undrain' : 'federation.members.drain')}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </article>
  )
}

function memberInstanceName(m, t) {
  if (m.instance === 'local') return t('federation.instances.local')
  return m.instance_name || t('federation.instances.otherProxy')
}

// The instances: this server and each LocalAI remote, with how this server
// reaches it and its share of the requests now.
function InstancesSection({ instances }) {
  const { t } = useTranslation('p2p')
  return (
    <section className="p2p-section" aria-labelledby="federation-instances-title" data-testid="federation-instances">
      <div className="p2p-section__head">
        <h2 className="p2p-h2" id="federation-instances-title">{t('federation.instances.title')} <span className="p2p-count dk-mono">{instances.length}</span></h2>
        <Button size="sm" icon="plus" to={ADD_INSTANCE_PATH} data-testid="federation-add-instance">{t('federation.instances.add')}</Button>
      </div>
      <p className="p2p-section__note">{t('federation.instances.note')}</p>
      <div className="dk-table-wrap">
        <table className="dk-table p2p-table">
          <caption className="dk-sr-only">{t('federation.instances.title')}</caption>
          <thead>
            <tr>
              <th scope="col">{t('federation.instances.col.instance')}</th>
              <th scope="col" className="dk-hide-phone">{t('federation.instances.col.peer')}</th>
              <th scope="col">{t('federation.instances.col.path')}</th>
              <th scope="col" className="fed-num">{t('federation.instances.col.inFlight')}</th>
              <th scope="col" className="fed-num dk-hide-phone">{t('federation.instances.col.models')}</th>
            </tr>
          </thead>
          <tbody>
            {instances.map(inst => {
              const path = instancePath(inst)
              return (
                <tr key={inst.id} data-testid={`federation-instance-${inst.id}`}>
                  <td>
                    <span className="p2p-cell-title">{inst.local ? t('federation.instances.local') : inst.name}</span>
                    {!inst.local && <span className="p2p-cell-sub">{t(`federation.instances.status.${inst.status || 'never'}`, { defaultValue: inst.status })}</span>}
                  </td>
                  <td className="dk-hide-phone dk-mono" title={inst.p2p?.peer_id}>{inst.local ? '-' : (inst.p2p ? shortPeer(inst.p2p.peer_id) : t('federation.instances.http'))}</td>
                  <td>
                    <span className="p2p-path">
                      <StatusChip tone={path.tone} dot>{t(`federation.path.${path.key}`)}</StatusChip>
                      {path.limited && <Tag title={t('federation.path.limitedHint')}>{t('federation.path.limited')}</Tag>}
                    </span>
                  </td>
                  <td className="fed-num dk-mono">{inst.in_flight}</td>
                  <td className="fed-num dk-mono dk-hide-phone">{inst.models.length}</td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
      <p className="p2p-section__note">{t('federation.instances.inFlightNote')}</p>
    </section>
  )
}

// A new group: the model name clients use, the routing, and the members,
// grouped by the model they serve so the same model on two instances sits
// together.
function NewGroupSheet({ instances, onClose, onSaved }) {
  const { t } = useTranslation('p2p')
  const id = useId()
  const [name, setName] = useState('')
  const [policy, setPolicy] = useState('least_busy')
  const [members, setMembers] = useState([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const candidates = useMemo(() => candidateGroups(instances), [instances])

  const toggle = (c) => {
    setMembers(list => (list.includes(c.model) ? list.filter(x => x !== c.model) : [...list, c.model]))
    if (!name) setName(c.suggested)
  }
  const save = async () => {
    setBusy(true); setError('')
    try {
      const g = await federationApi.create({ name: name.trim(), policy, members })
      onSaved(g)
    } catch (err) {
      setError(err?.message || '')
      setBusy(false)
    }
  }
  return (
    <SideSheet
      title={t('federation.sheet.title')} description={t('federation.sheet.description')}
      onClose={onClose} closeLabel={t('share.close')} labelId={`${id}-title`} testId="federation-sheet"
      footer={(
        <>
          <Button onClick={onClose} disabled={busy}>{t('share.cancel')}</Button>
          <Button variant="primary" busy={busy} disabled={!name.trim() || members.length < 2} onClick={save} data-testid="federation-save">{t('federation.sheet.save')}</Button>
        </>
      )}
    >
      <div className="dk-field">
        <label className="dk-label" htmlFor={`${id}-name`}>{t('federation.sheet.name')}</label>
        <input
          id={`${id}-name`} className="dk-input dk-mono" autoComplete="off" maxLength={120} value={name}
          onChange={e => setName(e.target.value)} aria-describedby={`${id}-name-hint`} data-testid="federation-name"
        />
        <p className="dk-hint" id={`${id}-name-hint`}>{t('federation.sheet.nameHint')}</p>
      </div>
      <div className="dk-field">
        <span className="dk-label" id={`${id}-routing`}>{t('federation.routing.label')}</span>
        <Segmented
          mode="radio" labelledBy={`${id}-routing`} value={policy} onChange={setPolicy} testId="federation-sheet-policy" itemTestId="federation-sheet-policy-"
          options={POLICIES.map(p => ({ id: p, label: t(`federation.routing.${p}.title`) }))}
        />
        <p className="dk-hint">{t(`federation.routing.${policy}.body`)}</p>
      </div>
      <fieldset className="fed-pick">
        <legend className="dk-label">{t('federation.sheet.members', { count: members.length })}</legend>
        <p className="dk-hint">{t('federation.sheet.membersHint')}</p>
        {candidates.length === 0 && <Callout tone="info">{t('federation.sheet.noModels')}</Callout>}
        {candidates.map(c => (
          <div key={c.upstream} className="fed-pick__group" role="group" aria-label={c.upstream}>
            <span className="fed-pick__model dk-mono">{c.upstream}</span>
            {c.options.map(o => (
              <label key={o.model} className="fed-pick__option">
                <input type="checkbox" className="dk-check" checked={members.includes(o.model)} onChange={() => toggle(o)} data-testid={`federation-pick-${o.model}`} />
                <span>{o.local ? t('federation.instances.local') : o.instanceName}</span>
                {o.model !== c.upstream && <span className="p2p-cell-sub dk-mono">{o.model}</span>}
              </label>
            ))}
          </div>
        ))}
      </fieldset>
      {error && <Callout tone="error" role="alert" testId="federation-error">{error}</Callout>}
    </SideSheet>
  )
}

function DeleteDialog({ group, onClose, onDone }) {
  const { t } = useTranslation('p2p')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const remove = async () => {
    setBusy(true); setError('')
    try {
      await federationApi.remove(group.name)
      onDone()
    } catch (err) {
      setError(err?.message || '')
      setBusy(false)
    }
  }
  return (
    <Dialog
      role="alertdialog" title={t('federation.groups.deleteTitle', { name: group.name })} onClose={onClose} pending={busy}
      closeLabel={t('share.close')} labelId="federation-delete-title" testId="federation-delete-dialog"
      foot={(
        <>
          <Button onClick={onClose} disabled={busy} data-autofocus>{t('share.cancel')}</Button>
          <Button variant="danger" busy={busy} onClick={remove} data-testid="federation-delete-confirm">{t('federation.groups.deleteConfirm')}</Button>
        </>
      )}
    >
      <p className="p2p-revoke__text">{t('federation.groups.deleteBody')}</p>
      {error && <Callout tone="error" role="alert">{error}</Callout>}
    </Dialog>
  )
}
