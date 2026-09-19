// SPDX-License-Identifier: MIT
import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useModels } from '../hooks/useModels'
import { CAP_CHAT } from '../utils/capabilities'
import { streamChat } from '../utils/api'
import { GroupRun, validParticipants } from '../utils/groupChat'

export default function GroupChat() {
  const { t } = useTranslation('chat')
  const { models, loading, error: modelError } = useModels(CAP_CHAT)
  const [model, setModel] = useState('')
  const [participants, setParticipants] = useState([])
  const [objective, setObjective] = useState('')
  const [message, setMessage] = useState('')
  const [transcript, setTranscript] = useState([])
  const [rounds, setRounds] = useState('1')
  const [locked, setLocked] = useState(false)
  const [running, setRunning] = useState(false)
  const [error, setError] = useState('')
  const runner = useRef(null)
  if (!runner.current) runner.current = new GroupRun(streamChat)
  const mounted = useRef(true)
  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false; runner.current.stop() }
  }, [])
  const selected = models.some(m => m.id === model) ? model : (models[0]?.id || '')
  const valid = validParticipants(participants)
  const roundsValid = Number.isInteger(Number(rounds)) && Number(rounds) >= 1 && Number(rounds) <= 10

  function addParticipant() {
    if (locked || !selected || participants.length >= 6) return
    let name = selected, suffix = 2
    while (participants.some(p => p.name.trim().toLowerCase() === name.toLowerCase())) name = `${selected} ${suffix++}`
    setParticipants([...participants, { name, model: selected }])
  }
  async function run(speaker, count = 1) {
    if (runner.current.running || !valid) return
    setLocked(true)
    setRunning(true)
    setError('')
    try {
      await runner.current.run({
        participants: participants.map(p => ({ ...p, name: p.name.trim() })),
        objective, transcript, speaker, rounds: count,
        onUpdate: entries => { if (mounted.current) setTranscript(entries) },
      })
    } catch (err) {
      if (mounted.current && err.name !== 'AbortError') setError(err.message)
    } finally {
      if (mounted.current) setRunning(false)
    }
  }
  function addMessage(e) {
    e.preventDefault()
    if (!message.trim() || runner.current.running) return
    setTranscript([...transcript, { name: t('group.moderator', 'Moderator'), content: message.trim(), status: 'complete' }])
    setMessage('')
  }
  function reset() {
    if (runner.current.running) return
    setTranscript([])
    setLocked(false)
    setError('')
    setMessage('')
  }

  return (
    <div className="group-chat stack">
      <header className="page-header">
        <h1>{t('group.title', 'Group chat')}</h1>
        <Link className="btn btn-secondary" to="/app/chat">{t('group.back', 'Back to Chat')}</Link>
      </header>
      <p className="text-note">{t('group.session', 'This text-only conversation stays in this page. Leaving or reloading clears its history. Models take turns sequentially.')}</p>
      {(error || modelError) && <div role="alert" className="callout callout--warning">{error || modelError}</div>}
      <section className="card stack" aria-label={t('group.setup', 'Conversation setup')}>
        <h2>{t('group.participants', 'Participants')}</h2>
        {loading && <p role="status">{t('group.loading', 'Loading chat models…')}</p>}
        {!loading && models.length === 0 && <p>{t('group.emptyModels', 'No installed chat models available.')}</p>}
        <div className="group-chat-controls">
          <label className="stack">{t('group.model', 'Model to add')}
            <select value={selected} onChange={e => setModel(e.target.value)} disabled={locked || loading || !selected}>
              {!selected && <option value="">{t('group.select', 'Select a model')}</option>}
              {models.map(m => <option key={m.id} value={m.id}>{m.id}</option>)}
            </select>
          </label>
          <button className="btn btn-secondary" disabled={locked || !selected || participants.length >= 6} onClick={addParticipant}>{t('group.addParticipant', 'Add participant')}</button>
        </div>
        {participants.map((p, i) => <div className="group-chat-controls" key={i}>
          <label className="stack">{t('group.name', 'Name for {{model}}', { model: p.model })}
            <input value={p.name} disabled={locked} onChange={e => setParticipants(participants.map((item, index) => index === i ? { ...item, name: e.target.value } : item))} />
          </label>
          <span className="text-meta">{p.model}</span>
          <button className="btn btn-secondary" disabled={locked} onClick={() => setParticipants(participants.filter((_, index) => index !== i))} aria-label={t('group.remove', 'Remove {{name}}', { name: p.name })}>{t('group.removeButton', 'Remove')}</button>
          <button className="btn btn-primary" disabled={!valid || running} onClick={() => run(i)}>{t('group.turn', 'Give {{name}} a turn', { name: p.name })}</button>
        </div>)}
        {!valid && <p className="text-note">{t('group.validation', 'Add 2–6 participants with unique, nonempty names.')}</p>}
        <label className="stack">{t('group.objective', 'Objective')}
          <textarea rows={3} value={objective} onChange={e => setObjective(e.target.value)} disabled={locked} />
        </label>
        {locked && <p className="text-note">{t('group.locked', 'Setup is locked. Start a new conversation to change participants or the objective.')}</p>}
      </section>
      <div className="group-chat-controls">
        <button className="btn btn-primary" disabled={!valid || running} onClick={() => run(undefined, 1)}>{t('group.oneRound', 'Run one round')}</button>
        <label className="stack">{t('group.rounds', 'Number of rounds')}
          <input type="number" min="1" max="10" step="1" value={rounds} onChange={e => setRounds(e.target.value)} disabled={running} />
        </label>
        <button className="btn btn-primary" disabled={!valid || running || !roundsValid} onClick={() => run(undefined, Number(rounds))}>{t('group.runRounds', 'Run rounds')}</button>
        {running && <button className="btn btn-danger" onClick={() => runner.current.stop()}>{t('group.stop', 'Stop')}</button>}
        <button className="btn btn-secondary" disabled={running} onClick={reset}>{t('group.new', 'New conversation')}</button>
      </div>
      <p className="text-note">{t('group.order', 'Rounds follow participant order. Each run is limited to 1–10 rounds. Stop ends the active request and cancels remaining turns.')}</p>
      <section className="stack" aria-label={t('group.transcript', 'Shared transcript')} aria-busy={running}>
        <h2>{t('group.transcript', 'Shared transcript')}</h2>
        {transcript.length === 0 && <p className="text-note">{t('group.empty', 'Add a moderator message or give a participant the first turn.')}</p>}
        {transcript.map((entry, i) => <article className="card stack" key={i}>
          <strong>{entry.name}{entry.model && <span className="text-meta"> ({entry.model})</span>}</strong>
          <div className="group-chat-content">{entry.content}</div>
          {entry.status === 'streaming' && <span role="status">{t('group.generating', 'Generating…')}</span>}
          {entry.status === 'incomplete' && <><span>{t('group.incomplete', 'Incomplete')}</span><span className="text-note">{t('group.excluded', 'Excluded from future model context.')}</span></>}
        </article>)}
      </section>
      <form className="stack" onSubmit={addMessage}>
        <label className="stack">{t('group.message', 'Moderator message')}
          <textarea rows={3} value={message} onChange={e => setMessage(e.target.value)} disabled={running} />
        </label>
        <button className="btn btn-primary" type="submit" disabled={running || !message.trim()}>{t('group.addMessage', 'Add message')}</button>
      </form>
    </div>
  )
}
