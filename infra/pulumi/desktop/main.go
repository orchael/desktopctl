package main

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	novncDesktopVersion   = "v0.1.5"
	aiAgentBridgeVersion  = "v0.1.0"
	novncHTTPPort         = 8080
	novncHTTPSPort        = 8443
	defaultBridgePort     = 9445
)

// Ubuntu 22.04 LTS (Jammy) x86_64 — update per region as needed.
// These are official Canonical AMIs.
var ubuntuAMIs = map[string]string{
	"us-east-1":      "ami-0c7217cdde317cfec",
	"us-east-2":      "ami-05fb0b8c1424f266b",
	"us-west-1":      "ami-0ce2cb35386fc22e9",
	"us-west-2":      "ami-008fe2fc65df48dac",
	"eu-west-1":      "ami-0694d931cee176e7d",
	"eu-central-1":   "ami-06dd92ecc74fdfb36",
	"ap-southeast-1": "ami-078c1149d8ad719a7",
	"ap-northeast-1": "ami-0d52744d6551d851e",
}

const cloudInitTmpl = `#cloud-config
package_update: true
package_upgrade: false

packages:
  - git
  - docker.io
  - tmux
  - curl
  - wget
  - unzip
  - ca-certificates
  - apt-transport-https
  - gnupg
  - software-properties-common
  - awscli
  - snapd
  - certbot
  - python3-certbot-dns-route53

runcmd:
  - systemctl enable docker
  - systemctl start docker
  - usermod -aG docker ubuntu
  - snap install nvim --classic
  - mkdir -p /workspace
  - chown ubuntu:ubuntu /workspace
  - mkdir -p /opt/ai-desktops
  - chown ubuntu:ubuntu /opt/ai-desktops

  # Obtain TLS certificate via Route53 DNS-01 challenge (no port 80 required).
  - certbot certonly --dns-route53 --non-interactive --agree-tos --email admin@orchael.ai -d {{ .Hostname }}

  # Enable certbot auto-renewal.
  - systemctl enable certbot.timer
  - systemctl start certbot.timer

  # Install novnc-desktop {{ .NovncVersion }} with custom ports and Let's Encrypt cert.
  - curl -fsSL https://raw.githubusercontent.com/orchael/novnc-desktop/{{ .NovncVersion }}/install.sh | bash -s -- --desktop-type elementary --http-port {{ .HTTPPort }} --https-port {{ .HTTPSPort }} --cert-file /etc/letsencrypt/live/{{ .Hostname }}/fullchain.pem --key-file /etc/letsencrypt/live/{{ .Hostname }}/privkey.pem

  # Install ai-agent-bridge {{ .BridgeVersion }}, bound to localhost only.
  - curl -fsSL https://raw.githubusercontent.com/orchael/ai-agent-bridge/{{ .BridgeVersion }}/install.sh | bash -s -- --bind 127.0.0.1 --port {{ .BridgePort }}
  - systemctl enable ai-agent-bridge
  - systemctl start ai-agent-bridge
  - |
    set -e
    REGION="{{ .Region }}"
    PAT_SECRET="{{ .PATSecret }}"
    WORKSPACE="/workspace"
    OWNER="{{ .GitHubOwner }}"

    # Retrieve PAT from AWS SSM or Secrets Manager.
    PAT=$(aws ssm get-parameter --region "$REGION" --name "$PAT_SECRET" --with-decryption --query Parameter.Value --output text 2>/dev/null || \
          aws secretsmanager get-secret-value --region "$REGION" --secret-id "$PAT_SECRET" --query SecretString --output text)
    if [ -z "$PAT" ]; then
      echo "ERROR: could not retrieve GitHub PAT from $PAT_SECRET" >&2
      exit 1
    fi

    # Write credentials to .netrc so the PAT never appears in process args or git URLs.
    printf 'machine github.com\nlogin x-access-token\npassword %s\n' "$PAT" > /root/.netrc
    chmod 600 /root/.netrc
    unset PAT

    {{- range .Repos }}
    REPO="{{ . }}"
    REPO_NAME=$(basename "$REPO" .git | sed 's|.*/||')
    REPO_OWNER=$(echo "$REPO" | sed 's|.*github\.com/||' | cut -d/ -f1)
    if [ "$REPO_OWNER" != "$OWNER" ]; then
      echo "ERROR: repo $REPO owner $REPO_OWNER != desktop owner $OWNER" >&2
      exit 1
    fi
    if [ ! -d "$WORKSPACE/$REPO_NAME/.git" ]; then
      git clone "https://github.com/${OWNER}/${REPO_NAME}.git" "$WORKSPACE/$REPO_NAME"
      chown -R ubuntu:ubuntu "$WORKSPACE/$REPO_NAME"
    fi
    {{- end }}

    # Remove .netrc credentials after cloning.
    rm -f /root/.netrc
  - |
    {
      printf 'DESKTOP_ID="%s"\n' "{{ .DesktopID }}"
      printf 'GITHUB_OWNER="%s"\n' "{{ .GitHubOwner }}"
      printf 'BRIDGE_PORT="%s"\n' "{{ .BridgePort }}"
    } > /opt/ai-desktops/desktop.env
    chmod 600 /opt/ai-desktops/desktop.env

final_message: "ai-desktops bootstrap complete for {{ .DesktopID }}"
`

type cloudInitData struct {
	DesktopID     string
	GitHubOwner   string
	Region        string
	PATSecret     string
	Repos         []string
	BridgePort    int
	Hostname      string
	NovncVersion  string
	BridgeVersion string
	HTTPPort      int
	HTTPSPort     int
}

func renderCloudInit(data cloudInitData) (string, error) {
	tmpl, err := template.New("cloud-init").Parse(cloudInitTmpl)
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func main() {
	pulumi.Run(run)
}

func run(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	awsCfg := config.New(ctx, "aws")

	desktopID := cfg.Require("desktopId")
	githubOwner := cfg.Require("githubOwner")
	zone := cfg.Require("zone")
	instanceType := cfg.Get("instanceType")
	if instanceType == "" {
		instanceType = "t3.large"
	}
	subnetID := cfg.Require("subnetId")
	sgID := cfg.Require("securityGroupId")
	instanceProfile := cfg.Require("instanceProfile")
	region := awsCfg.Require("region")
	patSecret := cfg.Get("patSecret")
	if patSecret == "" {
		patSecret = "/ai-desktops/github/pat"
	}
	reposRaw := cfg.Get("repos")
	repos := []string{}
	if reposRaw != "" {
		repos = strings.Split(reposRaw, ",")
	}
	sshKeyName := cfg.Get("sshKeyName")
	environment := cfg.Get("environment")
	if environment == "" {
		environment = "dev"
	}
	bridgePort := cfg.GetInt("bridgePort")
	if bridgePort == 0 {
		bridgePort = defaultBridgePort
	}

	// Select AMI: prefer amiId from config (pre-baked AMI), fall back to hardcoded map.
	amiID := cfg.Get("amiId")
	if amiID == "" {
		var ok bool
		amiID, ok = ubuntuAMIs[region]
		if !ok {
			return fmt.Errorf("no Ubuntu 22.04 AMI configured for region %s; add it to ubuntuAMIs or provide amiId", region)
		}
	}

	hostname := fmt.Sprintf("%s.%s", desktopID, zone)

	// Use pre-rendered userData if provided, otherwise render cloud-init locally.
	userData := cfg.Get("userData")
	if userData == "" {
		var err error
		userData, err = renderCloudInit(cloudInitData{
			DesktopID:     desktopID,
			GitHubOwner:   githubOwner,
			Region:        region,
			PATSecret:     patSecret,
			Repos:         repos,
			BridgePort:    bridgePort,
			Hostname:      hostname,
			NovncVersion:  novncDesktopVersion,
			BridgeVersion: aiAgentBridgeVersion,
			HTTPPort:      novncHTTPPort,
			HTTPSPort:     novncHTTPSPort,
		})
		if err != nil {
			return fmt.Errorf("render cloud-init: %w", err)
		}
	}

	// The Pulumi AWS provider base64-encodes UserData automatically;
	// pass the raw string to avoid double-encoding.
	instanceArgs := &ec2.InstanceArgs{
		Ami:                      pulumi.String(amiID),
		InstanceType:             pulumi.String(instanceType),
		SubnetId:                 pulumi.String(subnetID),
		VpcSecurityGroupIds:      pulumi.StringArray{pulumi.String(sgID)},
		IamInstanceProfile:       pulumi.String(instanceProfile),
		UserData:                 pulumi.String(userData),
		UserDataReplaceOnChange:  pulumi.Bool(false),
		AssociatePublicIpAddress: pulumi.Bool(true),
		RootBlockDevice: &ec2.InstanceRootBlockDeviceArgs{
			VolumeSize:          pulumi.Int(40),
			VolumeType:          pulumi.String("gp3"),
			DeleteOnTermination: pulumi.Bool(true),
		},
		Tags: pulumi.StringMap{
			"Name":         pulumi.String(hostname),
			"managed-by":   pulumi.String("ai-desktops"),
			"desktop-id":   pulumi.String(desktopID),
			"github-owner": pulumi.String(githubOwner),
			"environment":  pulumi.String(environment),
		},
	}
	if sshKeyName != "" {
		instanceArgs.KeyName = pulumi.String(sshKeyName)
	}

	instance, err := ec2.NewInstance(ctx, "desktop-"+desktopID, instanceArgs)
	if err != nil {
		return err
	}

	// --- Route53 record ---
	zoneData, err := route53.LookupZone(ctx, &route53.LookupZoneArgs{
		Name: pulumi.StringRef(zone),
	})
	if err != nil {
		return fmt.Errorf("Route53 zone %q not found: %w", zone, err)
	}

	dnsRecord, err := route53.NewRecord(ctx, "desktop-dns-"+desktopID, &route53.RecordArgs{
		ZoneId: pulumi.String(zoneData.ZoneId),
		Name:   pulumi.String(desktopID),
		Type:   pulumi.String("A"),
		Ttl:    pulumi.Int(60),
		Records: pulumi.StringArray{
			instance.PublicIp,
		},
	})
	if err != nil {
		return err
	}
	_ = dnsRecord

	// --- Outputs ---
	ctx.Export("desktopId", pulumi.String(desktopID))
	ctx.Export("instanceId", instance.ID())
	ctx.Export("hostname", pulumi.String(hostname))
	ctx.Export("novncUrl", pulumi.Sprintf("https://%s:%d/novnc", hostname, novncHTTPSPort))
	ctx.Export("sshTarget", pulumi.Sprintf("ubuntu@%s", hostname))
	ctx.Export("workspacePath", pulumi.String("/workspace"))
	ctx.Export("githubOwner", pulumi.String(githubOwner))

	return nil
}
