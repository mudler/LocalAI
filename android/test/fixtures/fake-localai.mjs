#!/usr/bin/env node
// SPDX-License-Identifier: MIT
// A tiny stand-in for a LocalAI server, for the emulator smoke test in CI
// (native/app/src/androidTest/.../SmokeTest.kt). It answers just enough for
// the app's server test and its WebUI view:
//
//   GET /.well-known/localai.json  the discovery document, version v9.9.9
//   GET /app                       an HTML page titled "Fake LocalAI"
//   anything else                  404
//
//   node test/fixtures/fake-localai.mjs 18080
//
// It listens on 127.0.0.1 only. CI maps the emulator's 127.0.0.1:18080 to it
// with `adb reverse`.

import { createServer } from 'node:http'
import { pathToFileURL } from 'node:url'

const DISCOVERY = JSON.stringify({ version: 'v9.9.9', endpoints: { chat_completions: '/v1/chat/completions' } })
const PAGE = '<!doctype html>\n<html><head><meta charset="utf-8"><title>Fake LocalAI</title></head><body><h1>Fake LocalAI</h1></body></html>\n'

function answer(res, status, type, body) {
  res.writeHead(status, { 'content-type': type, 'cache-control': 'no-store' })
  res.end(body)
}

/** Starts the server on 127.0.0.1:`port` (0 picks a free port) and resolves when it listens. */
export function startFakeLocalAI(port) {
  const server = createServer((req, res) => {
    const path = new URL(req.url ?? '/', 'http://127.0.0.1').pathname
    if (req.method === 'GET' && path === '/.well-known/localai.json') return answer(res, 200, 'application/json', DISCOVERY)
    if (req.method === 'GET' && path === '/app') return answer(res, 200, 'text/html; charset=utf-8', PAGE)
    answer(res, 404, 'text/plain', 'not found\n')
  })
  return new Promise((resolve, reject) => {
    server.once('error', reject)
    server.listen(port, '127.0.0.1', () => resolve(server))
  })
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const port = Number(process.argv[2])
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    console.error('usage: fake-localai.mjs <port>')
    process.exit(2)
  }
  const server = await startFakeLocalAI(port)
  console.log(`fake-localai: listening on http://127.0.0.1:${server.address().port}`)
}
