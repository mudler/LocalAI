import { describe, expect, it } from 'vitest'
import { prepareRemove, prepareSetup, runPlan, spawnRunner, type RunResult, type Runner } from '../src/main/sandboxSetup'
import { InvalidAttachPattern } from '../src/main/sandboxProfile'

function fake(results: Array<RunResult | Error>) {
  const calls: Array<{ argv: readonly string[]; input?: string }> = []
  const runner: Runner = async (argv, input) => {
    calls.push({ argv, input })
    const r = results[calls.length - 1] ?? { exitCode: 0, stderr: '' }
    if (r instanceof Error) throw r
    return r
  }
  return { runner, calls }
}
const ok: RunResult = { exitCode: 0, stderr: '' }

describe('prepare', () => {
  it('returns the exact text and commands before anything runs', () => {
    const p = prepareSetup('/opt/app/launcher')
    expect(p.profileText).toContain('userns,')
    expect(p.steps).toHaveLength(2)
    expect(p.terminalText).toContain('sudo')
  })
  it('refuses an unsafe pattern before building a plan', () => {
    expect(() => prepareSetup('/opt/x\nuserns,')).toThrow(InvalidAttachPattern)
  })
  it('prepares removal without a pattern', () => {
    expect(prepareRemove().steps).toHaveLength(2)
  })
})

describe('runPlan', () => {
  it('runs every step as an argv array, with the file text on stdin', async () => {
    const plan = prepareSetup('/opt/app/launcher')
    const { runner, calls } = fake([ok, ok])
    const r = await runPlan(plan, runner)
    expect(r).toEqual({ status: 'ok', completedSteps: 2 })
    expect(calls.map((c) => c.argv)).toEqual(plan.steps.map((s) => s.argv))
    expect(calls[0].input).toBe(plan.profileText)
    for (const c of calls) expect(Array.isArray(c.argv)).toBe(true)
  })
  it('maps 126 to dismissed and stops', async () => {
    const { runner, calls } = fake([{ exitCode: 126, stderr: '' }])
    expect(await runPlan(prepareSetup('/opt/app/launcher'), runner)).toEqual({ status: 'dismissed', completedSteps: 0 })
    expect(calls).toHaveLength(1)
  })
  it('maps 127 to not-authorised', async () => {
    const { runner } = fake([ok, { exitCode: 127, stderr: '' }])
    expect(await runPlan(prepareSetup('/opt/app/launcher'), runner)).toEqual({ status: 'not-authorised', completedSteps: 1 })
  })
  it('reports a missing pkexec', async () => {
    const { runner } = fake([{ exitCode: null, stderr: '', spawnError: 'ENOENT' }])
    expect(await runPlan(prepareRemove(), runner)).toEqual({ status: 'pkexec-missing', completedSteps: 0 })
  })
  it('reports another spawn error as failed', async () => {
    const { runner } = fake([{ exitCode: null, stderr: '', spawnError: 'EACCES' }])
    expect((await runPlan(prepareRemove(), runner)).status).toBe('failed')
  })
  it('reports a non-zero exit with the last stderr lines', async () => {
    const { runner } = fake([ok, { exitCode: 1, stderr: 'a\nb\nc\nd\n' }])
    const r = await runPlan(prepareSetup('/opt/app/launcher'), runner)
    expect(r).toEqual({ status: 'failed', completedSteps: 1, detail: 'b\nc\nd' })
  })
  it('reports an exit with no stderr using the step name', async () => {
    const { runner } = fake([{ exitCode: 3, stderr: '' }])
    const r = await runPlan(prepareRemove(), runner)
    expect(r.detail).toContain('Unload the profile')
  })
  it('turns a thrown runner error into failed', async () => {
    const { runner } = fake([new Error('boom')])
    expect(await runPlan(prepareRemove(), runner)).toEqual({ status: 'failed', completedSteps: 0, detail: 'boom' })
  })
  it('refuses a step that does not start with pkexec', async () => {
    const { runner, calls } = fake([])
    const plan = { ...prepareRemove(), steps: [{ description: 'x', argv: ['rm', '-rf', '/'] }] }
    expect((await runPlan(plan, runner)).status).toBe('failed')
    expect(calls).toHaveLength(0)
  })
})

// Uses the Node binary itself, never pkexec.
describe('spawnRunner', () => {
  it('runs an argv without a shell and feeds stdin', async () => {
    const r = await spawnRunner([process.execPath, '-e', 'process.stdin.pipe(process.stderr)'], 'hello; echo no')
    expect(r).toEqual({ exitCode: 0, stderr: 'hello; echo no' })
  })
  it('reports ENOENT for a missing program', async () => {
    const r = await spawnRunner(['/nonexistent/program-xyz'])
    expect(r.spawnError).toBe('ENOENT')
  })
})
