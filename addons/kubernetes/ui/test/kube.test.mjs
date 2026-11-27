import test from 'node:test'
import assert from 'node:assert/strict'
import { followName, withName, checkFailure, checkItem, extraBody, extraForm, extraValid, keyOf, kubeconfigContexts, kubeconfigProblem, kubeconfigServer, kubeSecret, MAX_CREDENTIAL, normalizeServer, rbacRows, splitKey, tokenKubeconfig, writeModeOf } from '../src/kube.js'
import en from '../src/i18n/kubernetes.en.json' with { type: 'json' }

// A kubeconfig as kubectl writes it (kubectl config view --flatten), with two
// contexts. The values are made up.
const KUBECONFIG = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==
    server: https://cluster.example:6443
  name: team-a
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==
    server: "https://Other.Example:443/"
  name: team-b
contexts:
- context:
    cluster: team-a
    namespace: default
    user: team-a-admin
  name: team-a
- context:
    cluster: team-b
    user: team-b-reader
  name: "team-b-ro"
current-context: team-a
kind: Config
preferences: {}
users:
- name: team-a-admin
  user:
    token: abc.def.ghi
- name: team-b-reader
  user:
    client-certificate-data: LS0tLS1CRUdJTg==
    client-key-data: LS0tLS1CRUdJTg==
`

test('kubernetes: reads the contexts and the current one from a kubectl-written kubeconfig', () => {
  assert.deepEqual(kubeconfigContexts(KUBECONFIG), { contexts: ['team-a', 'team-b-ro'], current: 'team-a' })
  assert.equal(kubeconfigServer(KUBECONFIG, 'team-a'), 'https://cluster.example:6443')
  assert.equal(kubeconfigServer(KUBECONFIG, 'team-b-ro'), 'https://other.example', 'the default port 443 is left out, as the add-on writes it')
  assert.equal(kubeconfigServer(KUBECONFIG, 'missing'), '')
  // An indented list, as some tools write it, reads the same.
  const indented = KUBECONFIG.replace(/^- /gm, '  - ').replace(/^ {4}(?=[a-z-]+:)/gm, '      ').replace(/^ {2}name:/gm, '    name:')
  assert.deepEqual(kubeconfigContexts(indented).contexts, ['team-a', 'team-b-ro'])
  assert.equal(kubeconfigServer(indented, 'team-a'), 'https://cluster.example:6443')
})

test('kubernetes: an unknown kubeconfig shape gives an empty list', () => {
  const empty = { contexts: [], current: '' }
  assert.deepEqual(kubeconfigContexts(''), empty)
  assert.deepEqual(kubeconfigContexts('not yaml at all {{{'), empty)
  assert.deepEqual(kubeconfigContexts('contexts: [{name: a, context: {cluster: c}}]\n'), empty)
  assert.deepEqual(kubeconfigContexts('contexts:\n- context:\n    cluster: c\n'), empty)
  assert.equal(kubeconfigServer('clusters: []\ncontexts: []\n', 'a'), '')
  // A server that is not https, or has a path, is not shown.
  assert.equal(kubeconfigServer(KUBECONFIG.replace('https://cluster.example:6443', 'http://cluster.example:6443'), 'team-a'), '')
  assert.equal(kubeconfigServer(KUBECONFIG.replace('https://cluster.example:6443', 'https://cluster.example:6443/k8s'), 'team-a'), '')
})

test('kubernetes: flags plugins, file references, insecure TLS, proxies, basic auth and size', () => {
  assert.equal(kubeconfigProblem(KUBECONFIG), '')
  assert.equal(kubeconfigProblem(''), '')
  const user = (lines) => KUBECONFIG.replace('    token: abc.def.ghi\n', lines)
  assert.equal(kubeconfigProblem(user('    exec:\n      apiVersion: client.authentication.k8s.io/v1\n      command: aws\n')), 'plugin')
  assert.equal(kubeconfigProblem(user('    auth-provider:\n      name: oidc\n')), 'plugin')
  assert.equal(kubeconfigProblem(user('    client-certificate: cert.pem\n')), 'fileRef')
  assert.equal(kubeconfigProblem(user('    client-key: key.pem\n')), 'fileRef')
  assert.equal(kubeconfigProblem(user('    tokenFile: /var/run/token\n')), 'fileRef')
  assert.equal(kubeconfigProblem(KUBECONFIG.replace('certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==\n    server: https://cluster', 'certificate-authority: /etc/ca.crt\n    server: https://cluster')), 'fileRef')
  assert.equal(kubeconfigProblem(KUBECONFIG.replace('    server: https://cluster.example:6443\n', '    server: https://cluster.example:6443\n    insecure-skip-tls-verify: true\n')), 'insecure')
  assert.equal(kubeconfigProblem(KUBECONFIG.replace('    server: https://cluster.example:6443\n', '    server: https://cluster.example:6443\n    insecure-skip-tls-verify: false\n')), '')
  assert.equal(kubeconfigProblem(KUBECONFIG.replace('    server: https://cluster.example:6443\n', '    server: https://cluster.example:6443\n    proxy-url: http://127.0.0.1:3128\n')), 'proxy')
  assert.equal(kubeconfigProblem(user('    username: admin\n    password: secret\n')), 'basicAuth')
  // The -data forms are embedded values, not file references.
  assert.equal(kubeconfigProblem(user('    client-certificate-data: LS0=\n    client-key-data: LS0=\n')), '')
  const big = `${KUBECONFIG}# ${'x'.repeat(MAX_CREDENTIAL)}\n`
  assert.equal(MAX_CREDENTIAL, 16384)
  assert.equal(kubeconfigProblem(big), 'tooLarge')
  // Size is counted in bytes, not characters.
  assert.equal(kubeconfigProblem(`# ${'é'.repeat(8200)}\n`), 'tooLarge')
})

test('kubernetes: builds a minimal kubeconfig from server, CA and token', () => {
  const ca = `-----BEGIN CERTIFICATE-----\n${'MIIB'.repeat(300).match(/.{1,64}/g).join('\n')}\n-----END CERTIFICATE-----\n`
  assert.ok(ca.length >= 1200)
  const token = `eyJhbGciOiJSUzI1NiJ9.${'a'.repeat(900)}.sig`
  const text = tokenKubeconfig({ server: 'https://cluster.example:6443', ca, token })
  assert.ok(new TextEncoder().encode(text).length < MAX_CREDENTIAL)
  assert.deepEqual(kubeconfigContexts(text), { contexts: ['localai'], current: 'localai' })
  assert.equal(kubeconfigServer(text, 'localai'), 'https://cluster.example:6443')
  assert.equal(kubeconfigProblem(text), '')
  const b64 = /certificate-authority-data: (\S+)/.exec(text)[1]
  assert.equal(Buffer.from(b64, 'base64').toString('utf8'), ca.trim() + '\n')
  assert.match(text, new RegExp(`token: "${token.replace(/\./g, '\\.')}"`))
  assert.doesNotMatch(text, /certificate-authority:/)
  // Without a server or a token there is no kubeconfig.
  assert.equal(tokenKubeconfig({ server: '', ca, token }), '')
  assert.equal(tokenKubeconfig({ server: 'https://cluster.example:6443', ca, token: '' }), '')
  assert.equal(tokenKubeconfig({ server: 'http://cluster.example', ca, token }), '')
})

test('kubernetes: makes and splits connection keys', () => {
  assert.equal(keyOf('https://Cluster.Example:6443/', 'team-a'), 'https://cluster.example:6443#context=team-a')
  assert.deepEqual(splitKey('https://cluster.example:6443#context=team-a'), { server: 'https://cluster.example:6443', context: 'team-a' })
  assert.deepEqual(splitKey('nonsense'), { server: '', context: '' })
  assert.equal(keyOf('http://cluster.example', 'a'), '')
  assert.equal(keyOf('https://cluster.example', ''), '')
  assert.equal(keyOf('https://cluster.example', 'has space'), '')
  assert.equal(keyOf('https://127.0.0.1:6443', `c${'x'.repeat(200)}`), '')
  assert.equal(keyOf('https://127.0.0.1:6443', 'team-a'), 'https://127.0.0.1:6443#context=team-a')
})

test('kubernetes: writes a server as the add-on does: canonical IP, numeric port, no trailing dots', () => {
  const cases = {
    'https://[2001:db8::1]': 'https://[2001:db8::1]',
    'https://[2001:DB8:0:0::1]': 'https://[2001:db8::1]',
    'https://[2001:0db8::0001]': 'https://[2001:db8::1]',
    'https://[2001:db8:0:0:0:0:0:1]': 'https://[2001:db8::1]',
    'https://[2001:db8:0:0:1:0:0:1]': 'https://[2001:db8::1:0:0:1]',
    'https://[::ffff:10.0.0.1]': 'https://10.0.0.1',
    'https://[::ffff:a00:1]:6443': 'https://10.0.0.1:6443',
    'https://[64:ff9b::7f00:1]': 'https://[64:ff9b::7f00:1]',
    'https://[::1.2.3.4]': 'https://[::102:304]',
    'https://cluster.example:0443': 'https://cluster.example',
    'https://cluster.example:00443': 'https://cluster.example',
    'https://cluster.example:06443': 'https://cluster.example:6443',
    'https://cluster.example..': 'https://cluster.example',
    'https://X.Example...:0443/': 'https://x.example',
  }
  for (const [input, want] of Object.entries(cases)) {
    assert.equal(normalizeServer(input), want, input)
    assert.equal(normalizeServer(normalizeServer(input)), want, `again ${input}`)
  }
  for (const bad of ['https://cluster.example:0', 'https://cluster.example:65536', 'https://...', 'https://[1::2::3]', 'https://[fe80::1%25eth0]', 'https://[12345::]', 'https://cluster.example?', 'https://cluster.example#', 'https://cluster.example/?']) {
    assert.equal(normalizeServer(bad), '', bad)
  }
})

test('kubernetes: shows at most 20 RBAC rows', () => {
  const rules = Array.from({ length: 23 }, (_, n) => ({ verbs: ['get', 'list'], apiGroups: n % 2 ? ['apps'] : [''], resources: [`r${n}`] }))
  const out = rbacRows(rules)
  assert.equal(out.rows.length, 20)
  assert.equal(out.more, 3)
  assert.deepEqual(out.rows[0], { verbs: 'get, list', resources: 'r0' })
  assert.deepEqual(out.rows[1], { verbs: 'get, list', resources: 'r1.apps' })
  assert.deepEqual(rbacRows([{ verbs: ['*'], apiGroups: ['*'], resources: ['*'] }]), { rows: [{ verbs: '*', resources: '*.*' }], more: 0 })
  assert.deepEqual(rbacRows([{ verbs: ['get'], apiGroups: [''], resources: ['configmaps'], resourceNames: ['app-config'] }]).rows[0], { verbs: 'get', resources: 'configmaps (app-config)' })
  assert.deepEqual(rbacRows(undefined), { rows: [], more: 0 })
})

test('kubernetes: the check result and its failures read as page words', () => {
  const ok = checkItem({ ok: true, check: { version: 'v1.31.2', namespace: 'default', rules: [], incomplete: false } })
  assert.deepEqual(ok, { tone: 'ok', key: 'check.ok', vars: { version: 'v1.31.2' } })
  assert.equal(checkItem({ ok: true, check: {} }).vars.version, '?')
  assert.equal(checkItem(null).key, 'check.failed')
  assert.equal(checkFailure(new Error('bad request: 401 Unauthorized: The cluster refused the credential of this connection (401).')), 'unauthorized')
  assert.equal(checkFailure(new Error('The credential of this connection has no RBAC permission for this (403).')), 'forbidden')
  assert.equal(checkFailure(new Error('the kubeconfig cannot be used: exec is not supported')), 'unsafe')
  assert.equal(checkFailure(new Error('the context of the kubeconfig points at another server than the connection')), 'mismatch')
  assert.equal(checkFailure(new Error('dial tcp 127.0.0.1:6443: connect: connection refused')), 'unreachable')
  assert.equal(checkFailure(new Error('boom')), 'other')
  for (const f of ['unauthorized', 'forbidden', 'unsafe', 'mismatch', 'unreachable', 'nocred']) {
    const i = checkItem({ failed: f })
    assert.equal(i.tone, 'warn')
    assert.ok(en.check[i.key.split('.')[1]], f)
  }
})

test('kubernetes: the form makes the key and the credential, and a new connection asks first', () => {
  const fresh = extraForm({})
  assert.equal(fresh.write_mode, 'propose')
  assert.equal(fresh.method, 'kubeconfig')
  const form = { ...fresh, kubeconfig: KUBECONFIG, context: 'team-a' }
  assert.equal(extraValid(form), true)
  assert.deepEqual(extraBody(form), { resources: ['https://cluster.example:6443#context=team-a'], write_mode: 'propose' })
  assert.equal(kubeSecret(form), KUBECONFIG)
  // A kubeconfig the add-on refuses cannot be saved.
  const bad = { ...form, kubeconfig: KUBECONFIG.replace('    token: abc.def.ghi\n', '    exec:\n      command: aws\n') }
  assert.equal(extraValid(bad), false)
  assert.equal(kubeSecret(bad), '')
  // The token method builds its own kubeconfig.
  const tok = { ...fresh, method: 'token', server: 'https://127.0.0.1:6443', ca: '', token: 'abc' }
  assert.equal(extraValid(tok), true)
  assert.deepEqual(extraBody(tok).resources, ['https://127.0.0.1:6443#context=localai'])
  assert.match(kubeSecret(tok), /current-context: localai/)
  // A saved connection keeps its key and mode, and saves without a new credential.
  const saved = extraForm({ id: 'c1', resources: ['https://cluster.example:6443#context=team-a'], write_mode: 'auto' })
  assert.equal(saved.write_mode, 'auto')
  assert.equal(saved.server, 'https://cluster.example:6443')
  assert.equal(saved.context, 'team-a')
  assert.equal(extraValid({ ...saved, id: 'c1' }), true)
  assert.deepEqual(extraBody({ ...saved, id: 'c1' }).resources, ['https://cluster.example:6443#context=team-a'])
  assert.equal(writeModeOf('nope'), 'off')
})

test('kubernetes: a new connection is named after its context until the person names it', () => {
  assert.equal(followName('', '', 'team-a'), 'Kubernetes: team-a')
  assert.equal(followName('Kubernetes: team-a', 'team-a', 'team-b-ro'), 'Kubernetes: team-b-ro')
  assert.equal(followName('Prod', 'team-a', 'team-b-ro'), 'Prod')
  const form = { ...extraForm({}), name: '' }
  assert.equal(withName(form, { ...form, kubeconfig: KUBECONFIG }).name, 'Kubernetes: team-a')
})
