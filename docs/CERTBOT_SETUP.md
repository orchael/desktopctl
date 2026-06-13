# Certbot DNS-01 Setup with Route53

This document describes how certbot is configured for automatic TLS certificate management via AWS Route53 DNS-01 validation.

## Overview

The desktop stack uses **DNS-01 challenge** with **Route53** to obtain and renew TLS certificates for novnc-desktop. This approach:
- ✅ Requires no open ports (no port 80 needed)
- ✅ Works from any network (no firewall dependencies)
- ✅ Fully automated renewal every 90 days
- ✅ No manual intervention required

## How It Works

### Initial Certificate (Cloud-Init)

When the EC2 instance launches, cloud-init runs:

```bash
# Obtain TLS certificate via Route53 DNS-01 challenge
certbot certonly --dns-route53 --non-interactive --agree-tos --email admin@orchael.ai -d {{ .Hostname }}

# Enable certbot auto-renewal
systemctl enable certbot.timer
systemctl start certbot.timer
```

### Certificate Location

After issuance, the certificate files are stored at:

```
/etc/letsencrypt/live/d-XXXXX.desktops.orchael.dev/
  ├── fullchain.pem    # Certificate chain (used by novnc-desktop)
  ├── privkey.pem      # Private key (used by novnc-desktop)
  ├── cert.pem         # Public certificate
  └── chain.pem        # CA chain
```

### noVNC-Desktop Configuration

The certificate is bound to novnc-desktop on ports 8080 (HTTP) and 8443 (HTTPS):

```bash
# From novnc-desktop installer
--cert-file /etc/letsencrypt/live/{{ .Hostname }}/fullchain.pem \
--key-file /etc/letsencrypt/live/{{ .Hostname }}/privkey.pem
```

### Automatic Renewal

Certbot runs every day via systemd timer. It checks if the certificate expires within 30 days and renews if needed:

```bash
# Systemd timer configuration
/etc/systemd/system/certbot.timer
/etc/systemd/system/certbot.service
```

## Prerequisites

The EC2 instance requires IAM permissions to modify Route53 DNS records:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "route53:GetChange",
        "route53:ListResourceRecordSets",
        "route53:ChangeResourceRecordSets"
      ],
      "Resource": "*"
    }
  ]
}
```

This is typically granted via an **IAM instance profile** attached to the EC2 instance.

## Verification

### Quick Test

Run the verification script against a deployed desktop:

```bash
ssh ubuntu@d-XXXXX.desktops.orchael.dev < ./scripts/verify-certbot-setup.sh
```

This script checks:
1. Certificate files exist at `/etc/letsencrypt/live/`
2. Certificate is valid and not expired
3. Certificate CN/SAN matches the hostname
4. Certbot is installed
5. Certbot timer is enabled and active
6. HTTPS port 8443 is responding with the certificate
7. Port 80 is closed (as expected for DNS-01)

### Manual Checks

**Check certificate details:**
```bash
sudo openssl x509 -in /etc/letsencrypt/live/*/fullchain.pem -noout -text
```

**Check expiration:**
```bash
sudo certbot certificates
```

**Check renewal timer:**
```bash
sudo systemctl status certbot.timer
sudo systemctl list-timers certbot.timer
```

**View renewal logs:**
```bash
sudo journalctl -u certbot.service -n 50
```

**Test renewal (dry-run, no changes):**
```bash
sudo certbot renew --dry-run
```

## Troubleshooting

### Certificate Not Issued (Cloud-Init Failure)

Check the cloud-init logs:
```bash
ssh ubuntu@hostname
tail -100 /var/log/cloud-init-output.log
```

### Route53 Permissions Missing

If certbot fails with "Route53 API error", the EC2 instance profile lacks Route53 permissions. Add the IAM policy above to the instance's role.

### Renewal Failing

Check systemd timer status:
```bash
sudo systemctl status certbot.service
sudo journalctl -u certbot.service
```

Common causes:
- Route53 permissions missing
- Network connectivity issues
- Certbot package outdated

### Certificate Not Used by novnc-desktop

Verify novnc-desktop is using the correct paths:
```bash
# Check novnc-desktop process
ps aux | grep novnc

# Check the config file (if accessible)
sudo cat /path/to/novnc/config
```

If the paths are wrong, reinstall novnc-desktop with the correct certificate paths.

## Renewal Process

Certbot automatically renews certificates 30 days before expiration:

1. Systemd timer triggers `certbot.service` (usually around 3 AM)
2. Certbot checks if renewal is needed
3. If needed, certbot creates a DNS TXT record in Route53
4. Let's Encrypt validates the DNS record
5. New certificate is saved to `/etc/letsencrypt/live/`
6. Certificate symlinks are updated (novnc-desktop reads files directly; no restart needed)
7. DNS record is deleted

**No manual intervention required.** Renewal happens automatically.

## Monitoring

### Certificate Expiration Alert

Set up monitoring to alert before certificate expires:

```bash
# Check days until expiry
sudo openssl x509 -in /etc/letsencrypt/live/*/fullchain.pem -noout -dates
```

### CloudWatch Monitoring (Optional)

Parse logs for renewal failures:
```bash
aws logs tail /var/log/certbot --follow
```

## FAQ

**Q: Can I use HTTP-01 instead?**
A: Yes, but it requires port 80 to always be open. DNS-01 is better for this use case.

**Q: What if DNS-01 validation fails?**
A: The renewal will fail and the certificate won't be renewed. Set up monitoring on the systemd timer or logs.

**Q: Can I manually trigger renewal?**
A: Yes: `sudo certbot renew --force-renewal`

**Q: Do I need to restart novnc-desktop after renewal?**
A: No. novnc-desktop reads the cert files directly from `/etc/letsencrypt/live/`, and symlinks are atomic.

**Q: What's the certificate authority?**
A: Let's Encrypt. Certificates are valid for 90 days.
