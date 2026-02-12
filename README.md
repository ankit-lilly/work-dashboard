# Job Viewer

A lightweight, real-time dashboard for monitoring and debugging AWS Step Functions across multiple environments. The UI is server-rendered with live updates via SSE (Datastar). Static assets are embedded into the binary for easy distribution.

## Setup

1. **AWS Profiles**: Ensure you have AWS CLI profiles configured in `~/.aws/config` for each environment (Dev, QA, Prod).
2. **Environment Variables**: Define your environments and their corresponding profiles.

```bash
export JOB_ENVS="dev:camp-dev,qa:camp-qa,prod:camp-prod"
```

The format is `name:profile:region` (region is optional, defaults to `us-east-2`). Multiple environments are comma-separated.
If you include multiple profiles with the same `name` (e.g., `dev`), the UI will display them as `name:profile` (e.g., `dev:dsoadev`).

3. **Run**:
```bash
go run main.go
```

4. **Access**: Open `http://localhost:8080`

## Build

```bash
make build
```

This compiles a standalone binary with embedded static assets in `bin/work-dashboard`.

## Polling Configuration

You can override polling intervals via environment variables (duration strings):

- `JOB_ACTIVE_POLL` (default `5s`)
- `JOB_FAILURES_POLL` (default `120s`)
- `JOB_STATE_MACHINES_POLL` (default `5m`)

Per-environment active polling override (accepts either `env` or `env:profile` keys):

- `JOB_ACTIVE_POLL_BY_ENV` (default `dev=10s,qa=10s,prod=30s`)

**What’s the difference?**
- **Polling intervals** control how often the backend refreshes each dataset from AWS and broadcasts it over SSE.
- **Per‑env active polling** lets you tune the active execution refresh per environment (e.g., fast for dev, slower for prod).

Example:
```bash
export JOB_ACTIVE_POLL="3s"
export JOB_FAILURES_POLL="90s"
export JOB_STATE_MACHINES_POLL="3m"
export JOB_ACTIVE_POLL_BY_ENV="dev=5s,qa=10s,prod=20s"
```

## Features

- **Unified View**: See Dev, QA, and Prod jobs in one table.
- **Live Pulse**: Active executions update every 5 seconds via SSE.
- **Recent Failures**: Quick access to failed executions.
- **Execution States**: “View States” opens a modal with full state input/output and failure cause.
- **JSON Preview**: “View JSON” opens a modal to preview S3 JSON files (handles stringified JSON fields and NDJSON).
- **Zero JS Apps**: No React/Webpack. Just Go + Datastar.

## UI Notes

- **View States**: shows state-by-state inputs/outputs and failure details.
- **View JSON**: previews S3 output files and highlights JSON structures for easier inspection.

## How It Works

### Rendering Model
- **Server-rendered HTML shell**: Go templates render the page layout and initial content.
- **Live updates via SSE**: Datastar (`static/js/datastar.js`) opens SSE connections and patches HTML into the DOM.
- **No custom JS**: UI actions are defined in HTML using `data-on:*` and `@get(...)` attributes.

### UI Update Flow
1. Page load renders `layout.html` + `index.html`.
2. The root container in `index.html` triggers `@get('/api/dashboard-updates', ...)`.
3. The backend streams HTML fragments and Datastar patches them into specific targets (e.g., `#active-jobs-list`).
4. User actions (search, list outputs, view JSON, view states) call targeted endpoints that return HTML fragments via SSE.

### Endpoints
**HTTP pages**
- `/` – main dashboard page
- `/view/json` – standalone JSON view (optional fallback)

**SSE / API endpoints (Datastar-driven)**
- `/api/dashboard-updates` – live active executions, failures, state machines
- `/api/record-search` – streaming record search results
- `/api/record-search-cancel` – stop an active search
- `/api/state-machine-executions` – fetch execution list for a state machine
- `/api/execution-states` – fetch full state history + input/output for an execution
- `/api/s3-preview-modal` – JSON preview rendered into modal
- `/api/s3-download` – download S3 object
- `/api/s3-search` – search inside JSON payloads in S3
- `/api/s3-prefix-list` – list output files by prefix
- `/api/s3-preview` – legacy inline preview (used by record search)

### Code Structure (High-Level)
- `main.go` – application entry; embeds static assets and starts the HTTP server.
- `internal/server/server.go` – server wiring, templates, broadcasters.
- `internal/server/routes.go` – HTTP routing for all endpoints.
- `internal/server/dashboard.go` – SSE updates for active jobs, failures, state machines.
- `internal/server/state_machines.go` – execution details, S3 interactions, JSON preview utilities.
- `internal/server/search.go` – streaming record search pipeline.
- `internal/server/states_view.go` – execution state history and modal rendering.
- `internal/aws/*` – AWS SDK wrappers for SFN, S3, and logs.

### Datastar Integration
- Uses `datastar-go` to emit HTML patches (`PatchElements`) over SSE.
- HTML defines **targets** with IDs and **actions** with `@get(...)`.
- This keeps UI logic in templates and avoids custom JavaScript.

### Request/Update Flow (Diagram)
```
Browser
  |  GET /
  v
Go server (templates render layout + index)
  |  HTML shell + data-init
  v
Browser (Datastar)
  |  SSE GET /api/dashboard-updates
  |  SSE GET /api/state-machine-executions (on click)
  |  SSE GET /api/execution-states (modal)
  |  SSE GET /api/s3-preview-modal (modal)
  v
Go server (handlers render HTML fragments)
  |  PatchElements -> target IDs
  v
DOM updated in-place (no page reload)
```
