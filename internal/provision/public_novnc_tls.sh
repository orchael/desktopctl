#!/bin/bash
# Configure the desktop's own HTTPS endpoint after central DNS is published.
set -euo pipefail
domain="${1:?desktop DNS hostname required}"
email="${2:-admin@orchael.ai}"
[[ "$domain" =~ ^[a-z0-9][a-z0-9.-]*[a-z0-9]$ ]] || exit 2
ufw allow 80/tcp
ufw allow 8443/tcp
metadata_token=$(curl -fsS --max-time 5 -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token)
public_ip=$(curl -fsS --max-time 5 -H "X-aws-ec2-metadata-token: $metadata_token" http://169.254.169.254/latest/meta-data/public-ipv4)
python3 - "$domain" "$public_ip" <<'PY'
import socket,sys,time
name,expected=sys.argv[1:]
for _ in range(120):
    try:
        if expected in socket.gethostbyname_ex(name)[2]: break
    except OSError: pass
    time.sleep(5)
else: raise SystemExit('Desktop DNS did not reach this instance')
PY
certbot certonly --standalone --preferred-challenges http --http-01-port 80 --non-interactive --agree-tos --email "$email" --cert-name "$domain" -d "$domain" --keep-until-expiring
python3 - "$domain" <<'PY'
import json,pathlib,re,sys
name=sys.argv[1]
site=pathlib.Path('/etc/nginx/sites-available/novnc')
s=site.read_text()
s=re.sub(r"(server_name\s+)[^;]+;",lambda m:m[1]+name+";",s)
s=re.sub(r'(ssl_certificate\s+)[^;]+;',lambda m:m[1]+f'/etc/letsencrypt/live/{name}/fullchain.pem;',s)
s=re.sub(r'(ssl_certificate_key\s+)[^;]+;',lambda m:m[1]+f'/etc/letsencrypt/live/{name}/privkey.pem;',s)
# Serve the actual installed static client independently of websockify's web root.
static_location="""location /novnc/ {
    auth_request /_auth/verify;
    error_page 401 = @unauthorized;
    alias /usr/share/novnc/;
    index vnc.html;
  }"""
m=re.search(r'location\s+/novnc/\s*\{',s)
if m:
    start=m.start();end=m.end();depth=1
    while depth and end<len(s):
        if s[end]=='{': depth+=1
        elif s[end]=='}': depth-=1
        end+=1
    if depth: raise SystemExit('Invalid nginx location')
    s=s[:start]+static_location+s[end:]
else:
    # Fresh AMIs proxy / to websockify; retain that protected WebSocket route.
    end=s.rfind('}')
    if end<0: raise SystemExit('Missing nginx server block')
    s=s[:end]+static_location+'\n'+s[end:]
if not re.search(r'location\s*=\s*/vnc[.]html\s*\{',s):
    end=s.rfind('}')
    s=s[:end]+"location = /vnc.html { return 302 /novnc/vnc.html$is_args$args; }\n"+s[end:]
if 'location = /access {\n    access_log off;' not in s:
    s=s.replace('location = /access {','location = /access {\n    access_log off;')
site.write_text(s)
p=pathlib.Path('/etc/novnc-auth/config.json');cfg=json.loads(p.read_text());cfg['base_url']=f'https://{name}:8443';p.write_text(json.dumps(cfg))
PY
install -d -m 755 /etc/letsencrypt/renewal-hooks/deploy
printf '#!/bin/sh\nnginx -t && systemctl reload nginx\n' > /etc/letsencrypt/renewal-hooks/deploy/desktopctl-novnc
chmod 755 /etc/letsencrypt/renewal-hooks/deploy/desktopctl-novnc
nginx -t
systemctl restart novnc-auth
systemctl reload nginx
systemctl enable --now certbot.timer
