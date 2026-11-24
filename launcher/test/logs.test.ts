import fs from 'node:fs'
import path from 'node:path'
import { afterEach, describe, expect, it } from 'vitest'
import { defaultLogsDir, exportLogs, LogHub, LogRing, logFiles, redact, RotatingFileWriter, type LogLine, type LogSink } from '../src/main/logs'
import { tmpDir } from './helpers'

const hubs: LogHub[] = []
afterEach(() => {
  for (const h of hubs.splice(0)) h.close()
})
function hub(extra: Partial<ConstructorParameters<typeof LogHub>[0]> = {}) {
  const dir = path.join(tmpDir('logs-'), 'logs')
  const h = new LogHub({ dir, ...extra })
  hubs.push(h)
  return { h, dir }
}

describe('redact', () => {
  const cases: Array<[string, string]> = [
    ['Authorization: Bearer abc.def-123', 'Authorization: [REDACTED]'],
    ['authorization=Basic dXNlcjpwYXNz', 'authorization=[REDACTED]'],
    ['sent header Bearer abc123xyz ok', 'sent header Bearer [REDACTED] ok'],
    ['api_key=sk-12345 next', 'api_key=[REDACTED] next'],
    ['LOCALAI_API_KEY=sk-12345', 'LOCALAI_API_KEY=[REDACTED]'],
    ['{"password": "hunter2", "user": "bob"}', '{"password": [REDACTED], "user": "bob"}'],
    ['GET /x?token=abc123&page=2', 'GET /x?token=[REDACTED]&page=2'],
    ['client_secret: "a b c"', 'client_secret: [REDACTED]'],
    ['run --api-key sk-999 --port 80', 'run --api-key [REDACTED] --port 80'],
    ['api-key = abc', 'api-key = [REDACTED]'],
  ]
  it.each(cases)('%s', (input, expected) => {
    expect(redact(input)).toBe(expected)
  })
  it('leaves ordinary lines alone', () => {
    for (const l of ['loaded 12 tokens=2048', 'max_tokens 100', 'API server listening on :8080', 'password policy is fine']) {
      expect(redact(l)).toBe(l)
    }
  })
  it('is idempotent', () => {
    const once = redact('Authorization: Bearer zzz api_key=abc')
    expect(redact(once)).toBe(once)
  })
})

describe('ring', () => {
  it('stays within capacity and drops the oldest', () => {
    const r = new LogRing({ capacity: 3 })
    for (let i = 1; i <= 10; i++) r.push('server', 'info', `l${i}`)
    expect(r.size).toBe(3)
    expect(r.query().map((l) => l.text)).toEqual(['l8', 'l9', 'l10'])
    expect(r.query()[2].seq).toBe(10)
  })
  it('clips very long lines', () => {
    const r = new LogRing({ maxLineLength: 32 })
    const l = r.push('server', 'info', 'x'.repeat(500))
    expect(l.text.length).toBeLessThan(80)
    expect(l.text).toContain('[truncated]')
  })
  it('filters by source, level, seq and limit', () => {
    const r = new LogRing()
    r.push('server', 'info', 'a')
    r.push('launcher', 'warn', 'b')
    r.push('server', 'error', 'c')
    r.push('updater', 'debug', 'd')
    expect(r.query({ source: 'server' }).map((l) => l.text)).toEqual(['a', 'c'])
    expect(r.query({ minLevel: 'warn' }).map((l) => l.text)).toEqual(['b', 'c'])
    expect(r.query({ afterSeq: 2 }).map((l) => l.text)).toEqual(['c', 'd'])
    expect(r.query({ limit: 2 }).map((l) => l.text)).toEqual(['c', 'd'])
  })
  it('delivers to subscribers until they unsubscribe, and survives a throwing one', () => {
    const r = new LogRing()
    const got: LogLine[] = []
    r.subscribe(() => {
      throw new Error('bad subscriber')
    })
    const off = r.subscribe((l) => got.push(l))
    r.push('server', 'info', 'one')
    off()
    r.push('server', 'info', 'two')
    expect(got.map((l) => l.text)).toEqual(['one'])
    expect(r.size).toBe(2)
  })
})

describe('rotating file', () => {
  it('rotates at the size cap and keeps a few files', () => {
    const dir = tmpDir('rot-')
    const w = new RotatingFileWriter({ dir, name: 'x.log', maxBytes: 100, keep: 2 })
    for (let i = 0; i < 40; i++) w.write(`line-${String(i).padStart(3, '0')}-${'.'.repeat(20)}`)
    w.close()
    const names = fs.readdirSync(dir).sort()
    expect(names).toEqual(['x.log', 'x.log.1', 'x.log.2'])
    for (const n of names) expect(fs.statSync(path.join(dir, n)).size).toBeLessThanOrEqual(100)
    // newest line is in the current file, older ones in higher numbers
    expect(fs.readFileSync(path.join(dir, 'x.log'), 'utf8')).toContain('line-039')
    const oldest = fs.readFileSync(path.join(dir, 'x.log.2'), 'utf8')
    const newer = fs.readFileSync(path.join(dir, 'x.log.1'), 'utf8')
    expect(oldest.split('\n')[0] < newer.split('\n')[0]).toBe(true)
    expect(logFiles(dir, 'x.log').map((f) => path.basename(f))).toEqual(['x.log.2', 'x.log.1', 'x.log'])
  })
  it('continues after a restart and creates files with mode 0600', () => {
    const dir = tmpDir('rot-')
    const a = new RotatingFileWriter({ dir, name: 'x.log' })
    a.write('first')
    a.close()
    const b = new RotatingFileWriter({ dir, name: 'x.log' })
    b.write('second')
    b.close()
    expect(fs.readFileSync(path.join(dir, 'x.log'), 'utf8')).toBe('first\nsecond\n')
    expect(fs.statSync(path.join(dir, 'x.log')).mode & 0o777).toBe(0o600)
  })
  it('never throws when the directory cannot be created', () => {
    const dir = tmpDir('rot-')
    const blocker = path.join(dir, 'file')
    fs.writeFileSync(blocker, '')
    const w = new RotatingFileWriter({ dir: path.join(blocker, 'sub'), name: 'x.log' })
    expect(() => w.write('hello')).not.toThrow()
    expect(w.lastError).toBeDefined()
  })
})

describe('hub', () => {
  it('is a LogSink: feeds ring, subscribers and the per-source file', () => {
    const { h, dir } = hub({ now: () => Date.UTC(2026, 10, 14, 12, 0, 0) })
    const sink: LogSink = h
    const got: LogLine[] = []
    h.subscribe((l) => got.push(l))
    sink.write('server', '2026-11-14 ERR failed to load\n')
    sink.write('launcher', 'started')
    sink.write('updater', 'checking', 'debug')
    expect(got.map((l) => [l.source, l.level, l.text])).toEqual([
      ['server', 'error', '2026-11-14 ERR failed to load'],
      ['launcher', 'info', 'started'],
      ['updater', 'debug', 'checking'],
    ])
    expect(fs.readFileSync(path.join(dir, 'server.log'), 'utf8')).toBe('2026-11-14T12:00:00.000Z ERROR 2026-11-14 ERR failed to load\n')
    expect(fs.existsSync(path.join(dir, 'launcher.log'))).toBe(true)
    expect(fs.existsSync(path.join(dir, 'updater.log'))).toBe(true)
  })
  it('redacts before the ring and the file see a line', () => {
    const { h, dir } = hub()
    h.write('server', 'request Authorization: Bearer SECRETTOKEN123')
    expect(h.query()[0].text).not.toContain('SECRETTOKEN123')
    expect(fs.readFileSync(path.join(dir, 'server.log'), 'utf8')).not.toContain('SECRETTOKEN123')
  })
  it('keeps the ring bounded', () => {
    const { h } = hub({ ring: { capacity: 5 } })
    for (let i = 0; i < 50; i++) h.write('launcher', `n${i}`)
    expect(h.query().length).toBe(5)
  })
  it('defaultLogsDir puts logs under the data folder', () => {
    expect(defaultLogsDir('/home/u/.localai')).toBe('/home/u/.localai/logs')
  })
})

describe('export', () => {
  it('bundles all three sources, rotated files included, redacted', async () => {
    const { h, dir } = hub({ maxBytes: 200, keep: 3 })
    for (let i = 0; i < 12; i++) h.write('server', `line ${i} ${'x'.repeat(30)}`)
    h.write('launcher', 'launcher api_key=LAUNCHSECRET1')
    h.write('updater', 'feed token=UPDSECRET2')
    // an old file written before redaction existed
    fs.appendFileSync(path.join(dir, 'launcher.log'), 'legacy password=OLDSECRET3\n')

    const dest = path.join(tmpDir('exp-'), 'out', 'bundle.txt')
    const res = await h.exportLogs(dest)
    const text = fs.readFileSync(dest, 'utf8')

    expect(res.path).toBe(dest)
    expect(res.files).toBeGreaterThanOrEqual(5)
    expect(fs.statSync(dest).mode & 0o777).toBe(0o600)
    expect(text.indexOf('===== server =====')).toBeLessThan(text.indexOf('===== launcher ====='))
    expect(text.indexOf('===== launcher =====')).toBeLessThan(text.indexOf('===== updater ====='))
    expect(text).toContain('--- server.log.1 ---')
    expect(text).toContain('line 11')
    expect(text).toContain('feed token=[REDACTED]')
    for (const s of ['LAUNCHSECRET1', 'UPDSECRET2', 'OLDSECRET3']) expect(text).not.toContain(s)
    // oldest rotated file comes before the current one
    expect(text.indexOf('server.log.1')).toBeLessThan(text.indexOf('--- server.log ---'))
  })
  it('works with no logs yet', async () => {
    const dir = path.join(tmpDir('exp-'), 'none')
    const dest = path.join(tmpDir('exp-'), 'b.txt')
    const res = await exportLogs(dir, dest)
    expect(res.files).toBe(0)
    expect(fs.readFileSync(dest, 'utf8')).toContain('(no log)')
  })
})
