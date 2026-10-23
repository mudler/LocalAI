#!/bin/sh
# SPDX-License-Identifier: MIT
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$root"
[ "$#" -eq 1 ] && [ -n "$1" ] || { echo 'one nonempty suite required' >&2; exit 2; }
exec python3 - "$1" <<'PY'
import json, os, pathlib, subprocess, sys, tempfile, time, socket, urllib.request
root = pathlib.Path.cwd()
suite = sys.argv[1]
inventory = json.loads((root / 'scripts/addons/suites.json').read_text())
if suite not in inventory:
    sys.exit('unknown or unavailable suite: ' + suite)
entry = inventory[suite]
if not entry.get('commands') or not entry.get('cases'):
    sys.exit('empty command/test inventory')
cache = root / '.cache/addons'
(cache / 'logs').mkdir(parents=True, exist_ok=True)
(cache / 'tmp').mkdir(exist_ok=True)
env = dict(os.environ, GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOFLAGS='-mod=readonly',
           GOPATH=str(cache/'go'), GOMODCACHE=str(cache/'go/pkg/mod'), GOCACHE=str(cache/'go-build'),
           TMPDIR=str(cache/'tmp'), npm_config_cache=str(cache/'npm'), PLAYWRIGHT_BROWSERS_PATH=str(cache/'browsers'))
env['PATH'] = str(cache/'bin') + os.pathsep + env['PATH']
log = cache / 'logs' / ('verify-' + str(time.time_ns()) + '.jsonl')
def run(command, cwd=root):
    result = subprocess.run(command, cwd=cwd, env=env, text=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
    with log.open('a') as f:
        f.write(json.dumps(dict(command=command, status=result.returncode, output=result.stdout))+'\n')
    print(result.stdout, end='', flush=True)
    if result.returncode:
        sys.exit(failure_summary(command, result.stdout, result.returncode))
    return result.stdout
def failure_summary(command, output, status):
    """Names what failed, so a flake in a long gate run can be diagnosed later.

    It reads only: failed case lines of the child, failed specs of the Ginkgo
    reports the command names, and the last lines of the output. It never
    turns a failure into a pass."""
    names, lines = [], []
    for line in output.splitlines():
        try: item = json.loads(line)
        except ValueError: continue
        if isinstance(item, dict) and 'case' in item and item.get('status') not in (None, 'passed'):
            names.append(str(item['case']))
            lines.append('failed case: %s (%s)' % (item['case'], item['status']))
    for arg in command:
        if not arg.startswith('-ginkgo.json-report='): continue
        path = pathlib.Path(arg.split('=', 1)[1])
        path = path if path.is_absolute() else root / path
        try: reports = json.loads(path.read_text())
        except (OSError, ValueError): continue
        for report in reports if isinstance(reports, list) else []:
            for spec in report.get('SpecReports', []):
                if spec.get('State') in ('passed', 'skipped', 'pending'): continue
                message = ((spec.get('Failure') or {}).get('Message') or '').strip().splitlines()
                names.append(str(spec.get('LeafNodeText')))
                lines.append('failed spec: %s (%s)%s' % (spec.get('LeafNodeText'), spec.get('State'),
                             ': ' + message[0][:200] if message else ''))
    tail = [l.strip() for l in output.splitlines() if l.strip()][-5:]
    if tail: lines.append('last output: ' + ' | '.join(l[:200] for l in tail))
    lines.append('child status %d; failed: %s; log: %s' % (status, ', '.join(names[:10]) or 'unknown', log))
    return '\n'.join(lines)
def ginkgo_report(path, expected):
    try:
        reports = json.loads(path.read_text())
        assert isinstance(reports, list) and reports
        cases = []
        for report in reports:
            assert report['SuiteSucceeded'] is True
            specs = report['SpecReports']
            assert specs
            for spec in specs:
                assert spec['State'] == 'passed'
                if spec['LeafNodeType'] == 'It': cases.append(spec['LeafNodeText'])
        assert cases and sorted(cases) == sorted(expected)
    except (OSError, ValueError, KeyError, TypeError, AssertionError):
        sys.exit('Ginkgo report missing, empty, skipped, failed or inventory mismatch: '+str(path))
    return cases

seen = []
if entry['kind'] == 'ginkgo':
    reports = entry.get('reports', [dict(path=entry['report'], cases=entry['cases'])])
    if not reports: sys.exit('empty report inventory')
    for report in reports: (root/report['path']).unlink(missing_ok=True)
for command in entry['commands']:
    if entry['kind'] == 'ginkgo' and (command[:3] != ['go', 'test', '-count=1'] or '-tags' not in command or 'auth' not in command):
        sys.exit('Ginkgo command requires go test -count=1 -tags auth')
    for line in run(command).splitlines():
        try: item = json.loads(line)
        except ValueError: continue
        if 'case' in item:
            if item.get('status') != 'passed': sys.exit('required case not passed: '+item['case'])
            seen.append(item['case'])
if entry['kind'] == 'ginkgo':
    seen = []
    for report in reports: seen.extend(ginkgo_report(root/report['path'], report['cases']))
if sorted(seen) != sorted(entry['cases']):
    sys.exit('required case inventory mismatch: '+repr(seen))
# Run real readiness only after the shell test inventory has passed.
run(['sh', 'scripts/addons/prepare.sh', '--check'])
with tempfile.TemporaryDirectory(dir=cache/'tmp') as scratch:
    scratch = pathlib.Path(scratch)
    probe = scratch/'sqlite_test.go'
    probe.write_text('''package readiness_test
import ("database/sql"; "testing"; _ "github.com/mattn/go-sqlite3"
. "github.com/onsi/ginkgo/v2"; . "github.com/onsi/gomega")
func TestReadiness(t *testing.T) { RegisterFailHandler(Fail); RunSpecs(t, "Native readiness") }
var _ = Describe("SQLite", func() { It("sqlite transaction", func() {
db,e:=sql.Open("sqlite3", ":memory:"); Expect(e).NotTo(HaveOccurred()); defer db.Close()
tx,e:=db.Begin(); Expect(e).NotTo(HaveOccurred())
_,e=tx.Exec("create table readiness(value integer); insert into readiness values (42)"); Expect(e).NotTo(HaveOccurred())
Expect(tx.Commit()).To(Succeed()); var n int
Expect(db.QueryRow("select value from readiness").Scan(&n)).To(Succeed()); Expect(n).To(Equal(42))
}) })
''')
    report = cache/'logs'/('ginkgo-'+str(time.time_ns())+'.json')
    run(['go', 'test', '-count=1', '-p', '2', '-tags', 'auth', str(probe), '-ginkgo.json-report='+str(report)])
    ginkgo_report(report, ['sqlite transaction'])
    # OS-selected isolated port, bounded health wait, private server temporary data.
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0)); port = sock.getsockname()[1]
    env['TMPDIR'] = str(scratch)
    with (scratch/'server.log').open('w+') as output:
        command = [str(root/'tests/e2e-ui/ui-test-server'), '--mock-backend='+str(root/'tests/e2e/mock-backend/mock-backend'), '--port='+str(port)]
        server = subprocess.Popen(command, env=env, stdout=output, stderr=subprocess.STDOUT)
        try:
            url = 'http://127.0.0.1:'+str(port)
            for _ in range(120):
                if server.poll() is not None: raise RuntimeError('UI server exited '+str(server.returncode))
                try:
                    with urllib.request.urlopen(url+'/readyz', timeout=1) as response:
                        if response.status == 200: break
                except OSError: pass
                time.sleep(1)
            else: raise RuntimeError('UI server readiness timed out')
            # Local Playwright CLI and library, never npx or an implicit install.
            ui = root/'core/http/react-ui'
            run([str(ui/'node_modules/.bin/playwright'), '--version'])
            run(['node', '--input-type=module', '-e', '''
import { chromium } from '@playwright/test';
const browser = await chromium.launch({executablePath: process.env.PLAYWRIGHT_CHROMIUM_PATH || undefined});
try { const page=await browser.newPage(); const response=await page.goto(process.argv[1]);
if (!response.ok()) throw new Error('UI HTTP failure');
await page.locator('#root').waitFor();
await page.waitForFunction(()=>document.querySelector('#root')?.children.length > 0);
console.log(JSON.stringify({case:'browser',status:'passed'}));
} finally { await browser.close(); }
''', url], cwd=ui)
        finally:
            server.terminate()
            try: server.wait(timeout=15)
            except subprocess.TimeoutExpired: server.kill(); server.wait()
            output.seek(0)
            with log.open('a') as f: f.write(json.dumps(dict(command=command,status=server.returncode,output=output.read()))+'\n')
print(json.dumps(dict(suite=suite, passed=len(seen)+3, skipped=0, log=str(log))))
PY
