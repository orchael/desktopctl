# WireGuard VPN

WireGuard is the recommended way to access ai-desktops. When enabled, SSH and
noVNC are restricted to the VPN subnet so desktops are not exposed to the
internet. UDP port 51820 (configurable) is opened on the desktop security group
for WireGuard handshakes.

Each desktop runs its own WireGuard server (`wg-aidesktops`) with IP
`10.99.0.1`. Operator devices are peers with addresses from `10.99.0.2`
onward. The VPN subnet (`10.99.0.0/24`) is separate from any other WireGuard
interfaces you may already be running.

## Setup (one time)

### 1. Enable WireGuard in config

```bash
ai-desktops wireguard init
```

This writes WireGuard defaults into `~/.ai-desktops/config.yaml`:

```yaml
wireguard:
  enabled: true
  port: 51820
  subnet: 10.99.0.0/24
  interface: wg-aidesktops
```

### 2. Add your device as a peer

```bash
ai-desktops wireguard add-peer laptop
```

This generates a keypair, stores the public key in `config.yaml`, and saves
the private key to `~/.ai-desktops/peers/laptop.key` (mode 0600). You do not
need to copy or save anything manually.

Repeat for each device you want to connect:

```bash
ai-desktops wireguard add-peer phone
```

### 3. Update foundation infrastructure

```bash
ai-desktops init-foundation
```

This applies the updated security group rules so the WireGuard port is
reachable from anywhere (handshakes are authenticated by the keypairs).

### 4. Create a desktop

```bash
ai-desktops create --name mydesktop
```

New desktops automatically get a WireGuard server installed and configured at
first boot. The server private key is stored in AWS SSM Parameter Store at
`/ai-desktops/<id>/wireguard/server-key` and never leaves AWS.

## Connecting to a desktop

### Get the client config

```bash
ai-desktops wireguard show-config laptop --desktop d-<id>
```

This retrieves the server public key from SSM, renders a complete WireGuard
client config, and displays it as a QR code (for mobile) and as text.

Example output:

```
Client config for "laptop" (desktop d-abc123):
[Interface]
PrivateKey = <your-private-key>
Address = 10.99.0.2/32

[Peer]
PublicKey = <server-public-key>
Endpoint = <desktop-hostname>:51820
AllowedIPs = 10.99.0.0/24
PersistentKeepalive = 25
```

### Import into WireGuard

**macOS / Linux**: Copy the config into `/etc/wireguard/wg-aidesktops.conf`
(or use the WireGuard GUI app and scan the QR code).

**iOS / Android**: Scan the QR code with the WireGuard mobile app.

### Connect

```bash
sudo wg-quick up wg-aidesktops
```

Once connected, access the desktop at `10.99.0.1` (SSH, noVNC, etc.).

## Adding a peer to a running desktop

If a desktop is already running and you want to add a new peer without
rebooting:

```bash
ai-desktops wireguard add-peer tablet --desktop d-<id>
```

This adds the peer to `config.yaml`, saves the key, applies the peer live
over SSH, and prints the full client config with QR code.

## Managing peers

```bash
# List all peers
ai-desktops wireguard list-peers

# Remove a peer from config
ai-desktops wireguard remove-peer laptop

# Remove a peer from config AND from a live desktop
ai-desktops wireguard remove-peer laptop --desktop d-<id>
```

Removing a peer also deletes `~/.ai-desktops/peers/laptop.key`.

## Key storage

| What | Where |
|------|-------|
| Peer private key | `~/.ai-desktops/peers/<name>.key` (mode 0600, local only) |
| Server private key | AWS SSM Parameter Store `/ai-desktops/<id>/wireguard/server-key` |
| Peer public key | `~/.ai-desktops/config.yaml` under `wireguard.peers` |

The server private key is fetched by cloud-init at first boot and written to
`/etc/wireguard/wg-aidesktops.conf` on the desktop. It is deleted from SSM
when the desktop is terminated.

## AMI requirements

WireGuard must be installed on the AMI used to create desktops. If you built
your AMI before WireGuard support was added, rebuild it with:

```bash
ai-desktops build-ami
```

The Packer build installs `wireguard-tools` and enables the `wg-aidesktops`
systemd service.

## Troubleshooting

**Handshake not completing**: Check that UDP 51820 is open in the desktop
security group. Run `ai-desktops init-foundation` after enabling WireGuard.

**Key not found**: If `show-config` reports "key file not found", the peer was
probably added on a different machine. Move the `.key` file to
`~/.ai-desktops/peers/<name>.key` on the current machine.

**Live apply failed**: If `add-peer --desktop` reports a live apply failure,
the peer is saved to config but not active yet. Reboot the desktop or re-run
`add-peer --desktop <id>` to retry.
