#!/bin/bash
# Verify certbot DNS-01 setup with Route53
# Run this on a deployed desktop: ssh user@desktop-hostname < verify-certbot-setup.sh

set -e

HOSTNAME="${1:-$(hostname -f)}"
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo "=================================================="
echo "Certbot DNS-01 Setup Verification"
echo "Hostname: $HOSTNAME"
echo "=================================================="
echo

# Test 1: Certificate file exists
echo "[1/7] Checking certificate files..."
CERT_PATH="/etc/letsencrypt/live/$HOSTNAME"
if [ -d "$CERT_PATH" ]; then
  echo -e "${GREEN}✓${NC} Certificate directory exists: $CERT_PATH"
  if [ -f "$CERT_PATH/fullchain.pem" ] && [ -f "$CERT_PATH/privkey.pem" ]; then
    echo -e "${GREEN}✓${NC} Certificate and key files present"
  else
    echo -e "${RED}✗${NC} Missing fullchain.pem or privkey.pem"
    exit 1
  fi
else
  echo -e "${RED}✗${NC} Certificate directory not found: $CERT_PATH"
  exit 1
fi
echo

# Test 2: Certificate validity
echo "[2/7] Checking certificate validity..."
EXPIRY=$(sudo openssl x509 -in "$CERT_PATH/fullchain.pem" -noout -enddate | cut -d= -f2)
EXPIRY_EPOCH=$(sudo date -d "$EXPIRY" +%s)
NOW_EPOCH=$(date +%s)
DAYS_LEFT=$(( (EXPIRY_EPOCH - NOW_EPOCH) / 86400 ))

if [ $DAYS_LEFT -gt 0 ]; then
  echo -e "${GREEN}✓${NC} Certificate is valid, expires in $DAYS_LEFT days ($EXPIRY)"
else
  echo -e "${RED}✗${NC} Certificate is expired! Expired: $EXPIRY"
  exit 1
fi
echo

# Test 3: Certificate matches hostname
echo "[3/7] Checking certificate hostname..."
CERT_CN=$(sudo openssl x509 -in "$CERT_PATH/fullchain.pem" -noout -subject | grep -oP '(?<=CN=)[^,/]+')
CERT_SAN=$(sudo openssl x509 -in "$CERT_PATH/fullchain.pem" -noout -text | grep -A1 "Subject Alternative Name" | tail -1 | grep -oP '(?<=DNS:)[^,]+' | head -1)

if [[ "$CERT_CN" == "$HOSTNAME" ]] || [[ "$CERT_SAN" == "$HOSTNAME" ]]; then
  echo -e "${GREEN}✓${NC} Certificate CN/SAN matches hostname"
  echo "  CN: $CERT_CN"
  echo "  SAN: $CERT_SAN"
else
  echo -e "${YELLOW}!${NC} Certificate CN ($CERT_CN) or SAN ($CERT_SAN) may not match $HOSTNAME"
fi
echo

# Test 4: Certbot is installed and working
echo "[4/7] Checking certbot installation..."
if command -v certbot &> /dev/null; then
  CERTBOT_VERSION=$(certbot --version 2>/dev/null | awk '{print $2}')
  echo -e "${GREEN}✓${NC} Certbot installed (v$CERTBOT_VERSION)"
else
  echo -e "${RED}✗${NC} Certbot not found in PATH"
  exit 1
fi
echo

# Test 5: Certbot timer is enabled
echo "[5/7] Checking certbot auto-renewal..."
if sudo systemctl is-enabled certbot.timer &> /dev/null; then
  echo -e "${GREEN}✓${NC} certbot.timer is enabled"
else
  echo -e "${YELLOW}!${NC} certbot.timer is not enabled"
fi

if sudo systemctl is-active certbot.timer &> /dev/null; then
  echo -e "${GREEN}✓${NC} certbot.timer is active"
  NEXT_RUN=$(sudo systemctl list-timers certbot.timer 2>/dev/null | grep certbot.timer | awk '{print $1, $2}' || echo "unknown")
  echo "  Next renewal: $NEXT_RUN"
else
  echo -e "${YELLOW}!${NC} certbot.timer is not active (may be waiting for next trigger)"
fi
echo

# Test 6: HTTPS port is responding
echo "[6/7] Checking HTTPS connectivity..."
if timeout 5 openssl s_client -connect localhost:8443 -servername "$HOSTNAME" </dev/null 2>/dev/null | grep -q "Verify return code"; then
  echo -e "${GREEN}✓${NC} HTTPS port 8443 is responding"

  # Extract certificate info from the connection
  REMOTE_CN=$(echo | openssl s_client -connect localhost:8443 -servername "$HOSTNAME" 2>/dev/null | openssl x509 -noout -subject 2>/dev/null | grep -oP '(?<=CN=)[^,/]+' || echo "unknown")
  echo "  Remote cert CN: $REMOTE_CN"
else
  echo -e "${YELLOW}!${NC} Could not connect to HTTPS port 8443 (check if novnc-desktop is running)"
fi
echo

# Test 7: Port 80 is closed (not needed for DNS-01)
echo "[7/7] Checking port 80 (should be closed)..."
if timeout 2 bash -c 'echo > /dev/tcp/localhost/80' 2>/dev/null; then
  echo -e "${YELLOW}!${NC} Port 80 is open (not needed for DNS-01, but not harmful)"
else
  echo -e "${GREEN}✓${NC} Port 80 is closed (as expected for DNS-01)"
fi
echo

# Summary
echo "=================================================="
echo "Verification complete!"
echo "=================================================="
echo
echo "Next steps:"
echo "  1. Check renewal logs: sudo journalctl -u certbot.service -n 20"
echo "  2. Test renewal (dry-run): sudo certbot renew --dry-run"
echo "  3. Check certificate details: openssl x509 -in $CERT_PATH/fullchain.pem -noout -text"
echo
