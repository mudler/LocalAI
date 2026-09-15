# Distributed binary conformance baseline

Date: 2026-09-15

The comparison used a detached temporary worktree at
`origin/master` commit `15de88d36179d9db3a4346ac3b6dacd0e83842bf`. The worktree was
removed after the run. The main checkout and all user branches were left
untouched.

Only the final path-level diff under `tests/e2e/mock-backend` was applied from
the pull-request branch. This includes the deterministic fixture implementation,
its extended protocol cases, and the package-consistent Ginkgo contract suite;
no production fix from the pull request was applied to the baseline.

The maximal compatible baseline passed:

```text
make protogen-go
go test ./tests/e2e/mock-backend -count=1
ok github.com/mudler/LocalAI/tests/e2e/mock-backend 0.014s

go test -race ./tests/e2e/mock-backend -count=1
ok github.com/mudler/LocalAI/tests/e2e/mock-backend 1.042s

make build-mock-backend
PASS
```

This proves that the deterministic PNG, WAV, video, GLB, nested export, and
quantization fixtures are compatible with current master. It also provides a
local-mode reference for their exact bytes and input-digest behavior.

The compiled distributed feature and authentication scenarios are branch-only
and were intentionally excluded from the master claim. Current master does not
contain `tests/e2e/distributed/cluster`, the binary cluster harness, worker
tunnels, peer relay, machine registration/tunnel credentials, or the staging
control routes those scenarios exercise. Copying those tests alone therefore
cannot compile, and copying their production dependencies would stop being a
master baseline. Their authoritative result is the pull-request branch's real
binary run; the fixture contract above is the common comparison boundary.
