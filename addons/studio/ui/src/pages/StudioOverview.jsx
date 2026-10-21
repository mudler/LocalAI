import { useCallback, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import PageHeader from '../components/PageHeader'
import Icon from '../components/Icon'
// eslint-disable-next-line no-unused-vars
import StudioComposer from '../components/studio/StudioComposer'
// eslint-disable-next-line no-unused-vars
import WorkMasonry from '../components/studio/WorkMasonry'
// eslint-disable-next-line no-unused-vars
import LineageView from '../components/studio/LineageView'
import { useStudioWork } from '../hooks/useStudioWork'
import { readLastModel } from '../utils/lastModel'
import { TYPE_ORDER } from '../utils/studioWork'
import '../components/studio/studio.css'

// The Studio front page: a prompt box that suggests what to make, and under it
// the things already made, with results that came from each other stacked into
// projects. A result or a stack opens as a lineage board at ?work=<id>, so Back
// and a reload both land where you were.
//
// The page does not generate anything itself. The composer opens the right
// workspace with the prompt filled in; each workspace is still where a run
// happens, is recorded, and shows its request.
//
//   modalities     the workspaces this person can use, each with the ids of the
//                  models installed for it
//   modelsLoading  true until the first answer about installed models
//   modelsError    set when that answer could not be read
//   refetchModels  ask again, used while a model installs
export default function StudioOverview({ modalities, upscalers, modelsLoading, modelsError, refetchModels }) {
  const { t } = useTranslation('media')
  const navigate = useNavigate()
  const [searchParams, setSearchParams] = useSearchParams()
  const { items, ready, addImageEntry, toggleFavourite, clearHistory } = useStudioWork()
  const [filter, setFilter] = useState('all')
  const [group, setGroup] = useState(true)
  // The composer's state lives here, so opening a lineage and coming back, or a
  // model installing, never costs the words that were typed.
  const [draft, setDraft] = useState({ type: '', text: '', models: {}, sizes: {}, count: 1, sourceId: '' })

  const workId = searchParams.get('work')
  const types = TYPE_ORDER.filter(k => modalities.some(m => m.key === k))

  // The model a type opens with: the one last picked in its workspace when it is
  // still installed, else the first installed.
  const defaultModel = useCallback((type, ids) => {
    const capability = modalities.find(m => m.key === type)?.capability
    const last = readLastModel(capability)
    return ids.includes(last) ? last : (ids[0] || '')
  }, [modalities])

  const open = (id) => setSearchParams({ work: id })
  const close = useCallback(() => setSearchParams({}), [setSearchParams])
  const handoff = useCallback((path) => navigate(path), [navigate])

  if (workId) {
    return (
      <div data-testid="studio-overview" className="page-pad studio-front">
        {!ready ? (
          <div className="dk-skeleton studio-skel studio-skel--board" aria-busy="true" />
        ) : (
          <LineageView
            key={workId}
            items={items}
            upscalers={upscalers}
            onAddImage={addImageEntry}
            workId={workId}
            modalities={modalities}
            defaultModel={defaultModel}
            onClose={close}
            onToggleFavourite={toggleFavourite}
            onHandoff={handoff}
            onModelsChanged={refetchModels}
          />
        )}
      </div>
    )
  }

  return (
    <div data-testid="studio-overview" className="page-pad studio-front">
      <PageHeader
        title={t('studio.overview.title')}
        supporting={t('studio.overview.subtitle')}
        actions={<span className="studio-key-hint"><kbd className="dk-kbd">/</kbd> {t('studio.composer.startTyping')}</span>}
      />

      {modelsError ? (
        <div className="studio-note studio-note--error studio-note--standalone" role="alert" data-testid="studio-models-error">
          <h3>{t('studio.error.title')}</h3>
          <p>{t('studio.error.body')}</p>
          <div className="studio-note__row">
            <button type="button" className="dk-btn dk-btn--secondary dk-btn--sm" onClick={refetchModels}>
              <Icon name="refresh" /> {t('studio.error.retry')}
            </button>
          </div>
        </div>
      ) : modelsLoading ? (
        <section className="studio-composer" aria-busy="true" data-testid="studio-composer-loading">
          <div className="dk-skeleton studio-skel studio-skel--composer" />
        </section>
      ) : (
        <StudioComposer
          draft={draft}
          setDraft={setDraft}
          modalities={modalities}
          items={items}
          onHandoff={handoff}
          onModelsChanged={refetchModels}
          defaultModel={defaultModel}
        />
      )}

      <WorkMasonry
        items={items}
        ready={ready}
        types={types}
        filter={filter}
        onFilter={setFilter}
        group={group}
        onGroup={setGroup}
        onOpen={open}
        onToggleFavourite={toggleFavourite}
        onClear={clearHistory}
      />
    </div>
  )
}
