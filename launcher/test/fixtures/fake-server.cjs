// A stand-in for the LocalAI server. Serves /readyz and /v1/models and starts
// a grandchild in the same process group, so group signals can be tested.
// Settings come from the environment:
//   FAKE_PORT, FAKE_READY_DELAY_MS, FAKE_IGNORE_TERM=1, FAKE_PIDS_FILE, FAKE_EXIT_AFTER_MS,
//   FAKE_ENV_FILE (writes the variables named in FAKE_ENV_KEYS, comma separated, as JSON),
//   FAKE_ECHO_ENV (prints the value of that variable, to test that the launcher scrubs it)
const http = require('node:http')
const fs = require('node:fs')
const { spawn } = require('node:child_process')

const port = Number(process.env.FAKE_PORT)
const readyAt = Date.now() + Number(process.env.FAKE_READY_DELAY_MS || 0)
const ignoreTerm = process.env.FAKE_IGNORE_TERM === '1'

if (process.env.FAKE_GRANDCHILD === '1') {
  if (ignoreTerm) process.on('SIGTERM', () => {})
  setInterval(() => {}, 1000)
} else {
  if (ignoreTerm) process.on('SIGTERM', () => console.log('fake-server: ignoring SIGTERM'))
  const child = spawn(process.execPath, [__filename], {
    env: { ...process.env, FAKE_GRANDCHILD: '1' },
    stdio: 'ignore',
  })
  if (process.env.FAKE_ENV_FILE) {
    const keys = (process.env.FAKE_ENV_KEYS || '').split(',').filter(Boolean)
    fs.writeFileSync(process.env.FAKE_ENV_FILE, JSON.stringify(Object.fromEntries(keys.map((k) => [k, process.env[k] ?? null]))))
  }
  if (process.env.FAKE_ECHO_ENV) console.log(`fake-server: ${process.env.FAKE_ECHO_ENV}=${process.env[process.env.FAKE_ECHO_ENV]}`)
  if (process.env.FAKE_PIDS_FILE) fs.writeFileSync(process.env.FAKE_PIDS_FILE, JSON.stringify({ server: process.pid, grandchild: child.pid }))
  http
    .createServer((req, res) => {
      if (req.url === '/readyz') {
        res.statusCode = Date.now() >= readyAt ? 200 : 503
        return res.end('{}')
      }
      if (req.url === '/v1/models') {
        res.setHeader('content-type', 'application/json')
        return res.end(JSON.stringify({ data: [{ id: 'tiny-model' }, { id: 'other/model' }] }))
      }
      res.statusCode = 404
      res.end()
    })
    .listen(port, '127.0.0.1', () => console.log('fake-server: listening'))
  if (process.env.FAKE_EXIT_AFTER_MS) setTimeout(() => process.exit(3), Number(process.env.FAKE_EXIT_AFTER_MS))
}
