# dashboard

Web Dashboard for TPT DataCenter (todo.md **Phase 9**).

A self-contained single-page app (no build toolchain — plain HTML/CSS/JS)
plus a thin Go reverse proxy that serves the SPA and fans API calls out to the
backend services so the whole UI runs on a single, CORS-free origin.

## Features

| View | Description |
| --- | --- |
| **Overview** | Live actuator/telemetry cards (valve, fan, outlet, mode) + a power/cooling trend sparkline from the telemetry API. |
| **Thermal** | Per-rack thermal heatmap, color-scaled green→red by temperature. |
| **Topology** | Interactive SVG of the physical topology graph (powered-by / cooled-by relationships) from the topology API. |
| **Control** | Manual control overrides with a safety confirmation; each action is clamped to the actuator envelope and written to the audit log. Includes Reset (return to auto) and Safe (latch into fail-safe). |
| **Alerts** | Active alerts derived from device state (manual override, latched-safe) plus the recent audit trail. A live grid-stress badge shows the current `grid-stress` level. |

The UI reads the API token from the header input and sends it as a Bearer
token. All override endpoints require auth in production (see the control /
orchestrator `-token` flags).

## Run

```bash
# Build and run the dashboard proxy (defaults assume the other services are up):
go run ./cmd/dashboard -addr :8085 \
  -control http://localhost:8082 \
  -hardware http://localhost:8083 \
  -telemetry http://localhost:8080 \
  -topology http://localhost:8081 \
  -orchestrator http://localhost:8084
```

Then open <http://localhost:8085>.

You can also point the SPA directly at separately-hosted APIs by overriding
the base URLs in the page URL, e.g.:

```
http://localhost:8085/?control=http://host:8082&telemetry=http://host:8080
```

## Layout

| Path | Responsibility |
| --- | --- |
| `static/index.html` | SPA shell. |
| `static/app.css` | Dark theme. |
| `static/app.js` | Data fetching, polling, heatmap/topology/control rendering. |
| `cmd/dashboard` | Static file server + reverse proxy. |

## Frontend stack

Chosen for zero-build reproducibility and minimal supply-chain surface:
**vanilla HTML + CSS + ES-module-free JS** with the Canvas and SVG APIs for
visualizations. This keeps the dashboard auditable and trivially buildable in
CI without a bundler. The same API surface is consumed by any future framework
(React/Svelte) if the project later chooses one.
