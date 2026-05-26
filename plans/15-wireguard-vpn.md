# Plan 15 — WireGuard VPN Support

## Objective

Add WireGuard VPN support to enable secure, authenticated operator access to desktops. WireGuard tools are installed in the base AMI; the server is configured at first boot via cloud-init with a per-desktop private key stored in AWS SSM Parameter Store. Operator/peer configurations are managed through CLI commands and persisted in `config.yaml`. When enabled, security groups restrict SSH/HTTP/HTTPS to the WireGuard tunnel network only, replacing public operator CIDR access.

**Pre-WireGuard baseline**: By default (WireGuard disabled), `desktop.operator_cidr` defaults to `0.0.0.0/0`, meaning SSH port 22 is open to the internet. Operators may restrict it to their own IP in `config.yaml`. When WireGuard is enabled, this field is ignored and the SG restricts SSH to the WireGuard subnet.

## Scope

- Add `WireGuardConfig` struct to config system with global settings (enabled, server port, subnet) and peer list
- Add `Save()` method to config system for persisting WireGuard peer changes
- Implement `internal/wireguard/` package for key generation (Curve25519), config rendering, and QR code output
- Add `wireguard` CLI command group: `init`, `add-peer`, `remove-peer`, `list-peers`, `show-config`
- Update cloud-init template to configure WireGuard server at first boot when enabled
- Update Pulumi foundation stack security group rules to conditionally open UDP 51820 and restrict other ports to WireGuard subnet
- Implement per-desktop WireGuard server private key generation and storage in AWS SSM Parameter Store
- Add WireGuard health checks to the `doctor` command
- Update `init_foundation`, `create`, and `terminate` commands to integrate with WireGuard lifecycle

## Depends on

- Plan 14 (pre-baked AMI, which installs `wireguard-tools`)
- Plan 01–13 (foundation config, Pulumi, CLI baseline)

## Acceptance Criteria

1. `ai-desktops wireguard init` sets up WireGuard configuration in `config.yaml` with sane defaults.
2. `ai-desktops init-foundation` with WireGuard enabled creates a security group that opens UDP 51820 and restricts SSH/HTTP/HTTPS to the WireGuard subnet.
3. `ai-desktops create` with WireGuard enabled generates a server keypair, stores the private key in SSM, and configures the server at first boot.
4. `ai-desktops wireguard add-peer <name>` generates a peer keypair, adds the public key to config, outputs the client config + QR code, and prints "peer private key will not be shown again".
5. `ai-desktops wireguard add-peer --desktop <id>` on a running desktop SSHes in and applies the peer live (without reboot).
6. `ai-desktops wireguard remove-peer <name>` removes the peer from config and (if `--desktop <id>`) from a live desktop.
7. `ai-desktops wireguard list-peers` displays a table of peer names, allowed IPs, and public keys (truncated).
8. `ai-desktops wireguard show-config <name> --desktop <id> --peer-private-key <key>` renders and displays the client config and QR code.
9. Desktop health check (`doctor`) includes WireGuard status verification when enabled.
10. `ai-desktops terminate` cleans up the server private key from SSM.
11. Operator can connect to the desktop via WireGuard from iOS (via QR), macOS, Linux, and Windows clients.
12. All operator access routes through the WireGuard tunnel; public security group rules are restricted or absent when WireGuard is enabled.

## Verification Steps

```bash
# Enable WireGuard
ai-desktops wireguard init
# → config.yaml updated with wireguard.enabled: true, subnet: 10.99.0.0/24

# Update security groups
ai-desktops init-foundation --preview
# → verify SG has UDP 51820 ingress, SSH restricted to 10.99.0.0/24

ai-desktops init-foundation

# Add a peer
ai-desktops wireguard add-peer myipad
# → outputs client config and QR code

# Create a desktop with WireGuard
ai-desktops create --github-owner myorg --repo myrepo --preview
# → verify cloud-init includes WireGuard server setup

ai-desktops create --github-owner myorg --repo myrepo

# Verify desktop is reachable via WireGuard
# (requires manual WireGuard client setup on operator machine)
ai-desktops ssh d-abc123  # through VPN tunnel

# Add another peer to running desktop
ai-desktops wireguard add-peer myphone --desktop d-abc123

# List peers
ai-desktops wireguard list-peers

# Health check includes WireGuard
ai-desktops doctor d-abc123
# → wg-active, wg-interface checks pass

# Terminate (clean up key)
ai-desktops terminate d-abc123
```

## Key Decisions

### WireGuard server per desktop

Each desktop has its own WireGuard server (not a shared bastion). This provides isolation: compromise of one desktop's WireGuard does not expose others. The trade-off is one SSM parameter per desktop for the server private key (minor overhead).

### Server private key in SSM, peer private keys local-only

WireGuard server private key is generated at create time and stored in SSM SecureString, never in config files or logs. Peer (operator) private keys are generated on-demand by the CLI and printed once; the operator must save them. This matches WireGuard's zero-knowledge design: `ai-desktops` never holds operator keys. If an operator loses their key, they must re-run `add-peer` to generate a new one.

### Peer management via config.yaml

Peer public keys and names are stored in `config.yaml` so they survive CLI restarts and are shared across the operator's machines (if `config.yaml` is synced). The config file is the single source of truth for who is authorized to access the WireGuard server. Peer private keys are not stored; they live on the operator's device.

### WireGuard network separate from operator CIDR

The WireGuard subnet (e.g., `10.99.0.0/24`) is independent of the legacy `operator_cidr`. When WireGuard is enabled, security group rules use the WireGuard subnet, not the operator CIDR. The `operator_cidr` becomes optional in this mode. This allows the operator to access the desktop from anywhere (as long as they have the WireGuard client and a peer key), not just from a static CIDR.

### Re-run `init-foundation` for SG changes

Updating the security group to restrict access when WireGuard is enabled requires re-running `init-foundation`. This is a deliberate workflow because SG changes during a create would leave the operator unable to reach the new desktop (no VPN tunnel yet). The documented order is: (1) enable WireGuard in config, (2) add peers, (3) re-run `init-foundation`, (4) create desktop, (5) connect via VPN. For existing desktops, the SG change applies at the next `init-foundation` run; old desktops remain reachable until stop/start.

### Live peer add/remove via SSH

The `wg` command supports adding/removing peers without restarting. When `--desktop <id>` is specified, `add-peer` and `remove-peer` SSH into the desktop and apply the change live. This requires SSH key access to the desktop (which the operator already has). The peer list in `/etc/wireguard/wg0.conf` is also updated for persistence across reboots.

### QR code for mobile clients

Each peer config can be rendered as a QR code (via `github.com/skip2/go-qrcode`) so iOS users can easily import the config. The QR code is printed to the terminal; the operator scans it with their phone's WireGuard app.

## Trade-offs

| Trade-off | Rationale |
|---|---|
| Per-desktop WireGuard server vs. shared bastion | Per-desktop is simpler to deploy and isolate. Bastion topology can be added later for shared operator access without desktop-specific keys. |
| Peer private keys printed once, not stored | Matches WireGuard's design philosophy and improves security. Trade-off: operator must save the key or re-add the peer if lost. |
| WireGuard subnet hardcoded in config, not dynamic | Simplicity and predictability. Future: allow dynamic subnet selection per operator or environment. |
| OperatorCIDR optional when WireGuard enabled | Cleaner than supporting both simultaneously. If both are set, SG rules still use WireGuard subnet. |
| SSM Parameter Store for server key, not local file | Secure remote storage, integrates with AWS ecosystem, no local key material on the operator machine. Trade-off: requires AWS API calls to retrieve the key at boot. |

## Testing Approach

**Unit tests:**
- `internal/wireguard/wireguard_test.go`: test key generation (verify Curve25519 keys are 32 bytes, base64 encoded), public key derivation, server/client config template rendering, IP allocation (next IP, skip server IP .1, handle full subnet)
- `internal/config/config_test.go`: test WireGuard config validation, `Save()`/load roundtrip, OperatorCIDR optional when WireGuard enabled
- `internal/awsx/awsx_test.go`: mock SSM calls for `PutSecureParameter`, `DeleteParameter`

**Integration (manual):**
- Run `wireguard init` and verify config.yaml is updated
- Run `init-foundation` with WireGuard enabled and verify security group has UDP 51820 and restricted SSH
- Run `create` with WireGuard enabled and verify SSM parameter exists
- SSH into a desktop and verify WireGuard interface is up (`wg show`)
- Run `wireguard add-peer` and verify peer is added to config and appears in `/etc/wireguard/wg0.conf`
- Run `wireguard add-peer --desktop <id>` and verify peer is added live without reboot
- Run `doctor` and verify WireGuard checks pass
- Manually connect a WireGuard client and verify tunnel works, SSH/noVNC accessible through tunnel
- Verify SSH/noVNC are not accessible outside the tunnel

## Out of Scope (for follow-up)

- Multi-desktop WireGuard mesh (peer-to-peer desktop connectivity)
- Separate WireGuard bastion host (shared server for multiple desktops)
- Automatic DNS routing through the VPN
- WireGuard server monitoring and alerts
- Peer revocation without regeneration
- Mobile app push notifications on connection loss
- Integration with identity providers (OIDC, LDAP) for peer authorization
