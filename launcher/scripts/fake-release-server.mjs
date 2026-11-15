// Serves a feed directory over HTTP and logs every request line to a file.
// usage: node fake-release-server.mjs <port> <dir> <logfile>
// A file `<path>.redirect` makes `<path>` answer 302 with the file's text as
// Location, the way github.com answers `releases/latest`.
// Files under `repos/` are the API answers (`repos/<owner>/<repo>/releases/tags/dev-master`
// is the rolling development release; see src/main/devFeed.ts) and are served as JSON.
import http from 'node:http'
import fs from 'node:fs'
import path from 'node:path'
const [port, dir, log] = [Number(process.argv[2]), process.argv[3], process.argv[4]]
http.createServer((req, res) => {
  fs.appendFileSync(log, `${req.method} ${req.url} ua=${req.headers['user-agent']} auth=${req.headers.authorization ? 'yes' : 'no'}\n`)
  const url0 = req.url.split('?')[0]
  const f = path.join(dir, decodeURIComponent(url0))
  if (f.startsWith(dir) && fs.existsSync(`${f}.redirect`)) {
    res.statusCode = 302
    res.setHeader('location', fs.readFileSync(`${f}.redirect`, 'utf8').trim())
    return res.end()
  }
  if (!f.startsWith(dir) || !fs.existsSync(f) || fs.statSync(f).isDirectory()) { res.statusCode = 404; return res.end('not found') }
  if (url0.startsWith('/repos/')) res.setHeader('content-type', 'application/json')
  res.setHeader('content-length', fs.statSync(f).size)
  fs.createReadStream(f).pipe(res)
}).listen(port, '127.0.0.1')
