/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import Icon from '../Icon'
import LoadingSpinner from '../LoadingSpinner'
import { Button, Callout, Segmented, Slider, Switch } from '../ui'
import { FileFooter, ProblemDetails } from './WellKnownCard'
import { useWellKnown } from '../../hooks/useWellKnown'
import { useModels } from '../../hooks/useModels'
import { readAloudState, speak, stopReadAloud, subscribeReadAloud, ttsModel } from '../../utils/readAloud'
import { preview } from '../../utils/soundPlayer'
import { READ_ALOUD, SOUND_EVENTS, SOUND_NAMES, SPEED, soundOf, voiceOf } from '../../utils/sound'
import { parseSource, sourceText, wellKnownProblem, WELL_KNOWN_FILES } from '../../utils/wellKnown'
import './wellKnown.css'
import './soundTab.css'

const VOLUME_RANGE = { min: 0, max: 100, step: 5, integer: true }
const SPEED_RANGE = { min: SPEED.min, max: SPEED.max, step: SPEED.step, integer: false }
const QUIET_DEFAULT = ['22:00', '07:00']
const EVENT_ICON = { replyDone: 'check-circle', approval: 'shield', taskDone: 'clock', error: 'alert-circle' }

const isObject = (v) => v !== null && typeof v === 'object' && !Array.isArray(v)

function timeLabel(d, locale) {
  try { return d.toLocaleTimeString(locale, { hour: 'numeric', minute: '2-digit' }) } catch { return '' }
}

// SoundTab is Account > Sound: the sound and voice keys of ui.json. Every
// change is saved at once, like a switch, and only those two keys are sent, so
// the rest of the file stays. Keys of sound or voice that this page does not
// know are kept: a change is merged into the object the file has now.
export default function SoundTab() {
  const { t, i18n } = useTranslation('workspace')
  const wk = useWellKnown('ui')
  const defaultsWk = useWellKnown('defaults')
  const { models } = useModels()
  const { doc } = wk
  const sound = useMemo(() => soundOf(doc?.data), [doc])
  const voice = useMemo(() => voiceOf(doc?.data), [doc])
  const [busy, setBusy] = useState(false)
  const [problem, setProblem] = useState(null)
  const [savedAt, setSavedAt] = useState(null)
  const [sourceOpen, setSourceOpen] = useState(false)
  const [source, setSource] = useState('')
  // The number boxes hold their text while the person drags or types; the
  // file is written once it settles.
  const [draft, setDraft] = useState({})
  const timers = useRef({})
  const speech = useSyncExternalStore(subscribeReadAloud, readAloudState)

  const run = useCallback(async (build) => {
    setBusy(true)
    setProblem(null)
    try {
      let version = doc?.version ?? 0
      let current = doc?.data
      for (let i = 0; ; i++) {
        try {
          await wk.save(build(current || {}), { version })
          break
        } catch (e) {
          // Another writer changed the file: build the change again on theirs.
          if (!e.conflict || i >= 3) throw e
          current = e.current.data
          version = e.current.version ?? 0
        }
      }
      setSavedAt(new Date())
      return true
    } catch (e) {
      setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t))
      return false
    } finally { setBusy(false) }
  }, [wk, doc, t])

  // patch changes keys of one object (sound or voice); a key set to undefined
  // is taken out. The object is merged into what the file holds now.
  const patch = useCallback((key, changes) => run((data) => {
    const next = { ...(isObject(data[key]) ? data[key] : {}) }
    for (const [k, v] of Object.entries(changes)) {
      if (v === undefined) delete next[k]
      else next[k] = v
    }
    return { [key]: next }
  }), [run])
  const patchEvent = (event, name) => run((data) => {
    const s = isObject(data.sound) ? data.sound : {}
    return { sound: { ...s, events: { ...(isObject(s.events) ? s.events : {}), [event]: name } } }
  })

  useEffect(() => () => { Object.values(timers.current).forEach(clearTimeout) }, [])
  // A number box is written 400 ms after the last change.
  const settle = (name, text, write) => {
    setDraft(d => ({ ...d, [name]: text }))
    clearTimeout(timers.current[name])
    timers.current[name] = setTimeout(async () => {
      await write(text)
      setDraft(d => { const n = { ...d }; delete n[name]; return n })
    }, 400)
  }

  const volumeText = draft.volume !== undefined ? draft.volume : (isObject(doc?.data?.sound) && typeof doc.data.sound.volume === 'number' ? String(Math.round(doc.data.sound.volume * 100)) : '')
  const speedText = draft.speed !== undefined ? draft.speed : (isObject(doc?.data?.voice) && typeof doc.data.voice.speed === 'number' ? String(doc.data.voice.speed) : '')
  const setVolume = (text) => settle('volume', text, (v) => {
    const n = Number(v)
    return patch('sound', { volume: v === '' || !Number.isFinite(n) ? undefined : Math.min(1, Math.max(0, Math.round(n) / 100)) })
  })
  const setSpeed = (text) => settle('speed', text, (v) => {
    const n = Number(v)
    return patch('voice', { speed: v === '' || !Number.isFinite(n) ? undefined : Math.min(SPEED.max, Math.max(SPEED.min, n)) })
  })

  const quiet = sound.quiet
  const setQuiet = (index, text) => {
    const pair = quiet ? [quiet.fromText, quiet.toText] : QUIET_DEFAULT
    const next = [...pair]
    next[index] = text
    if (!/^([01][0-9]|2[0-3]):[0-5][0-9]$/.test(text) || next[0] === next[1]) { setDraft(d => ({ ...d, [`q${index}`]: text })); return }
    setDraft(d => { const n = { ...d }; delete n[`q${index}`]; return n })
    patch('sound', { quietHours: next })
  }

  const pick = useMemo(() => ttsModel(defaultsWk.doc?.data, doc?.data, models), [defaultsWk.doc, doc, models])
  const usable = pick.state === 'model' || pick.state === 'automatic'
  const playSample = () => {
    if (speech.id === 'sample' && (speech.status === 'playing' || speech.status === 'loading')) { stopReadAloud(); return }
    speak('sample', t('sound.voice.sampleText'), { model: pick.model, voice: voice.voice, speed: voice.speed }).catch(() => {})
  }
  const sampleBusy = speech.id === 'sample' && (speech.status === 'playing' || speech.status === 'loading')

  const revert = async () => {
    setBusy(true)
    setProblem(null)
    try { await wk.revert(); setSavedAt(new Date()) } catch (e) { setProblem(wellKnownProblem(e, t)) } finally { setBusy(false) }
  }
  const toggleSource = () => {
    if (!sourceOpen) setSource(sourceText(doc?.data))
    setSourceOpen(v => !v)
  }
  const saveSource = async () => {
    const parsed = parseSource(source)
    if (parsed.error) { setProblem({ message: t(`wellKnown.source.${parsed.error}`), errors: parsed.detail ? [parsed.detail] : [] }); return }
    setBusy(true)
    setProblem(null)
    try {
      await wk.save(parsed.value, { replace: true, version: doc?.version ?? 0 })
      setSourceOpen(false)
      setSavedAt(new Date())
    } catch (e) { setProblem(e.conflict ? { message: t('wellKnown.conflict'), errors: [] } : wellKnownProblem(e, t)) } finally { setBusy(false) }
  }

  const quietFrom = draft.q0 !== undefined ? draft.q0 : (quiet ? quiet.fromText : QUIET_DEFAULT[0])
  const quietTo = draft.q1 !== undefined ? draft.q1 : (quiet ? quiet.toText : QUIET_DEFAULT[1])

  return (
    <section className="sd" data-testid="account-sound" aria-label={t('sound.title')}>
      {wk.error && !doc && (
        <div className="wk-note wk-note--error" role="alert"><Icon name="alert-circle" /><div><p>{t('wellKnown.loadFailed')}</p><ProblemDetails errors={[wk.error.message]} /></div></div>
      )}
      {doc && !doc.valid && (
        <div className="wk-note wk-note--warn" role="status" data-testid="wk-invalid-sound">
          <Icon name="warning" />
          <div><p>{doc.fallback ? t('wellKnown.invalidFallback') : t('wellKnown.invalid')}</p><ProblemDetails errors={doc.errors} /></div>
        </div>
      )}
      {problem && (
        <div className="wk-note wk-note--error" role="alert" data-testid="wk-problem-sound">
          <Icon name="alert-circle" />
          <div><p>{problem.message}</p><ProblemDetails errors={problem.errors} /></div>
        </div>
      )}

      {!doc && !wk.error ? <div className="wk-loading"><LoadingSpinner size="sm" /></div> : (
        <div className="sd__grid">
          <section className="wk-card sd-card" aria-labelledby="sd-notify-title" data-testid="sd-notify">
            <header className="sd-card__head">
              <div className="wk-card__titles">
                <h2 id="sd-notify-title"><Icon name="inbox" /> {t('sound.notify.title')}</h2>
                <p>{t('sound.notify.lede')}</p>
              </div>
              <Switch checked={sound.enabled} onChange={on => patch('sound', { enabled: on })} disabled={busy} labelledBy="sd-notify-title" testId="sd-enabled" />
            </header>
            <ul className="sd-events">
              {SOUND_EVENTS.map(ev => (
                <li key={ev} className="sd-event" data-testid={`sd-event-${ev}`}>
                  <span className="sd-event__text">
                    <label htmlFor={`sd-sel-${ev}`} className="sd-event__label">{t(`sound.events.${ev}.label`)}</label>
                    <span className="sd-event__hint">{t(`sound.events.${ev}.hint`)}</span>
                  </span>
                  <span className="dk-select-wrap sd-event__pick">
                    <select id={`sd-sel-${ev}`} className="dk-select" value={sound.events[ev]} disabled={busy} onChange={e => patchEvent(ev, e.target.value)} data-testid={`sd-select-${ev}`}>
                      {SOUND_NAMES.map(n => <option key={n} value={n}>{t(`sound.names.${n}`)}</option>)}
                    </select>
                  </span>
                  <Button
                    variant="ghost" size="sm" icon="play" disabled={sound.events[ev] === 'none'}
                    aria-label={t('sound.play', { event: t(`sound.events.${ev}.label`) })} title={t('sound.play', { event: t(`sound.events.${ev}.label`) })}
                    onClick={() => preview(sound.events[ev], sound.volume)} data-testid={`sd-play-${ev}`}
                  />
                </li>
              ))}
            </ul>
            <div className="sd-row">
              <label className="sd-row__label" htmlFor="sd-volume"><Icon name="volume" /> {t('sound.volume')}</label>
              <Slider
                id="sd-volume" range={VOLUME_RANGE} value={volumeText} onChange={setVolume} label={t('sound.volume')}
                placeholder={String(Math.round(sound.volume * 100))} resetLabel={t('sound.volumeReset')} testId="sd-volume-slider"
              />
            </div>
            <div className="sd-row sd-row--quiet">
              <span className="sd-row__label" id="sd-quiet-label"><Icon name="moon" /> {t('sound.quiet.title')}</span>
              <Switch checked={!!quiet || draft.q0 !== undefined || draft.q1 !== undefined} onChange={on => (on ? patch('sound', { quietHours: QUIET_DEFAULT }) : patch('sound', { quietHours: undefined }))} disabled={busy} labelledBy="sd-quiet-label" testId="sd-quiet" />
              <p className="sd-row__hint" data-testid="sd-quiet-hint">{quiet ? t('sound.quiet.between', { from: quiet.fromText, to: quiet.toText }) : t('sound.quiet.none')}</p>
              {(quiet || draft.q0 !== undefined || draft.q1 !== undefined) && (
                <div className="sd-quiet__times">
                  <label htmlFor="sd-quiet-from">{t('sound.quiet.from')}</label>
                  <input id="sd-quiet-from" type="time" className="dk-input" value={quietFrom} onChange={e => setQuiet(0, e.target.value)} data-testid="sd-quiet-from" />
                  <label htmlFor="sd-quiet-to">{t('sound.quiet.to')}</label>
                  <input id="sd-quiet-to" type="time" className="dk-input" value={quietTo} onChange={e => setQuiet(1, e.target.value)} data-testid="sd-quiet-to" />
                </div>
              )}
            </div>
            <p className="dk-hint sd-note"><Icon name="info" /> {t('sound.notify.rule')}</p>
          </section>

          <section className="wk-card sd-card" aria-labelledby="sd-voice-title" data-testid="sd-voice">
            <header className="sd-card__head">
              <div className="wk-card__titles">
                <h2 id="sd-voice-title"><Icon name="volume" /> {t('sound.voice.title')}</h2>
                <p>{t('sound.voice.lede')}</p>
              </div>
            </header>
            <div className="dk-field">
              <span className="dk-label">{t('sound.voice.readAloud')}</span>
              <Segmented
                label={t('sound.voice.readAloud')} value={voice.readAloud} itemTestId="sd-read-"
                options={READ_ALOUD.map(m => ({ id: m, label: t(`sound.voice.mode.${m}`), disabled: busy }))}
                onChange={m => patch('voice', { readAloud: m })}
              />
              <p className="dk-hint">{t(`sound.voice.modeHint.${voice.readAloud}`)}</p>
            </div>
            <div className="dk-field">
              <label className="dk-label" htmlFor="sd-voice-name">{t('sound.voice.voice')}</label>
              <input
                id="sd-voice-name" className="dk-input" value={draft.voiceName !== undefined ? draft.voiceName : voice.voice} maxLength={100}
                placeholder={t('sound.voice.voicePlaceholder')} aria-describedby="sd-voice-hint" spellCheck={false} autoComplete="off"
                onChange={e => settle('voiceName', e.target.value, v => patch('voice', { voice: v.trim() === '' ? undefined : v.trim() }))}
                data-testid="sd-voice-name"
              />
              <p className="dk-hint" id="sd-voice-hint">{t('sound.voice.voiceHint')}</p>
            </div>
            <div className="sd-row">
              <label className="sd-row__label" htmlFor="sd-speed">{t('sound.voice.speed')}</label>
              <Slider
                id="sd-speed" range={SPEED_RANGE} value={speedText} onChange={setSpeed} label={t('sound.voice.speed')}
                placeholder="1" resetLabel={t('sound.voice.speedReset')} testId="sd-speed-slider"
              />
            </div>
            <div className="sd-row sd-row--switch">
              <span className="sd-row__text">
                <span className="sd-row__label" id="sd-skip-label">{t('sound.voice.skipCode')}</span>
                <span className="sd-row__hint">{t('sound.voice.skipCodeHint')}</span>
              </span>
              <Switch checked={voice.skipCode} onChange={on => patch('voice', { skipCode: on })} disabled={busy} labelledBy="sd-skip-label" testId="sd-skip-code" />
            </div>
            <div className="sd-sample">
              <Button variant="primary" size="sm" icon={sampleBusy ? 'stop' : 'play'} disabled={!usable} onClick={playSample} data-testid="sd-sample">
                {sampleBusy ? t('sound.voice.stop') : t('sound.voice.sample')}
              </Button>
              {speech.id === 'sample' && speech.status === 'error' && <span className="sd-sample__err" role="status" data-testid="sd-sample-error">{t('sound.voice.sampleFailed')}</span>}
            </div>
            <div className="sd-model" data-testid="sd-model" data-state={pick.state} data-remote={pick.remote ? 'true' : undefined}>
              {usable && (
                <p className="sd-model__line" data-testid="sd-model-line">
                  <Icon name="info" />
                  <span>{t(pick.state === 'model' ? 'sound.voice.madeBy' : 'sound.voice.madeByAuto', { model: pick.model })} <Link to="/app/account?tab=defaults">{t('sound.voice.change')}</Link></span>
                </p>
              )}
              {!usable && (
                <Callout tone="warn" testId="sd-model-missing" actions={<Button size="sm" to="/app/account?tab=defaults">{t('sound.voice.change')}</Button>}>
                  {t(`sound.voice.model.${pick.state}`, { model: pick.model })}
                </Callout>
              )}
              {usable && pick.remote && (
                <Callout tone="warn" testId="sd-model-remote">{t('sound.voice.remote', { model: pick.model })}</Callout>
              )}
            </div>
          </section>
        </div>
      )}

      {sourceOpen && (
        <div className="dk-field">
          <label className="dk-label" htmlFor="sd-source">{t('wellKnown.source.label')}</label>
          <textarea id="sd-source" className="dk-input dk-textarea dk-mono wk-source" spellCheck={false} value={source} onChange={e => { setSource(e.target.value); setProblem(null) }} data-testid="wk-source-text-sound" />
          <p className="dk-hint">{t('wellKnown.source.hint')}</p>
          <div className="wk-actions">
            <Button variant="primary" size="sm" icon="save" busy={busy} onClick={saveSource} data-testid="sd-source-save">{t('wellKnown.save')}</Button>
            <Button variant="ghost" size="sm" onClick={toggleSource}>{t('wellKnown.cancel')}</Button>
          </div>
        </div>
      )}

      <FileFooter name="sound" file={WELL_KNOWN_FILES.ui} onSource={toggleSource} sourceOpen={sourceOpen} onRevert={revert} canRevert={!busy && !!doc?.exists}>
        {savedAt && <span className="wk-stored__saved" role="status" data-testid="sd-saved">{t('wellKnown.savedAt', { time: timeLabel(savedAt, i18n.language) })}</span>}
      </FileFooter>
    </section>
  )
}
