import { useCallback, useEffect, useRef, useState } from 'react'
import { useOutletContext } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { fetchUrl } from '../utils/api'
import { copyToClipboard } from '../utils/clipboard'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import Transcript, { Turn } from '../../../../talk/ui/src/components/talk/Transcript'
import { base64Of } from '../lib/portrait'
import { runTurn, stagesFor } from '../lib/turn'
import { useRecorder } from '../lib/useRecorder'

let nextId = 0
const uid = (prefix) => `${prefix}${Date.now().toString(36)}${(nextId++).toString(36)}`

// 1234 -> "1.2 s", 61000 -> "1 min 1 s".
export function formatMs(ms) {
  if (!Number.isFinite(ms)) return ''
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)} min ${s % 60} s`
}

// Turn by turn: you speak (push to talk) or type, the page runs the four
// stages one after the other, then plays the clip of the reply. Each reply is
// a new clip that starts from the portrait.
export default function TurnMode({ choice, voice, settings, portrait, chips, blocked, loading, portraitPicker, onBusy }) {
  const { t } = useTranslation('head')
  const { addToast } = useOutletContext()
  const [entries, setEntries] = useState([])
  const [run, setRun] = useState(null) // { kind, stages: { name: { state, ms } }, started, finished }
  const [error, setError] = useState(null)
  const [text, setText] = useState('')
  const [clip, setClip] = useState(null) // { url, key }
  // Redraws the clock of the running stage.
  const [, setTick] = useState(0)
  const abortRef = useRef(null)
  const urlsRef = useRef([])
  const recorder = useRecorder()

  const busy = Boolean(run && !run.finished)
  useEffect(() => { onBusy?.(busy) }, [busy, onBusy])

  useEffect(() => {
    if (!busy) return
    const id = setInterval(() => setTick((n) => n + 1), 250)
    return () => clearInterval(id)
  }, [busy])

  // The clips live in this tab only; free them when the page goes away.
  useEffect(() => () => {
    abortRef.current?.abort()
    urlsRef.current.forEach((u) => URL.revokeObjectURL(u))
  }, [])

  const ready = Boolean(portrait && choice.avatar && choice.tts && !blocked && !loading)
  const canReply = ready && Boolean(choice.chat)
  const canListen = canReply && Boolean(choice.stt) && recorder.supported

  const setStage = (name, state, info) => setRun((r) => r && ({ ...r, stages: { ...r.stages, [name]: { state, ms: info?.ms, since: Date.now() } } }))
  const upsert = (id, patch) => setEntries((list) => {
    const i = list.findIndex((e) => e.id === id)
    if (i === -1) return [...list, { id, ...patch }]
    const next = [...list]
    next[i] = { ...next[i], ...patch }
    return next
  })

  const submit = useCallback(async (kind, payload) => {
    if (abortRef.current || !portrait) return
    const controller = new AbortController()
    abortRef.current = controller
    setError(null)
    const order = stagesFor(kind)
    setRun({ kind, stages: Object.fromEntries(order.map((s) => [s, { state: 'waiting' }])), started: Date.now(), finished: false })
    const userId = uid('u')
    const replyId = uid('r')
    const history = entries.filter((e) => !e.cancelled)
    if (kind === 'text') upsert(userId, { role: 'user', text: payload })
    try {
      const result = await runTurn({
        request: (path, init) => fetchUrl(path, init),
        input: kind === 'audio' ? { kind, blob: payload } : { kind, text: payload },
        models: choice,
        portrait: base64Of(portrait.dataUrl),
        history,
        instructions: settings.instructions,
        voice,
        nfe: settings.nfe,
        seed: settings.seed,
        signal: controller.signal,
        onStage: setStage,
        onUser: (said) => { if (kind === 'audio') upsert(userId, { role: 'user', text: said }) },
        onReply: (reply) => upsert(replyId, { role: kind === 'say' ? 'said' : 'assistant', text: reply }),
      })
      const url = URL.createObjectURL(result.video)
      urlsRef.current.push(url)
      const total = Object.values(result.timings).reduce((a, b) => a + b, 0)
      upsert(replyId, { clipUrl: url, timings: result.timings, total })
      setClip({ url, key: replyId })
    } catch (err) {
      if (err?.name === 'AbortError') {
        upsert(replyId, { role: 'note', text: '', cancelled: true })
      } else {
        setError({ stage: err?.stage, code: err?.code, message: err?.message || String(err) })
        setEntries((list) => list.filter((e) => !(e.id === replyId && !e.text)))
      }
    } finally {
      abortRef.current = null
      setRun((r) => r && ({ ...r, finished: true }))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [portrait, choice, settings, voice, entries])

  const cancel = () => abortRef.current?.abort()

  const send = (kind) => {
    const value = text.trim()
    if (!value || busy) return
    setText('')
    submit(kind, value)
  }

  // Push to talk: hold the button (or Space or Enter on it) while you speak.
  const holdStart = async () => {
    if (!canListen || busy || recorder.state === 'recording' || recorder.state === 'starting') return
    setError(null)
    await recorder.start()
  }
  const holdEnd = async () => {
    if (recorder.state !== 'recording' && recorder.state !== 'starting') return
    const blob = await recorder.stop()
    if (blob) submit('audio', blob)
    else setError({ code: 'tooShort' })
  }

  const copy = async () => {
    const lines = entries.filter((e) => e.text).map((e) => `${t(`transcript.${e.role}`)}: ${e.text}`)
    const ok = await copyToClipboard(lines.join('\n'))
    addToast(ok ? t('notices.copied') : t('notices.copyFailed'), ok ? 'success' : 'error')
  }

  const running = run && Object.entries(run.stages).find(([, s]) => s.state === 'running')
  const micBlocked = recorder.state === 'blocked' || recorder.state === 'missing' || recorder.state === 'error'
  const recording = recorder.state === 'recording' || recorder.state === 'starting'

  return (
    <div className="talk-grid">
      <section className="talk-stage head-stage" aria-label={t('stage')}>
        {chips}
        {blocked || (
          <>
            <FaceView portrait={portrait} clip={clip} onEnded={() => setClip(null)} />
            {portraitPicker}

            <div className="talk-status" role="status" aria-live="polite" data-testid="head-status" data-state={busy ? 'busy' : recording ? 'recording' : error ? 'error' : 'idle'}>
              <h2>
                {busy && <Icon name="spinner" spin />}
                <span>{busy ? t(`stages.${running?.[0] || 'video'}.busy`) : recording ? t('status.recording') : error ? t('status.failed') : !portrait ? t('status.noPortrait') : t('status.ready')}</span>
              </h2>
              <p>{recording ? t('status.recordingBody') : error ? errorLine(t, error) : busy && running?.[0] === 'video' ? t('status.videoSlow') : !portrait ? t('status.noPortraitBody') : t('status.readyBody')}</p>
            </div>

            {run && (
              <ol className="head-stages" data-testid="head-stages" aria-label={t('stages.title')}>
                {Object.entries(run.stages).map(([name, s]) => (
                  <li key={name} data-state={s.state} data-stage={name}>
                    <Icon name={s.state === 'done' ? 'check-circle' : s.state === 'running' ? 'spinner' : s.state === 'failed' ? 'alert-circle' : s.state === 'cancelled' ? 'stop' : 'circle'} spin={s.state === 'running'} />
                    <span className="head-stages__name">{t(`stages.${name}.label`)}</span>
                    <span className="head-stages__time">
                      {s.state === 'running' && s.since ? formatMs(Date.now() - s.since) : s.ms !== undefined ? formatMs(s.ms) : ''}
                    </span>
                  </li>
                ))}
              </ol>
            )}

            {micBlocked && (
              <div className="talk-alert" role="alert" data-testid="head-mic-blocked">
                <Icon name="alert-circle" />
                <div>
                  <h3>{t(`mic.${recorder.state}.title`)}</h3>
                  <p>{t(`mic.${recorder.state}.body`)}</p>
                </div>
              </div>
            )}

            <form className="head-composer" onSubmit={(e) => { e.preventDefault(); send(canReply ? 'text' : 'say') }}>
              <label htmlFor="head-text" className="head-composer__label">{t('composer.label')}</label>
              <textarea
                id="head-text"
                className="talk-ta head-composer__text"
                rows={2}
                value={text}
                disabled={!ready}
                placeholder={choice.chat ? t('composer.placeholder') : t('composer.placeholderSay')}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => { if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) { e.preventDefault(); send(canReply ? 'text' : 'say') } }}
                data-testid="head-text"
              />
              <div className="talk-ctl">
                {busy ? (
                  <button type="button" className="talk-btn talk-btn--end" onClick={cancel} data-testid="head-cancel">
                    <Icon name="stop" /> {t('composer.cancel')}
                  </button>
                ) : (
                  <>
                    <button
                      type="button"
                      className="talk-start head-talk"
                      disabled={!canListen}
                      data-recording={recorder.state === 'recording'}
                      aria-pressed={recorder.state === 'recording'}
                      title={canListen ? t('composer.holdHelp') : !choice.stt ? t('composer.noStt') : undefined}
                      onPointerDown={(e) => { e.preventDefault(); holdStart() }}
                      onPointerUp={holdEnd}
                      onPointerLeave={holdEnd}
                      onPointerCancel={holdEnd}
                      onKeyDown={(e) => { if ((e.key === ' ' || e.key === 'Enter') && !e.repeat) { e.preventDefault(); holdStart() } }}
                      onKeyUp={(e) => { if (e.key === ' ' || e.key === 'Enter') { e.preventDefault(); holdEnd() } }}
                      data-testid="head-hold"
                    >
                      <span>{recorder.state === 'recording' ? t('composer.recording', { time: formatMs(recorder.elapsed) }) : t('composer.hold')}</span>
                      <span className="talk-start__cell"><Icon name="mic" /></span>
                    </button>
                    <button type="submit" className="talk-btn" disabled={!canReply || !text.trim()} data-testid="head-send">
                      <Icon name="sparkles" /> {t('composer.send')}
                    </button>
                    <button type="button" className="talk-btn" disabled={!ready || !text.trim()} onClick={() => send('say')} data-testid="head-say">
                      <Icon name="volume" /> {t('composer.say')}
                    </button>
                  </>
                )}
              </div>
            </form>
            <p className="talk-note">{t('composer.note')}</p>
          </>
        )}
      </section>

      <Transcript
        title={t('transcript.title')}
        copyLabel={t('transcript.copy')}
        onCopy={copy}
        canCopy={entries.some((e) => e.text)}
        empty={entries.length === 0 ? t('transcript.empty') : null}
        testId="head-transcript"
        copyTestId="head-copy"
        follow={entries}
      >
        {entries.map((entry) => entry.cancelled ? (
          <p key={entry.id} className="talk-turn talk-turn--note" data-role="note"><span className="talk-tag">{t('transcript.cancelled')}</span></p>
        ) : (
          <Turn key={entry.id} role={entry.role} label={t(`transcript.${entry.role}`)} text={entry.text}>
            {entry.clipUrl && (
              <div className="head-turn__clip">
                <button type="button" className="talk-btn talk-btn--ghost" onClick={() => setClip({ url: entry.clipUrl, key: `${entry.id}-${Date.now()}` })} data-testid="head-replay">
                  <Icon name="play" /> {t('transcript.replay')}
                </button>
                <a className="talk-btn talk-btn--ghost" href={entry.clipUrl} download={`talking-head-${entry.id}.mp4`}>
                  <Icon name="download" /> {t('transcript.download')}
                </a>
                <span className="head-turn__times">
                  {Object.entries(entry.timings || {}).map(([k, ms]) => `${t(`stages.${k}.label`)} ${formatMs(ms)}`).join(' · ')}
                </span>
              </div>
            )}
          </Turn>
        ))}
      </Transcript>
    </div>
  )
}

function errorLine(t, error) {
  if (error.code) return t(`errors.${error.code}`)
  const stage = error.stage ? t(`stages.${error.stage}.label`) : ''
  return stage ? t('errors.stage', { stage, message: error.message }) : error.message
}

// The face: the portrait while nothing plays, the clip of a reply while it
// plays. A browser that refuses to start sound on its own gets a Play button.
function FaceView({ portrait, clip, onEnded }) {
  const { t } = useTranslation('head')
  const videoRef = useRef(null)
  const [needsClick, setNeedsClick] = useState(false)
  // The clip is H.264 and AAC in MP4. A browser built without those codecs
  // (some Chromium builds) cannot play it, so the page offers the file.
  const [unplayable, setUnplayable] = useState(false)

  useEffect(() => {
    const v = videoRef.current
    if (!clip || !v) return
    setNeedsClick(false)
    setUnplayable(false)
    v.currentTime = 0
    v.play().catch(() => setNeedsClick(true))
  }, [clip?.key])

  return (
    <div className="head-face" data-playing={Boolean(clip)} data-testid="head-face">
      {portrait ? <img className="head-face__still" src={portrait.dataUrl} alt={t('face.portraitAlt')} /> : (
        <div className="head-face__empty">
          <Icon name="image" />
          <p>{t('face.empty')}</p>
        </div>
      )}
      {clip && (
        <video
          key={clip.url}
          ref={videoRef}
          className="head-face__clip"
          src={clip.url}
          playsInline
          onEnded={onEnded}
          onError={() => setUnplayable(true)}
          data-testid="head-clip"
        />
      )}
      {clip && unplayable && (
        <div className="head-face__note" role="alert" data-testid="head-unplayable">
          <p>{t('face.unplayable')}</p>
          <a className="talk-btn" href={clip.url} download="talking-head.mp4"><Icon name="download" /> {t('face.download')}</a>
        </div>
      )}
      {clip && needsClick && !unplayable && (
        <button type="button" className="talk-btn head-face__play" onClick={() => { setNeedsClick(false); videoRef.current?.play().catch(() => setNeedsClick(true)) }}>
          <Icon name="play" /> {t('face.play')}
        </button>
      )}
    </div>
  )
}
