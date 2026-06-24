## What

A little dashboard thing for me to view jobs running across different environments without
going through the hassle of logging into different AWS accounts.  You can't log into two
different aws accounts at the same time.

I run this on my local machine and use my aws sso profiles to monitor jobs across different
environments from a single UI.

Does this even work bro?



## How does it work

It uses Go on the backend and Datastar for updating the UI via SSE.

You need aws sso profiles for it work.  You can take a look at .env.example to see what
environment variables are expected.

```
Browser
  |  GET /
  v
Go server (templates render layout + index)
  |  HTML shell + data-init="@get('/api/dashboard-updates')"
  v
Browser (Datastar)
  |  SSE GET /api/dashboard-updates  (single persistent connection)
  v
State Orchestrator (single goroutine)
  |  Fetches all data sources on schedule (5s tick)
  |  Applies changes atomically to DashboardState
  |  Detects what changed via content hashing
  |  Notifies subscriber (the SSE handler)
  v
SSE Handler
  |  Renders only changed sections
  |  PatchSignals + PatchElements → target IDs
  v
DOM updated in-place (no page reload)
```

### On-demand streams (separate from the main dashboard state)

```
Browser (user interaction)
  |  SSE GET /api/state-machine-executions (on click)
  |  SSE GET /api/execution-states (modal)
  |  SSE GET /api/record-search (search)
  |  SSE GET /api/s3-preview-modal (modal)
  v
Go server (per-request handlers, call app services directly)
  |  PatchElements -> target IDs
  v
DOM updated in-place
```

### Architecture overview

See [docs/architecture-flow.md](docs/architecture-flow.md) for the full architecture diagram,
request flows, and responsibility boundaries.

