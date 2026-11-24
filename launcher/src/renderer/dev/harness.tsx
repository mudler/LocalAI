// Screenshot harness: renders the shell from a named fixture with a fake
// bridge. Open harness.html?fixture=<name>&theme=light|dark. Not part of the app.
import { createRoot } from 'react-dom/client'
import '../styles'
import './harness.css'
import { App } from '../App'
import { fakeBridge } from './fakeBridge'
import { fixture, type FixtureName } from './fixtures'

const q = new URLSearchParams(location.search)
const name = (q.get('fixture') ?? 'ready') as FixtureName
const theme = q.get('theme')
if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme
const now = Date.now()
const bridge = fakeBridge(fixture(name, now), { extras: q.get('extras') !== 'none' })
;(window as unknown as { harness: unknown }).harness = { bridge }
createRoot(document.getElementById('root')!).render(<App bridge={bridge} now={now} />)

// Scripted steps for screenshots of states a click reaches: ?steps=click:Menu;click:Settings
// click:<name>  presses the control with that accessible name or text
// type:<label>=<value>  types into the field with that label
// key:<Key>  presses a key on the focused element; wait:<ms> waits
const steps = (q.get('steps') ?? '').split(';').filter(Boolean)
if (steps.length) {
  const accName = (el: Element) => (el.getAttribute('aria-label') ?? el.textContent ?? '').replace(/\s+/g, ' ').trim()
  const find = (name: string): HTMLElement | undefined =>
    [...document.querySelectorAll<HTMLElement>('button, [role=tab], [role=menuitem], [role=menuitemradio], label, a')].find((e) => accName(e) === name) ??
    [...document.querySelectorAll<HTMLElement>('button, [role=tab], [role=menuitem], [role=menuitemradio], label, a')].find((e) => accName(e).startsWith(name))
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms))
  void (async () => {
    await sleep(400)
    for (const step of steps) {
      const i = step.indexOf(':')
      const kind = step.slice(0, i)
      const arg = step.slice(i + 1)
      if (kind === 'click') find(arg)?.click()
      else if (kind === 'type') {
        const [label, value] = arg.split('=')
        const input = [...document.querySelectorAll<HTMLInputElement>('input')].find((e) => document.querySelector(`label[for="${e.id}"]`)?.textContent === label || e.getAttribute('aria-label') === label)
        if (input) {
          Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, value)
          input.dispatchEvent(new Event('input', { bubbles: true }))
        }
      } else if (kind === 'key') document.activeElement?.dispatchEvent(new KeyboardEvent('keydown', { key: arg, bubbles: true }))
      else if (kind === 'wait') await sleep(Number(arg))
      await sleep(250)
    }
    document.body.dataset.stepsDone = '1'
  })()
}

// Screenshots show the end of every entrance animation, not a frame of it.
if (q.get('still') !== '0') {
  const st = document.createElement('style')
  st.textContent = '*, *::before, *::after { animation-duration: 0s !important; animation-delay: 0s !important; transition-duration: 0s !important; transition-delay: 0s !important; }'
  document.head.appendChild(st)
}
