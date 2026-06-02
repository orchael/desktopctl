package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

const (
	defaultBridgePort = 9445
	novncHTTPSPort    = 8443
)

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
	sshKeyName := cfg.Get("sshKeyName")
	environment := cfg.Get("environment")
	if environment == "" {
		environment = "dev"
	}
	bridgePort := cfg.GetInt("bridgePort")
	if bridgePort == 0 {
		bridgePort = defaultBridgePort
	}

	// amiId must be set to a Packer-built novnc-desktop AMI; plain Ubuntu AMIs are not supported.
	amiID := cfg.Get("amiId")
	if amiID == "" {
		return fmt.Errorf("amiId is required: set it to a Packer-built ai-desktops AMI (see packer/ubuntu-desktop.pkr.hcl)")
	}

	hostname := fmt.Sprintf("%s.%s", desktopID, zone)

	// Provisioning logic lives in the CLI so the stack only owns infrastructure.
	userData := cfg.Get("userData")
	if userData == "" {
		return fmt.Errorf("userData is required: render cloud-init in the ai-desktops CLI before updating the stack")
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
	ctx.Export("amiId", pulumi.String(amiID))
	ctx.Export("region", pulumi.String(region))

	return nil
}
