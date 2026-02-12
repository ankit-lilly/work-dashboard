## What

A little dashboard thing for me to view jobs running across different environments without going through the 
hassle of logging into different AWS accounts. You can't log into two different aws accounts at the same time.

So this makes it convenient for me to monitor and debug things.



## How does it work

It uses Go on the backend and Datastar for updating the UI via SSE.

You need aws sso profiles for it work. You can take a look at .env.example to see what environment variables are expected.


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


