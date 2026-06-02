# Plan 16 — desktop-web Welcome Page at Root URL

## Objective

Serve the `apps/desktop-web` Vite app as the welcome page at the root HTTPS URL of every desktop
(`https://<hostname>:8443/`). Move noVNC to `/novnc/`, add a real `/api/desktop` backend that
reads runtime state from the desktop, and bake the built static assets into the Packer AMI so
nothing is compiled at boot time. Fix the incorrect `novnc_url` reported by `create`.

## Depends On

- Plan 12 (Vite desktop-web scaffold — existing)
- Plan 14 (Packer AMI — existing)

## Scope

### 1 — Fix `NoVNCURL` (one line)

`internal/desktop/desktop.go:47` — change:

```go
return "https://" + hostname + ":8443/novnc"
```

to:

```go
return "https://" + hostname + ":8443/novnc/vnc.html"
```

Update the corresponding test expectation in `internal/desktop/desktop_test.go`.

### 2 — Real `/api/desktop` backend

Replace the mock `api-server.js` with a real Node.js server that:

- Reads `/opt/ai-desktops/desktop.env` for `DESKTOP_ID`, `GITHUB_OWNER`, `WORKSPACE`,
  `ENVIRONMENT`, `BRIDGE_PORT`
- Scans `$WORKSPACE` for git directories to populate `repos`
- Queries `systemctl is-active` for `docker`, `ai-agent-bridge`, `novnc-desktop` to populate
  `services`
- Derives `hostname` from `os.hostname()` or env
- Constructs `novnc_url` as `https://<hostname>:8443/novnc/vnc.html`
- Serves `GET /api/desktop` on `127.0.0.1:3001`

Keep the mock data in a separate `api-server.mock.js` file for local development
(`pnpm run dev:mock`).

Add a systemd unit `ai-desktops-web.service` that runs the backend as `ubuntu`, starts after
`network.target`, and restarts on failure.

### 3 — Nginx reconfiguration in `ai-desktops-setup-tls`

Update `packer/files/ai-desktops-setup-tls` to produce this routing layout on port 8443:

| Path | Serves |
|---|---|
| `/` | Static files from `/opt/ai-desktops/web/dist/` |
| `/novnc/` | Proxy to noVNC internal service (currently at root) |
| `/api/` | Proxy to `127.0.0.1:3001` (desktop-web backend) |
| `/_auth/` | Existing novnc-auth integration (unchanged) |

The Python patch block that currently inserts `/app/` becomes a block that:
1. Adds a root `location /` serving static files with `try_files $uri $uri/ /index.html`
2. Adds `location /novnc/` proxying to the noVNC WebSocket service
3. Adds `location /api/` proxying to `127.0.0.1:3001`

The existing noVNC root proxy location remains but is renamed to `/novnc/`. The noVNC service
internally serves at a path that nginx maps to `/novnc/` — confirm the correct
`proxy_pass` target against the novnc-desktop nginx config (likely `http://127.0.0.1:8080/`
with rewrite, or directly via WebSocket).

### 4 — Bake desktop-web into the Packer AMI

Add a Packer `shell` provisioner (in `packer/ubuntu-desktop.pkr.hcl`) that:

1. Installs Node.js 22 LTS via NodeSource
2. Installs pnpm via corepack
3. Copies `apps/desktop-web/` to a temp location on the build instance
4. Runs `pnpm install --frozen-lockfile && pnpm run build`
5. Copies `dist/` to `/opt/ai-desktops/web/dist/` with `ubuntu:ubuntu` ownership
6. Copies `api-server.js` to `/opt/ai-desktops/web/api-server.js`
7. Copies `ai-desktops-web.service` to `/etc/systemd/system/`
8. Runs `systemctl enable ai-desktops-web`
9. Removes Node.js and pnpm from the AMI (not needed at runtime since the backend
   is a plain Node script and Node is not required — switch to Python if preferred)

**Alternative:** Write the `api-server.js` backend as a self-contained Python script
(`api-server.py`) using only stdlib (`http.server`, `subprocess`, `json`) to avoid the
Node.js runtime dependency entirely. This is the preferred approach — Python is already
present in the AMI from certbot.

### 5 — Update `apps/desktop-web` package.json scripts

Add:
- `dev:mock` — runs Vite dev server proxying to `api-server.mock.js`
- `build` — already exists; confirm it outputs to `dist/`

Update `vite.config.ts` so the dev server proxies `/api/` to `localhost:3001` (or the mock
server port).

## Files Changed

| File | Change |
|---|---|
| `internal/desktop/desktop.go` | Fix `NoVNCURL` return value |
| `internal/desktop/desktop_test.go` | Update expected URL |
| `apps/desktop-web/api-server.js` | Replace mock with real implementation (or rename to `api-server.mock.js` and write real one) |
| `apps/desktop-web/package.json` | Add `dev:mock` script |
| `apps/desktop-web/vite.config.ts` | Add `/api/` proxy for local dev |
| `packer/files/ai-desktops-setup-tls` | New nginx layout (root static, `/novnc/` proxy, `/api/` proxy) |
| `packer/files/ai-desktops-web.service` | New systemd unit |
| `packer/ubuntu-desktop.pkr.hcl` | Add desktop-web build provisioner |

## Acceptance Criteria

1. `ai-desktops create --json` returns `novnc_url` ending in `/novnc/vnc.html`.
2. HTTPS GET to `https://<hostname>:8443/` returns the desktop-web HTML (not noVNC).
3. HTTPS GET to `https://<hostname>:8443/novnc/vnc.html` opens the noVNC desktop.
4. `GET /api/desktop` returns correct `desktop_id`, `hostname`, `github_owner`, `repos`, and
   `services` from the live desktop state.
5. `systemctl is-active ai-desktops-web` returns `active` on a provisioned desktop.
6. `pnpm run build` in `apps/desktop-web/` succeeds with no errors.
7. The built `dist/` is present at `/opt/ai-desktops/web/dist/` in the AMI.
8. `pnpm run dev:mock` starts the dev server with mock data; the app loads in a local browser.
9. No provider credentials or PAT values appear in the rendered browser output.
10. Existing Go unit tests still pass (`go test ./...`).

## Verification

```bash
# Unit test
go test ./internal/desktop/...

# Desktop-web local build
cd apps/desktop-web
pnpm install
pnpm run build          # verify dist/ output
pnpm run dev:mock       # verify app loads at localhost:5173 with mock data

# After AMI rebuild and desktop creation:
ai-desktops create --github-owner <owner> --json | jq .novnc_url
# → "https://<hostname>:8443/novnc/vnc.html"

curl -k https://<hostname>:8443/                   # welcome page HTML
curl -k https://<hostname>:8443/api/desktop        # JSON desktop info
curl -k https://<hostname>:8443/novnc/vnc.html     # noVNC page
ssh ubuntu@<hostname> systemctl is-active ai-desktops-web
```

## Key Decisions

### Python backend over Node.js

The desktop-web API backend (`/api/desktop`) should be a self-contained Python script using
only stdlib. Python is already present in the AMI from certbot. Using Python avoids installing
Node.js at runtime and keeps the AMI smaller. The Vite front end remains TypeScript/React;
only the production API backend changes language.

### Static files served by nginx, not a Node server

Nginx already runs on the desktop. Serving the built `dist/` via nginx `root` is simpler and
more reliable than adding a separate static-file HTTP server. The Python backend only handles
`/api/desktop`; everything else is nginx serving files from disk.

### noVNC moved from root to `/novnc/`

The root URL is more valuable as a welcome/status page than as a direct noVNC launch. Operators
landing on the root URL get an overview before clicking through to the desktop. The noVNC URL
stored in DynamoDB and returned by `create` is updated to reflect the new path.

### AMI rebuild required

These changes require a new Packer AMI build. Existing desktops provisioned with the old AMI
will not have the welcome page. Document this in the AMI rebuild notes — operators must run
`ai-desktops ami build` and reprovision, or run the nginx/service changes manually over SSH.

## Out of Scope

- Authentication on the welcome page (deferred to Plan SR-4 / future user access work)
- Live-updating the dashboard without page refresh
- Desktop-web showing agent session state or active tasks
- Serving desktop-web over port 443 (desktop uses 8443; this is unchanged)
