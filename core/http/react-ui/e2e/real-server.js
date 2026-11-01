// Starts a real local-ai binary for the specs that need what the shared test
// server cannot give: authentication, or a first run with no model. The
// binary and the mock backend come from the environment (LOCALAI_BIN built
// with -tags auth, MOCK_BACKEND_BIN); the data lives in a temporary folder.
import { spawn } from 'node:child_process'
import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'

export const haveBinary = Boolean(process.env.LOCALAI_BIN && process.env.MOCK_BACKEND_BIN)

function freePort() {
  return new Promise((resolve, reject) => {
    const s = net.createServer()
    s.listen(0, '127.0.0.1', () => { const { port } = s.address(); s.close(() => resolve(port)) })
    s.on('error', reject)
  })
}

export async function startRealServer({ auth = false, withModel = true, env = {} } = {}) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'localai-real-'))
  const models = path.join(dir, 'models')
  fs.mkdirSync(models, { recursive: true })
  if (withModel) {
    fs.writeFileSync(path.join(models, 'mock-model.yaml'), 'name: mock-model\nbackend: mock-backend\nknown_usecases:\n  - chat\nparameters:\n  model: mock-model.bin\n')
  }
  const port = await freePort()
  const args = ['run', '--address', `127.0.0.1:${port}`, '--models-path', models, '--data-path', path.join(dir, 'data'),
    '--localai-config-dir', path.join(dir, 'config'), '--backends-path', path.join(dir, 'backends'),
    '--external-grpc-backends', `mock-backend:${process.env.MOCK_BACKEND_BIN}`]
  const childEnv = { ...process.env, LOCALAI_AUTOLOAD_GALLERIES: 'false', LOCALAI_AUTOLOAD_BACKEND_GALLERIES: 'false', ...env }
  delete childEnv.API_KEY
  delete childEnv.LOCALAI_API_KEY
  if (auth) childEnv.LOCALAI_AUTH = 'true'
  const child = spawn(process.env.LOCALAI_BIN, args, { env: childEnv, stdio: ['ignore', fs.openSync(path.join(dir, 'server.log'), 'a'), fs.openSync(path.join(dir, 'server.log'), 'a')], cwd: dir })
  const url = `http://127.0.0.1:${port}`
  const deadline = Date.now() + 150_000
  for (;;) {
    try { if ((await fetch(`${url}/readyz`)).ok) break } catch { /* not up yet */ }
    if (child.exitCode !== null) throw new Error(`local-ai exited with ${child.exitCode}: ${fs.readFileSync(path.join(dir, 'server.log'), 'utf8').slice(-600)}`)
    if (Date.now() > deadline) { child.kill(); throw new Error('local-ai did not start in time') }
    await new Promise(r => setTimeout(r, 500))
  }
  return {
    url,
    async close() {
      child.kill('SIGTERM')
      await new Promise(r => { child.on('exit', r); setTimeout(r, 5000) })
      fs.rmSync(dir, { recursive: true, force: true })
    },
  }
}
