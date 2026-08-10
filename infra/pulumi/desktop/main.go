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
	defaultVolumeSize = 100
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
	volumeSize := cfg.GetInt("volumeSize")
	if volumeSize <= 0 {
		volumeSize = defaultVolumeSize
	}
	if volumeSize <= 0 {
		return fmt.Errorf("volumeSize must be a positive integer, got %d", volumeSize)
	}

	// amiId must be set to a Packer-built novnc-desktop AMI; plain Ubuntu AMIs are not supported.
	amiID := cfg.Get("amiId")
	if amiID == "" {
		return fmt.Errorf("amiId is required: set it to a Packer-built ai-desktops AMI (see packer/ubuntu-desktop.pkr.hcl)")
	}

	nestedVirtualization := cfg.Get("nestedVirtualization") == "true"
	marketType := cfg.Get("marketType")
	if marketType == "" {
		marketType = "on-demand"
	}
	spotMaxPrice := cfg.Get("spotMaxPrice")

	// importInstanceId is set by the CLI when the instance was pre-launched via
	// RunInstances with CpuOptions.NestedVirtualization=enabled. Pulumi imports
	// the existing resource instead of creating a new one.
	// TODO: remove this workaround once pulumi-aws exposes NestedVirtualization
	// on InstanceCpuOptionsArgs. Track:
	// https://github.com/pulumi/pulumi-aws/issues — field not yet in v6.83.3;
	// watch for a new minor that adds NestedVirtualization to InstanceCpuOptionsArgs.
	importInstanceID := cfg.Get("importInstanceId")

	hostname := fmt.Sprintf("%s.%s", desktopID, zone)

	// Provisioning logic lives in the CLI so the stack only owns infrastructure.
	// Prefer gzip-compressed base64 user data so large cloud-init payloads stay
	// under EC2's 16 KiB raw user-data limit. Keep raw userData as a fallback
	// for older CLI-created stack configs.
	userDataBase64 := cfg.Get("userDataBase64")
	userData := cfg.Get("userData")
	if userDataBase64 == "" && userData == "" {
		return fmt.Errorf("userData is required: render cloud-init in the ai-desktops CLI before updating the stack")
	}

	// NestedVirtualization=enabled is incompatible with hibernation. Spot desktops
	// are also configured to stop on interruption, so keep hibernation disabled.
	hibernation := !nestedVirtualization && marketType != "spot"

	instanceArgs := &ec2.InstanceArgs{
		Ami:                      pulumi.String(amiID),
		InstanceType:             pulumi.String(instanceType),
		SubnetId:                 pulumi.String(subnetID),
		VpcSecurityGroupIds:      pulumi.StringArray{pulumi.String(sgID)},
		IamInstanceProfile:       pulumi.String(instanceProfile),
		UserDataReplaceOnChange:  pulumi.Bool(false),
		AssociatePublicIpAddress: pulumi.Bool(true),
		Hibernation:              pulumi.Bool(hibernation),
		RootBlockDevice: &ec2.InstanceRootBlockDeviceArgs{
			VolumeSize:          pulumi.Int(volumeSize),
			VolumeType:          pulumi.String("gp3"),
			Encrypted:           pulumi.Bool(true),
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
	// NestedVirtualization is set at launch time by the CLI via RunInstances with
	// CpuOptions.NestedVirtualization=enabled, because the Pulumi AWS Go SDK does
	// not yet expose the NestedVirtualization field on InstanceCpuOptionsArgs.
	// When importInstanceID is set, Pulumi adopts the pre-launched instance;
	// IgnoreChanges on cpuOptions prevents Pulumi from trying to remove settings
	// it cannot set itself.
	if sshKeyName != "" {
		instanceArgs.KeyName = pulumi.String(sshKeyName)
	}
	if marketType == "spot" {
		spotOptions := &ec2.InstanceInstanceMarketOptionsSpotOptionsArgs{
			InstanceInterruptionBehavior: pulumi.String("stop"),
			SpotInstanceType:             pulumi.String("persistent"),
		}
		if spotMaxPrice != "" {
			spotOptions.MaxPrice = pulumi.String(spotMaxPrice)
		}
		instanceArgs.InstanceMarketOptions = &ec2.InstanceInstanceMarketOptionsArgs{
			MarketType:  pulumi.String("spot"),
			SpotOptions: spotOptions,
		}
	}
	if userDataBase64 != "" {
		instanceArgs.UserDataBase64 = pulumi.String(userDataBase64)
	} else {
		// The Pulumi AWS provider base64-encodes UserData automatically; pass
		// the raw string to avoid double-encoding.
		instanceArgs.UserData = pulumi.String(userData)
	}

	instanceResourceOpts := []pulumi.ResourceOption{}
	if importInstanceID != "" {
		instanceResourceOpts = append(instanceResourceOpts,
			pulumi.Import(pulumi.ID(importInstanceID)),
			pulumi.IgnoreChanges([]string{"cpuOptions"}),
		)
	}

	instance, err := ec2.NewInstance(ctx, "desktop-"+desktopID, instanceArgs, instanceResourceOpts...)
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
	ctx.Export("novncUrl", pulumi.Sprintf("https://%s:%d/novnc/vnc.html", hostname, novncHTTPSPort))
	ctx.Export("sshTarget", pulumi.Sprintf("ubuntu@%s", hostname))
	ctx.Export("workspacePath", pulumi.String("/workspace"))
	ctx.Export("githubOwner", pulumi.String(githubOwner))
	ctx.Export("amiId", pulumi.String(amiID))
	ctx.Export("region", pulumi.String(region))
	ctx.Export("marketType", pulumi.String(marketType))

	return nil
}
