import { useCallback, useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { faceApi, voiceApi } from '../../utils/api'
import { FACE_CUTOFF, VOICE_CUTOFF, rankMatches, strength } from '../../utils/identity'
import { UNDO_WINDOW_MS } from '../../hooks/useDelayedAction'
// eslint-disable-next-line no-unused-vars
import BoundingBoxCanvas from '../biometrics/BoundingBoxCanvas'
// eslint-disable-next-line no-unused-vars
import HomeUndoToast from '../home/HomeUndoToast'
// eslint-disable-next-line no-unused-vars
import LoadingSpinner from '../LoadingSpinner'
import Icon from '../Icon'
// eslint-disable-next-line no-unused-vars
import ClipInput from './ClipInput'
// eslint-disable-next-line no-unused-vars
import DistanceScale from './DistanceScale'
// eslint-disable-next-line no-unused-vars
import EnrolSheet from './EnrolSheet'
// eslint-disable-next-line no-unused-vars
import ErrorNote from './ErrorNote'
// eslint-disable-next-line no-unused-vars
import KnownList from './KnownList'
import { useRegistry } from './useRegistry'
// eslint-disable-next-line no-unused-vars
import Verdict from './Verdict'

const KINDS = {
  voice: {
    api: voiceApi, mode: 'audio', cutoff: VOICE_CUTOFF, print: 'voiceprint', key: 'localai_voice_enrollments',
    probe: 'audio', pair: ['audio1', 'audio2'], minTopK: 25,
  },
  face: {
    api: faceApi, mode: 'image', cutoff: FACE_CUTOFF, print: 'faceprint', key: 'localai_face_enrollments',
    probe: 'img', pair: ['img1', 'img2'], minTopK: 25,
  },
}

// eslint-disable-next-line no-unused-vars
function CutoffControl({ value, onChange, modelDefault, kind }) {
  const { t } = useTranslation('biometrics')
  const same = Math.abs(value - modelDefault) < 0.0005
  const fill = { '--dk-fill': `${((value - 0.05) / 0.95) * 100}%` }
  return (
    <div className="idn-cutoff">
      <div className="idn-cutoff__head">
        <label htmlFor={`${kind}-cutoff`}><strong>{t('cutoff.label')}</strong> <span className="dk-mono" data-testid="cutoff-value">{value.toFixed(2)}</span></label>
        {same
          ? <span className="dk-badge">{t('cutoff.default')}</span>
          : <button type="button" className="dk-btn dk-btn--ghost dk-btn--sm" onClick={() => onChange(modelDefault)}>{t('cutoff.reset', { value: modelDefault.toFixed(2) })}</button>}
      </div>
      <div className="idn-cutoff__row">
        <span className="dk-hint">{t('cutoff.stricter')}</span>
        <input id={`${kind}-cutoff`} className="dk-range" type="range" min="0.05" max="1" step="0.01" value={value} style={fill}
          onChange={(e) => onChange(parseFloat(e.target.value))} />
        <span className="dk-hint">{t('cutoff.looser')}</span>
      </div>
      <p className="dk-hint">{t('cutoff.explain')}</p>
    </div>
  )
}

// eslint-disable-next-line no-unused-vars
function HowToRead({ kind, print }) {
  const { t } = useTranslation('biometrics')
  return (
    <p className="idn-how"><strong>{t('how.title')}</strong> {t('how.body', { print })}</p>
  )
}

// eslint-disable-next-line no-unused-vars
function WhyWrong({ kind, notes }) {
  const { t } = useTranslation('biometrics')
  const list = [...notes, ...t(`${kind}.why`, { returnObjects: true })]
  return (
    <details className="idn-disclosure">
      <summary><Icon name="chevron-right" /> {t('why.title')}</summary>
      <ul className="idn-disclosure__list" data-testid="why-list">{list.map(n => <li key={n}>{n}</li>)}</ul>
    </details>
  )
}

// eslint-disable-next-line no-unused-vars
function Stored({ kind, print }) {
  const { t } = useTranslation('biometrics')
  const points = t(`${kind}.stored`, { returnObjects: true, print })
  return (
    <details className="idn-disclosure idn-disclosure--page" data-testid="stored-note">
      <summary><Icon name="chevron-right" /> {t('stored.title')}</summary>
      <dl className="idn-stored">
        {points.map(p => <div key={p.where}><dt>{p.where}</dt><dd>{p.what}</dd></div>)}
      </dl>
    </details>
  )
}

// The whole identity tool, for voices or for faces: who is this, same person,
// the people this browser knows, and what is stored. The two kinds differ in
// the sample (audio or photo), the calls and the default cut-off; everything
// else is shared so the pages read as one family.
export default function Workbench({ kind, model, addToast, canSpeech = false, more = null, needed = null, onCount }) {
  const cfg = KINDS[kind]
  const isFace = kind === 'face'
  const { t } = useTranslation('biometrics')
  const noun = t(`${kind}.print`)
  const [tool, setTool] = useState('identify')
  const [probe, setProbe] = useState(null)
  const [probeFacts, setProbeFacts] = useState(null)
  const [clipA, setClipA] = useState(null)
  const [clipB, setClipB] = useState(null)
  const [antiSpoof, setAntiSpoof] = useState(false)
  const [find, setFind] = useState({ status: 'idle' })
  const [pair, setPair] = useState({ status: 'idle' })
  const [findCut, setFindCut] = useState(cfg.cutoff)
  const [pairCut, setPairCut] = useState(null)
  const [sheet, setSheet] = useState(null)

  const onForgot = useCallback((entry, outcome) => {
    if (outcome === 'gone') addToast(t('toast.removed', { name: entry.name }), 'info')
    else if (outcome === 'already-gone') addToast(t('toast.alreadyGone', { name: entry.name }), 'warning')
    else addToast(outcome.message, 'error')
  }, [addToast, t])
  const forgetCall = useCallback((body) => cfg.api.forget(body), [cfg])
  const reg = useRegistry({ storageKey: cfg.key, forget: forgetCall, onForgot })

  const visibleEntries = reg.entries
  const known = visibleEntries.length
  useEffect(() => { onCount?.(known) }, [known, onCount])

  const identify = async () => {
    if (!model || !probe) return
    setFind({ status: 'working' })
    const topK = Math.max(cfg.minTopK, known * 2)
    try {
      const data = await cfg.api.identify({ model, [cfg.probe]: probe.dataUrl, top_k: topK, threshold: findCut })
      const matches = data?.matches || []
      reg.noteSearch(matches, topK)
      setFind({ status: 'done', matches, cutoffSent: findCut, searched: known })
    } catch (err) {
      setFind({ status: 'failed', error: err })
    }
  }

  const compare = async () => {
    if (!model || !clipA || !clipB) return
    setPair({ status: 'working' })
    setPairCut(null)
    try {
      const body = { model, [cfg.pair[0]]: clipA.dataUrl, [cfg.pair[1]]: clipB.dataUrl }
      if (isFace) body.anti_spoofing = antiSpoof
      const data = await cfg.api.verify(body)
      setPair({ status: 'done', data })
    } catch (err) {
      setPair({ status: 'failed', error: err })
    }
  }

  const ranked = useMemo(() => (find.status === 'done' ? rankMatches(find.matches, findCut) : []), [find, findCut])
  const best = ranked[0]
  const nameOf = (m) => m.name || m.id

  const onEnrolled = (entry, extra) => {
    reg.add(entry)
    setSheet(null)
    addToast(t(extra.asSpeech && !extra.speechError ? 'toast.enrolledBoth' : 'toast.enrolled', { name: entry.name }), 'success')
    if (extra.keptTooBig) addToast(t('toast.tooBig'), 'warning')
    if (extra.speechError) addToast(t('toast.speechFailed', { message: extra.speechError.message }), 'error')
  }

  const again = async (row) => {
    const saved = row.sampleUrl || row.thumbnail
    if (!saved) { setSheet({ name: row.name, labels: row.labels }); return }
    try {
      const data = await cfg.api.register(isFace
        ? { model, name: row.name, img: saved, labels: row.labels || {} }
        : { model, name: row.name, audio: saved, labels: row.labels || {} })
      reg.replace(row.id, { ...row, id: data.id, registeredAt: data.registered_at || new Date().toISOString(), model })
      addToast(t('toast.enrolled', { name: row.name }), 'success')
    } catch (err) {
      addToast(err.message, 'error')
    }
  }

  const noteFor = (r) => {
    const d = r.distance.toFixed(2)
    const c = findCut.toFixed(2)
    const n = find.matches.length
    if (r.within) return t(`${kind}.matchText`, { name: nameOf(r), distance: d, cutoff: c, count: n })
    return t(`${kind}.missText`, { name: nameOf(r), distance: d, cutoff: c })
  }

  const pairResult = pair.status === 'done' ? pair.data : null
  const pairModelCut = pairResult?.threshold || cfg.cutoff
  const pairActive = pairCut ?? pairModelCut
  const pairWithin = pairResult ? pairResult.distance <= pairActive : false
  const pairWord = pairResult ? strength(pairResult.distance, pairActive) : null

  const working = find.status === 'working' || pair.status === 'working'

  return (
    <div className="idn-bench" data-kind={kind}>
      {needed}
      {!needed && <section className="idn-tool dk-card" aria-label={t('tool.aria')}>
        <div className="idn-tool__bar">
          <div className="dk-segmented" role="tablist" aria-label={t('tool.aria')}>
            <button type="button" role="tab" className="dk-seg" aria-selected={tool === 'identify'} onClick={() => setTool('identify')}>{t(`${kind}.whoTab`)}</button>
            <button type="button" role="tab" className="dk-seg" aria-selected={tool === 'same'} onClick={() => setTool('same')}>{t('tool.sameTab')}</button>
          </div>
          <p className="dk-hint idn-tool__lede">{tool === 'identify' ? t(`${kind}.whoLede`) : t(`${kind}.sameLede`)}</p>
        </div>

        {tool === 'identify' && (
          <div className="idn-tool__body" role="tabpanel">
            <ClipInput kind={cfg.mode} label={t(isFace ? 'tool.photo' : 'tool.clip')} value={probe} onChange={(v) => { setProbe(v); setFind({ status: 'idle' }) }} idPrefix={`${kind}-probe`} onFacts={setProbeFacts} />
            <div className="idn-tool__go">
              <button type="button" className="dk-btn dk-btn--primary" onClick={identify} disabled={!model || !probe || find.status === 'working'} data-testid="identify-go">
                {find.status === 'working' ? <><LoadingSpinner size="sm" /> {t(`${kind}.working`)}</> : <>{t(`${kind}.whoGo`)} <Icon name="arrow-right" /></>}
              </button>
              <span className="dk-hint">{t(`${kind}.whoNote`, { count: known })}</span>
            </div>

            {find.status === 'working' && (
              <div className="idn-working" aria-busy="true" data-testid="identify-working">
                <div className="dk-progress" role="progressbar" aria-label={t('tool.workingAria')} data-indeterminate><span className="dk-progress-bar" /></div>
                <p className="dk-hint">{t(`${kind}.workingText`, { count: known })}</p>
              </div>
            )}

            {find.status === 'failed' && <ErrorNote error={find.error} kind={kind} onRetry={identify} />}

            {find.status === 'done' && (
              <div className="idn-result" data-testid="identify-result">
                {!best && (
                  <Verdict tone="none" title={t(known ? `${kind}.noneReturned` : `${kind}.noneEnrolled`)}>
                    {t(known ? `${kind}.noneReturnedText` : `${kind}.noneEnrolledText`)}
                  </Verdict>
                )}
                {best && best.within && (
                  <Verdict tone="match" title={t(`${kind}.probably`, { name: nameOf(best) })} word={best.word}>{noteFor(best)}</Verdict>
                )}
                {best && !best.within && (
                  <Verdict tone="miss" title={t(`${kind}.nobody`)} word={best.word === 'near' ? 'near' : null}>{noteFor(best)}</Verdict>
                )}

                {best && (
                  <>
                    <DistanceScale cutoff={findCut} noun={noun} dots={ranked.slice(0, 8).map(r => ({ id: r.id, label: nameOf(r).split(/\s+/)[0], distance: r.distance, within: r.within, best: r.rank === 1 && r.within }))} />
                    <ol className="idn-ranks" aria-label={t('ranks.aria')}>
                      {ranked.slice(0, 5).map(r => (
                        <li key={r.id} data-within={r.within ? 'true' : 'false'} data-testid="rank-row">
                          <span className="idn-ranks__n dk-mono">#{r.rank}</span>
                          <span className="idn-ranks__name">{nameOf(r)}</span>
                          <span className="dk-mono">{r.distance.toFixed(2)}</span>
                          {r.within ? <span className="dk-badge dk-badge--ok"><Icon name="check" /> {t('ranks.within')}</span> : <span className="dk-badge">{t('ranks.far')}</span>}
                        </li>
                      ))}
                    </ol>
                    <div className="idn-result__tune">
                      <CutoffControl value={findCut} onChange={setFindCut} modelDefault={cfg.cutoff} kind={`${kind}-find`} />
                      <HowToRead kind={kind} print={noun} />
                    </div>
                  </>
                )}
                <WhyWrong kind={kind} notes={probeFacts ? (probeFacts.seconds < 3 ? [t('why.short', { s: probeFacts.seconds.toFixed(1) })] : []) : []} />
                {(!best || !best.within) && (
                  <button type="button" className="dk-btn dk-btn--secondary idn-result__enrol" onClick={() => setSheet({ sample: probe })}>
                    <Icon name="plus" /> {t(`${kind}.enrolThis`)}
                  </button>
                )}
              </div>
            )}
          </div>
        )}

        {tool === 'same' && (
          <div className="idn-tool__body" role="tabpanel">
            <div className="idn-pair">
              <ClipInput kind={cfg.mode} label={t(isFace ? 'tool.photoA' : 'tool.clipA')} value={clipA} onChange={(v) => { setClipA(v); setPair({ status: 'idle' }) }} idPrefix={`${kind}-a`} />
              <ClipInput kind={cfg.mode} label={t(isFace ? 'tool.photoB' : 'tool.clipB')} value={clipB} onChange={(v) => { setClipB(v); setPair({ status: 'idle' }) }} idPrefix={`${kind}-b`} />
            </div>
            {isFace && (
              <label className="dk-choice idn-tool__option">
                <input className="dk-check" type="checkbox" checked={antiSpoof} onChange={(e) => setAntiSpoof(e.target.checked)} />
                <span>{t('face.antiSpoof')} <span className="dk-hint">{t('face.antiSpoofHint')}</span></span>
              </label>
            )}
            <div className="idn-tool__go">
              <button type="button" className="dk-btn dk-btn--primary" onClick={compare} disabled={!model || !clipA || !clipB || pair.status === 'working'} data-testid="compare-go">
                {pair.status === 'working' ? <><LoadingSpinner size="sm" /> {t('tool.comparing')}</> : <>{t('tool.compare')} <Icon name="arrow-right" /></>}
              </button>
              <span className="dk-hint">{t(`${kind}.sameNote`)}</span>
            </div>

            {pair.status === 'working' && (
              <div className="idn-working" aria-busy="true" data-testid="compare-working">
                <div className="dk-progress" role="progressbar" aria-label={t('tool.workingAria')} data-indeterminate><span className="dk-progress-bar" /></div>
              </div>
            )}
            {pair.status === 'failed' && <ErrorNote error={pair.error} kind={kind} onRetry={compare} />}

            {pairResult && (
              <div className="idn-result" data-testid="compare-result">
                <Verdict tone={pairWithin ? 'match' : 'miss'} title={t(pairWithin ? `${kind}.same` : `${kind}.different`)} word={pairWord === 'far' ? null : pairWord}>
                  {t('same.text', { print: noun, distance: pairResult.distance.toFixed(2), cutoff: pairActive.toFixed(2) })}
                </Verdict>
                <DistanceScale single cutoff={pairActive} noun={noun} dots={[{ id: 'pair', label: t('same.distance'), distance: pairResult.distance, within: pairWithin, best: pairWithin }]} />
                {isFace && (pairResult.img1_area || pairResult.img2_area) && (
                  <div className="idn-pair idn-pair--boxes">
                    {[[clipA, pairResult.img1_area, pairResult.img1_is_real], [clipB, pairResult.img2_area, pairResult.img2_is_real]].map(([clip, area, real], i) => (
                      <figure key={i} className="idn-face">
                        <BoundingBoxCanvas src={clip?.dataUrl} boxes={area ? [{ ...area, label: real === false ? t('face.spoof') : null, tone: 'accent' }] : []} alt={t('face.found', { n: i + 1 })} />
                        {antiSpoof && real != null && <figcaption><span className={`dk-badge dk-badge--${real ? 'ok' : 'error'}`}>{real ? t('face.real') : t('face.spoof')}</span></figcaption>}
                      </figure>
                    ))}
                  </div>
                )}
                <div className="idn-result__tune">
                  <CutoffControl value={pairActive} onChange={setPairCut} modelDefault={pairModelCut} kind={`${kind}-pair`} />
                  <HowToRead kind={kind} print={noun} />
                </div>
                <WhyWrong kind={kind} notes={[]} />
              </div>
            )}
          </div>
        )}
      </section>}

      <section className="idn-known" aria-labelledby={`${kind}-known-title`}>
        <div className="idn-known__head">
          <h2 className="idn-h2" id={`${kind}-known-title`}>{t(`${kind}.knownTitle`)} <span className="dk-hubtab-count">{known}</span></h2>
          <button type="button" className="dk-btn dk-btn--secondary" onClick={() => setSheet({})} disabled={!model} data-testid="enrol-open">
            <Icon name="plus" /> {t(`${kind}.enrol`)}
          </button>
        </div>
        <p className="dk-hint idn-known__note" data-testid="registry-note">{t(reg.searched ? 'known.noteSearched' : 'known.note', { noun })}</p>
        <KnownList
          entries={visibleEntries} extras={reg.extras} missing={reg.missing} waiting={reg.waiting}
          bestId={best?.within ? best.id : null} noun={noun} kind={kind}
          onDelete={(row) => reg.schedule(row)} onUndo={reg.undo} onAgain={again} onEnrol={() => setSheet({})} canEnrol={!!model}
        />
      </section>

      <Stored kind={kind} print={noun} />
      {more}

      {sheet && (
        <EnrolSheet kind={kind} model={model} canSpeech={canSpeech} prefill={sheet} onClose={() => setSheet(null)} onDone={onEnrolled} />
      )}
      {reg.latest && (
        <HomeUndoToast
          key={reg.latest.id}
          message={t('toast.removing', { name: reg.latest.name })}
          duration={UNDO_WINDOW_MS}
          onUndo={() => reg.undo(reg.latest.id)}
          onExpire={() => reg.finish(reg.latest.id)}
          undoLabel={t('known.undo')}
          dismissible={false}
          testId="forget-undo-toast"
        />
      )}
      {working && <span className="dk-sr-only" role="status">{t('tool.workingAria')}</span>}
    </div>
  )
}
