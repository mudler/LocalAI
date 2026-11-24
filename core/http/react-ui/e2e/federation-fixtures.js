// Fixtures for Swarm, P2P, Federation: this server, two LocalAI remotes (one
// hole-punched, one behind a relay that limits it) and one group, plus one
// function that answers every federation call the page makes and keeps the
// state, so a spec says only what is different about its case.
//
// Pure of Playwright imports so a script that is not a test can use it too.

export const BRANCH_PEER = '12D3KooWBn9sPqT4vX2mHcR8yLdQe5Gh6iKoPzWa7Ha'
export const FIELD_PEER = '12D3KooWFk2rN6mHcR8yLdQe5Gh6iKoPzTz10Vt'

export function federationInstances() {
  return [
    {
      id: 'local', name: '', local: true, in_flight: 2,
      models: [{ model: 'qwen3-8b', upstream_id: 'qwen3-8b' }, { model: 'whisper-1', upstream_id: 'whisper-1' }],
    },
    {
      id: 'r-branch', name: 'Branch LocalAI', local: false, url: `p2p://${BRANCH_PEER}`, transport: 'p2p', status: 'ok', in_flight: 1,
      p2p: { peer_id: BRANCH_PEER, local_port: 41001, state: 'up', path: 'hole_punched' },
      models: [{ model: 'branch-qwen3-8b', upstream_id: 'qwen3-8b' }, { model: 'gemma-3', upstream_id: 'gemma-3' }],
    },
    {
      id: 'r-field', name: 'Field kit', local: false, url: `p2p://${FIELD_PEER}`, transport: 'p2p', status: 'ok', in_flight: 0,
      p2p: { peer_id: FIELD_PEER, local_port: 41002, state: 'up', path: 'relayed', limited: true },
      models: [{ model: 'field-qwen3-8b', upstream_id: 'qwen3-8b' }],
    },
  ]
}

export function federationGroups() {
  return [{
    name: 'shared-qwen', policy: 'least_busy', state: 'primary',
    members: [
      { model: 'qwen3-8b', instance: 'local', upstream_id: 'qwen3-8b', drained: false, state: 'healthy', in_flight: 2 },
      { model: 'branch-qwen3-8b', instance: 'r-branch', instance_name: 'Branch LocalAI', upstream_id: 'qwen3-8b', drained: false, state: 'healthy', in_flight: 1 },
      { model: 'field-qwen3-8b', instance: 'r-field', instance_name: 'Field kit', upstream_id: 'qwen3-8b', drained: false, state: 'healthy', in_flight: 0, constrained: true },
    ],
  }]
}

// mockFederation answers the federation routes from a state the spec can
// read: calls lists every write as { method, path, body }.
export async function mockFederation(page, { groups = federationGroups(), instances = federationInstances() } = {}) {
  const state = { groups: structuredClone(groups), instances: structuredClone(instances), calls: [] }
  const json = (route, body, status = 200) => route.fulfill({ status, json: body })
  await page.route('**/api/federation**', async route => {
    const req = route.request()
    const url = new URL(req.url())
    const path = url.pathname.replace(/^.*\/api\/federation/, '')
    const method = req.method()
    const body = req.postData() ? JSON.parse(req.postData()) : undefined
    if (method !== 'GET') state.calls.push({ method, path, body })
    if (method === 'GET' && path === '') return json(route, { instances: state.instances, groups: state.groups })
    if (method === 'POST' && path === '/groups') {
      if (state.groups.some(g => g.name === body.name)) return json(route, { error: { message: 'a model with that name already exists' } }, 409)
      const members = body.members.map(model => {
        const inst = state.instances.find(i => i.models.some(m => m.model === model))
        return { model, instance: inst?.id, instance_name: inst?.name, upstream_id: inst?.models.find(m => m.model === model)?.upstream_id, drained: false, in_flight: 0 }
      })
      const g = { name: body.name, policy: body.policy || 'least_busy', members }
      state.groups.push(g)
      return json(route, g, 201)
    }
    const m = path.match(/^\/groups\/([^/]+)(\/drain)?$/)
    const g = m && state.groups.find(x => x.name === decodeURIComponent(m[1]))
    if (!g) return json(route, { error: { message: 'federation group not found' } }, 404)
    if (method === 'PATCH') {
      if (body.policy) g.policy = body.policy
      return json(route, g)
    }
    if (method === 'POST' && m[2]) {
      const mem = g.members.find(x => x.model === body.model)
      mem.drained = body.drain
      return json(route, g)
    }
    if (method === 'DELETE') {
      state.groups = state.groups.filter(x => x !== g)
      return route.fulfill({ status: 204, body: '' })
    }
    return json(route, g)
  })
  return state
}
