---
title: "Routing logs in embedded applications"
---

Go applications that embed LocalAI can retain the bounded in-memory router
decision log without enabling billing statistics:

```go
config.WithDisableStats(true),
config.WithRouterDecisionLog(true),
```

The explicit routing-log option retains classifier, selected-model, cache and
score diagnostics. It does not create a billing recorder or record token usage.
Without that opt-in, disabling stats still disables both logs as before.

`config.DisableMetricsEndpoint` also prevents failover health-gauge registration;
failover routing itself continues to run.
