## What

A little dashboard thing for me to view jobs running across different environments without
going through the hassle of logging into different AWS accounts.  You can't log into two
different aws accounts at the same time.

I run this on my local machine and use my aws sso profiles to monitor jobs across different
environments from a single UI.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/ankit-lilly/work-dashboard/main/scripts/install.sh | bash
```

Options:

```bash
# Install a specific version
VERSION=v0.2.0 curl -fsSL ... | bash

# Install to a custom directory
INSTALL_DIR=~/.local/bin curl -fsSL ... | bash
```

## Setup

After installing, run the interactive setup wizard to configure your AWS SSO profiles:

```bash
radar setup
```

This reads your `~/.aws/config`, lets you pick which SSO profiles to monitor, assign
environment tiers (dev/qa/prod), and generates the `.env` file automatically.

## Usage

```bash
radar server
```

Open http://localhost:8080 in your browser. The dashboard loads instantly and fills in
data via SSE as it arrives from AWS.

## Development

```bash
# Run locally (formats + tidies first)
make run

# Build optimized binary
make build

# Build + watch CSS
make css-watch
```



## How does it work

It uses Go on the backend and Datastar for updating the UI via SSE.

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

