// A stand-in for the LocalAI server. Serves /readyz and /v1/models and starts
// a grandchild in the same process group, so group signals can be tested.
// Settings come from the environment:
//   FAKE_PORT, FAKE_READY_DELAY_MS, FAKE_IGNORE_TERM=1, FAKE_PIDS_FILE, FAKE_EXIT_AFTER_MS,
//   FAKE_ENV_FILE (writes the variables named in FAKE_ENV_KEYS, comma separated, as JSON),
//   FAKE_ECHO_ENV (prints the value of that variable, to test that the launcher scrubs it)
//   FAKE_NOTIFY_KEY (the notification inbox needs `Authorization: Bearer <key>`; unset: no key needed)
//   The inbox: GET /api/chats/notifications/events (server-sent events), GET /api/chats/notifications/count,
//   and two test hooks: POST /__emit with {"event": "notification" | "notifications_changed", "data": {...}, "unread": N}
//   sends an event to every open stream (and sets the count, when given); GET /__stats answers {"streams": open, "connections": total}.
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
  const streams = new Set()
  let connections = 0
  let unread = 0
  const noteAuth = (req) => !process.env.FAKE_NOTIFY_KEY || req.headers.authorization === `Bearer ${process.env.FAKE_NOTIFY_KEY}`
  http
    .createServer((req, res) => {
      if (req.url === '/api/chats/notifications/events') {
        if (!noteAuth(req)) {
          res.statusCode = 401
          return res.end('{}')
        }
        connections++
        res.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' })
        res.write(': ping\n\n')
        streams.add(res)
        req.on('close', () => streams.delete(res))
        return
      }
      if (req.url === '/api/chats/notifications/count') {
        if (!noteAuth(req)) {
          res.statusCode = 401
          return res.end('{}')
        }
        res.setHeader('content-type', 'application/json')
        return res.end(JSON.stringify({ unread }))
      }
      if (req.url === '/__stats') return res.end(JSON.stringify({ streams: streams.size, connections }))
      if (req.url === '/__emit' && req.method === 'POST') {
        let body = ''
        req.on('data', (c) => (body += c))
        req.on('end', () => {
          const { event, data, unread: n, raw } = JSON.parse(body)
          if (typeof n === 'number') unread = n
          const frame = raw ?? `event: ${event}\ndata: ${JSON.stringify(data ?? {})}\n\n`
          for (const r of streams) r.write(frame)
          res.end('{}')
        })
        return
      }
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
