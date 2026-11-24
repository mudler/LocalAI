import test from 'node:test'
import assert from 'node:assert/strict'
import { projectModelActions, dispatchModelAction } from '../../src/addons/modelActions.js'
const app = (id = 'paint') => ({ id, title: 'Creative', enabled: true, state: 'running', generation: 3, ui: { sdk: { min: 2, max: 2 }, entry: `/api/addons/${id}/assets/${'a'.repeat(64)}/ui.js`, pages: ['canvas'], modelActions: [{ capability: 'FLAG_IMAGE', page: 'canvas', path: 'images' }] } })
const snapshot = (apps = [app()], extra = {}) => ({ allowed: true, known: true, status: 'ready', apps, actor: 'owner:1', ...extra })
test('Model actions: arbitrary providers deterministic collisions and selected model encoding', () => {
 const a = projectModelActions(snapshot([app('zebra'), app('arbitrary')]), 'FLAG_IMAGE', 'org/a:b雪?&')
 assert.deepEqual(a.choices.map(c => c.addonId), ['arbitrary', 'zebra'])
 assert.deepEqual(a, projectModelActions(snapshot([app('arbitrary'), app('zebra')]), 'FLAG_IMAGE', 'org/a:b雪?&'))
 assert.equal(new URL(a.choices[0].url, 'http://host').searchParams.get('model'), 'org/a:b雪?&')
 assert.equal(projectModelActions(snapshot(), 'FLAG_CHAT', 'm').choices.length, 0)
})
test('Model actions: absence forbidden loading errors and installed lifecycle are distinct', () => {
 for (const [extra, state] of [[{allowed:false}, 'forbidden'], [{allowed:false,known:false},'loading'], [{status:'idle'},'loading'], [{status:'error'},'list-error']]) assert.equal(projectModelActions(snapshot([], extra), 'FLAG_IMAGE', 'm').status, state)
 assert.equal(projectModelActions(snapshot([]), 'FLAG_IMAGE', 'm').status, 'absent')
 for (const [patch, state] of [[{enabled:false},'installed-disabled'], [{state:'starting'},'starting'], [{state:'failed'},'installed-failed'], [{ui:{...app().ui,sdk:{min:99,max:99}}},'incompatible']]) {
  const p = projectModelActions(snapshot([{...app(),...patch}]), 'FLAG_IMAGE', 'm')
  assert.equal(p.choices.length, 0); assert.equal(p.states[0].status,state)
 }
})
test('Model actions: strict SDK bounded declaration validation fails closed', () => {
 for (const patch of [{page:'missing'}, {path:'../x'}, {path:'/x'}, {path:'x?model=evil'}, {path:'x#hash'}, {path:''}, {path:'x/'.repeat(9)}, {path:'a'.repeat(65)}, {capability:'FLAG_CHAT'}, {url:'https://evil'}]) {
 const a = app(); Object.assign(a.ui.modelActions[0],patch)
 assert.equal(projectModelActions(snapshot([a]), 'FLAG_IMAGE','m').choices.length,0)
 }
 const a = app(); a.ui.modelActions.push({...a.ui.modelActions[0]})
 assert.equal(projectModelActions(snapshot([a]), 'FLAG_IMAGE','m').choices.length,0)
 assert.equal(projectModelActions(snapshot([app(),app()]), 'FLAG_IMAGE','m').choices.length,0)
})
test('Model actions: fresh dispatch refuses changed actor generation immutable entry and destination', async () => {
 const s = snapshot(); const choice = projectModelActions(s,'FLAG_IMAGE','selected').choices[0]
 for (const mutate of [a=>({...a,generation:4}),a=>({...a,ui:{...a.ui,entry:a.ui.entry.replace('a'.repeat(64),'b'.repeat(64))}}),a=>({...a,ui:{...a.ui,modelActions:[{capability:'FLAG_IMAGE',page:'canvas',path:'other'}]}}), a=>({...a,enabled:false})]) {
 let now = {...s,list:s}; let visited = false
 assert.equal(await dispatchModelAction(choice,{current:()=>now,refresh:async()=>{const apps=[mutate(app())]; now={...now,list:snapshot(apps)};return apps},navigate:()=>{visited=true}}),false)
 assert.equal(visited,false)
 }
 let now = {...s,list:s}; let visited
 assert.equal(await dispatchModelAction(choice,{current:()=>now,refresh:async()=>{const apps=[app()];now={...now,list:snapshot(apps)};return apps},navigate:p=>{visited=p}}),true)
 assert.equal(new URL(visited,'http://host').searchParams.get('model'),'selected')
 assert.equal(await dispatchModelAction(choice,{current:()=>now,refresh:async()=>{now={...now,actor:'owner:2'};return now.list.apps},navigate:()=>assert.fail()}),false)
 assert.equal(await dispatchModelAction({...choice,actor:'previous'}, {current:()=>now,refresh:async()=>now.list.apps,navigate:()=>assert.fail()}),false)
})

test('Model actions: revoked session authority refuses a pending refresh before consumer render', async () => {
 const s = snapshot(), choice = projectModelActions(s,'FLAG_IMAGE','m').choices[0]
 let valid = true
 const now = {...s,list:s}
 assert.equal(await dispatchModelAction(choice, {current:()=>now,sessionCurrent:()=>valid,refresh:async()=>{valid=false;return s.apps},navigate:()=>assert.fail('revoked session navigated')}),false)
})
