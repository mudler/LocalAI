/* eslint-disable no-unused-vars -- components used only inside JSX look unused to this config, which has no eslint-plugin-react */
import { useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { useTranslation } from 'react-i18next'
import { useClusterSettings } from '../../hooks/useClusterSettings'
import { useJoinToken } from '../../hooks/useJoinToken'
import { useWorkerInvite } from '../../hooks/useWorkerInvite'
import { defaultWays, needsInvite } from '../../utils/addWorker'
import { Button } from '../ui'
import AddSplitWorker from './AddSplitWorker'
import { ChooseStep, CommandStep, WaitStep } from './AddWorkerSteps'
import './addworker.css'

// The numbered step of the model-split way, which has its own steps.
function Step({ n, title, children, id }) {
  return (
    <section className="sw-step" aria-labelledby={id}>
      <span className="sw-step__n" aria-hidden="true">{n}</span>
      <div className="sw-step__body">
        <h2 className="sw-step__title" id={id}>{title}</h2>
        {children}
      </div>
    </section>
  )
}

const STEPS = ['choose', 'run', 'wait']

// "Add worker": which connections the worker uses, the command to run on the
// new machine with a join token minted by the server, and a live line that
// says when the worker has arrived. It reads the roster the page already polls,
// so "found" is a real node that was not there when the page opened, never a
// timer.
//
//   nodes     the roster (array)
//   addToast, onApproved
//   initialMethod  'worker' | 'shard': the way shown first. A model-split worker
//                  has its own steps.
export default function AddWorkerFlow({ nodes, addToast, onApproved, initialMethod, single = false }) {
  const { t } = useTranslation('cluster')
  const settings = useClusterSettings({ interval: 10000 })
  const [method, setMethod] = useState(initialMethod === 'shard' ? 'shard' : 'worker')
  const [step, setStep] = useState('choose')
  const [ways, setWays] = useState(null)
  const [uses, setUses] = useState(1)
  const [ttl, setTtl] = useState(600)
  const invite = useWorkerInvite({ active: step !== 'choose' && needsInvite(ways || []) })

  // The roster at the moment the page opened. Anything else is new.
  const baseline = useRef(null)
  if (baseline.current === null && Array.isArray(nodes)) baseline.current = new Set(nodes.map(node => node.id))

  const chosen = ways ?? defaultWays(settings.data, invite.mode)
  const token = useJoinToken({ ways: chosen, uses, ttl, enabled: step !== 'choose' })
  const again = () => {
    baseline.current = new Set((nodes || []).map(node => node.id))
    setStep('choose')
  }

  if (method === 'shard') {
    return (
      <div className="sw-add" data-testid="add-node">
        <AddSplitWorker Step={Step} first={1} addToast={addToast} />
        <p className="sw-note sw-note--quiet">
          {single
            ? <Button variant="ghost" size="sm" icon="arrow-left" to="/app/nodes/add" data-testid="add-back-to-worker">{t('add.backToWorker')}</Button>
            : <Button variant="ghost" size="sm" icon="arrow-left" onClick={() => setMethod('worker')} data-testid="add-back-to-worker">{t('add.backToWorker')}</Button>}
        </p>
      </div>
    )
  }

  const reached = STEPS.indexOf(step)
  return (
    <div className="aw" data-testid="add-node" data-step={step}>
      <ol className="aw-steps" aria-label={t('add.stepsLabel')}>
        {STEPS.map((id, index) => (
          <li key={id} className="aw-steps__item" data-state={index < reached ? 'done' : index === reached ? 'current' : 'todo'} aria-current={index === reached ? 'step' : undefined}>
            <span className="aw-steps__n" aria-hidden="true">{index < reached ? '✓' : index + 1}</span>
            <span className="aw-steps__label">{t(`add.step.${id}`)}</span>
          </li>
        ))}
      </ol>

      {step === 'choose' && (
        <ChooseStep
          settings={settings.data} p2pMode={invite.mode} ways={chosen} onWays={setWays}
          onContinue={() => setStep('run')} onSplit={() => setMethod('shard')}
        />
      )}
      {step === 'run' && (
        <CommandStep
          settings={settings.data} ways={chosen} token={token} invite={invite} uses={uses} onUses={setUses} ttl={ttl} onTtl={setTtl}
          addToast={addToast} onBack={() => setStep('choose')} onNext={() => setStep('wait')}
        />
      )}
      {step === 'wait' && (
        <WaitStep
          nodes={nodes} baseline={baseline.current} token={token} ways={chosen} addToast={addToast} onApproved={onApproved}
          onBack={() => setStep('run')} onAnother={again}
        />
      )}
    </div>
  )
}
