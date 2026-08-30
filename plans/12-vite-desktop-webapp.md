# Task 12 - Vite Desktop Webapp

## Objective

Create the on-desktop Vite webapp and deployment path.

## Depends On

- Task 08

## Scope

Create:

- `apps/desktop-web`
- Vite app scaffold
- build command
- deployment into `/opt/ai-desktops/desktop-web/dist`
- minimal local service or Nginx route for serving static assets

Initial UI should show:

- desktop ID
- hostname
- GitHub owner boundary
- workspace repo list
- Docker status
- `bridgectl` status
- links to noVNC and useful local services

## Requirements

- Use Vite.
- Keep the webapp as a local desktop surface, not the fleet control plane.
- Do not expose secrets in the browser.
- The app should work from static built assets.
- Any local API shim must bind to localhost unless intentionally proxied through authenticated desktop access.

## Acceptance Criteria

- `npm` or `pnpm` build succeeds.
- Built assets are deployable by cloud-init/bootstrap.
- Desktop webapp loads in the browser after provisioning.
- No provider credentials or GitHub token values appear in rendered output.

## Verification

```bash
cd apps/desktop-web
pnpm install
pnpm build
```

If the project chooses npm instead:

```bash
cd apps/desktop-web
npm install
npm run build
```
