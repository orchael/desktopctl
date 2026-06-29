# Plan 18 — VPN Gateway Server

## Objective

Replace the per-desktop WireGuard server model with a centrally-managed VPN gateway so
that operator devices (workstations, iPads, phones) configure WireGuard once and can
reach any desktop — current or future — without rescanning QR codes or updating configs.

A lightweight API server (`ai-desktops-server`) handles desktop provisioning, peer
management, and gateway configuration. The existing CLI becomes a thin client that talks
to the server rather than calling AWS directly.

## Problem with the current model

Each desktop runs its own WireGuard server. Client configs (iPad, workstation) bake in
the desktop's public key and endpoint. A second desktop requires a second tunnel on every
client device. This scales linearly with the number of desktops and breaks the "scan
once" promise for mobile clients.

## Target architecture

```
iPad / workstation
     │  WireGuard (one tunnel, one QR scan ever)
     ▼
┌─────────────────────────┐
│   VPN Gateway (EC2)     │  ← persistent, stable IP + DNS
│   wg0: 10.99.0.1/16    │
│   ai-desktops-server    │  ← REST API + provisioning engine
└────────┬────────────────┘
         │  WireGuard (peer-to-gateway, auto-configured at boot)
    ┌────┴────┐
    │ d-001   │  10.99.1.1
    │ d-002   │  10.99.2.1
    │ d-003   │  10.99.3.1
    └─────────┘
```

- **Gateway**: single persistent EC2 instance with a stable Elastic IP and DNS name
  (`gateway.desktops.orchael.dev`). All clients connect here once.
- **Desktops**: configured at boot to connect back to the gateway as WireGuard peers.
  SSH/noVNC are accessible only via VPN addresses.
- **Server**: runs on the gateway, exposes a REST API for provisioning, peer management,
  and status. The existing CLI talks to the server; the desktop web app does too.

## Scope

### Gateway infrastructure (Pulumi)

- New Pulumi stack `infra/pulumi/gateway/` provisioning:
  - Persistent EC2 instance (t3.small) in the existing VPC
  - Elastic IP attached to the instance
  - Route53 A record: `gateway.desktops.orchael.dev`
  - Security group: UDP 51820 (WireGuard) open, TCP 8443 (API) restricted to operator
    CIDR or auth token
  - IAM instance profile with SSM read/write for peer and desktop key management
- Packer AMI for the gateway: Ubuntu + WireGuard + ai-desktops-server binary

### WireGuard gateway (`internal/wireguard/gateway/`)

- Gateway runs a single WireGuard interface (`wg0`) with a `/16` subnet
  (`10.99.0.0/16`): gateway at `.0.1`, operator peers in `10.99.0.0/24`, desktops in
  `10.99.1.0/24` onwards
- Peer registration API: add/remove operator peers and desktop peers dynamically via
  `wg set` (no restart needed)
- Gateway private key stored in SSM at `/ai-desktops/gateway/wireguard/key`; public key
  returned via API so desktop and client configs can be generated server-side

### Desktop changes

- Desktop WireGuard server removed; desktop is now a WireGuard **client** connecting
  back to the gateway
- Cloud-init: fetch gateway public key + endpoint from the server API; write client
  config and bring up WireGuard at boot
- Desktop VPN address allocated by the server at provision time; stored in the fleet
  store alongside hostname

### API server (`cmd/ai-desktops-server/`)

Go HTTP server (stdlib `net/http`) running on the gateway at port 8443 (TLS).

**Endpoints (v1):**

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/v1/desktops` | Provision a new desktop |
| `GET`  | `/v1/desktops` | List desktops |
| `GET`  | `/v1/desktops/{id}` | Get desktop status |
| `POST` | `/v1/desktops/{id}/start` | Start hibernated desktop |
| `POST` | `/v1/desktops/{id}/stop` | Hibernate desktop |
| `DELETE` | `/v1/desktops/{id}` | Terminate desktop |
| `POST` | `/v1/peers` | Register operator peer (returns client config + QR) |
| `DELETE` | `/v1/peers/{name}` | Remove operator peer |
| `GET`  | `/v1/peers` | List operator peers |
| `GET`  | `/v1/gateway/pubkey` | Gateway WireGuard public key |

Auth: bearer token stored in SSM, passed in `Authorization: Bearer <token>` header.

### CLI changes

- Add `server` config block: `url`, `token` (or path to token file)
- CLI commands proxy to the server API instead of calling AWS directly when a server URL
  is configured; fall back to direct AWS mode when no server is configured (for
  bootstrapping and local dev)
- New command: `ai-desktops server status` — health check the running server

### Desktop web app

- `apps/desktop-web` connects to the server API for desktop listing and lifecycle
  actions; replaces the current stub with a real provisioning UI

## Depends on

- Plan 15 (WireGuard VPN — SSM peer storage, stable server keys)
- Plans 01–14 (foundation, fleet store, Pulumi, AMI)

## Acceptance Criteria

1. Operator scans one QR code (or runs `add-peer --apply` once) and can reach every
   desktop — current and future — without any additional config changes.
2. `ai-desktops create d-002` provisions a second desktop; both iPad and do-dev2 can
   SSH/noVNC to it immediately with no client-side changes.
3. `ai-desktops-server` passes a `/healthz` check and all API endpoints return correct
   status codes.
4. Destroying and recreating a desktop preserves operator peer configs; the VPN address
   for that desktop ID is stable.
5. The CLI falls back cleanly to direct AWS mode when no server URL is configured.
6. Gateway EC2 instance is provisioned entirely by Pulumi (`init-foundation`); no manual
   setup steps.

## Out of scope

- Multi-user auth (single operator token is sufficient for v1)
- Desktop web app UI build-out (tracked separately in Plan 12/16)
- IPv6
