package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/cloudwatch"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(run)
}

func run(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")

	zone := cfg.Require("zone")
	fleetTable := cfg.Get("fleetTable")
	if fleetTable == "" {
		fleetTable = "ai-desktops-fleet"
	}
	operatorCIDR := cfg.Get("operatorCIDR")
	if operatorCIDR == "" {
		operatorCIDR = "0.0.0.0/0"
	}
	environment := cfg.Get("environment")
	if environment == "" {
		environment = "dev"
	}
	wireguardEnabled := cfg.GetBool("wireguardEnabled")
	wireguardPort := cfg.GetInt("wireguardPort")
	if wireguardPort == 0 {
		wireguardPort = 51820
	}
	const wireguardSubnet = "10.99.0.0/24"
	vpcID := cfg.Get("vpcId")

	// --- Route53 hosted zone lookup ---
	zoneData, err := route53.LookupZone(ctx, &route53.LookupZoneArgs{
		Name: pulumi.StringRef(zone),
	})
	if err != nil {
		return fmt.Errorf("Route53 zone %q not found: %w; create the hosted zone before running init-foundation", zone, err)
	}

	// --- VPC ---
	var vpcIDOutput pulumi.StringOutput
	var subnetID pulumi.StringOutput

	if vpcID != "" {
		// Use the provided VPC.
		vpcIDOutput = pulumi.String(vpcID).ToStringOutput()
		subnets, err := ec2.GetSubnets(ctx, &ec2.GetSubnetsArgs{
			Filters: []ec2.GetSubnetsFilter{{
				Name:   "vpc-id",
				Values: []string{vpcID},
			}},
		})
		if err != nil {
			return fmt.Errorf("list subnets for vpc %s: %w", vpcID, err)
		}
		if len(subnets.Ids) == 0 {
			return fmt.Errorf("no subnets found in vpc %s", vpcID)
		}
		subnetID = pulumi.String(subnets.Ids[0]).ToStringOutput()
	} else {
		// Create a VPC with a public subnet.
		vpc, err := ec2.NewVpc(ctx, "ai-desktops-vpc", &ec2.VpcArgs{
			CidrBlock:          pulumi.String("10.10.0.0/16"),
			EnableDnsHostnames: pulumi.Bool(true),
			EnableDnsSupport:   pulumi.Bool(true),
			Tags: pulumi.StringMap{
				"Name":        pulumi.String("ai-desktops-vpc"),
				"managed-by":  pulumi.String("ai-desktops"),
				"environment": pulumi.String(environment),
			},
		})
		if err != nil {
			return err
		}

		igw, err := ec2.NewInternetGateway(ctx, "ai-desktops-igw", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
			Tags:  pulumi.StringMap{"Name": pulumi.String("ai-desktops-igw"), "managed-by": pulumi.String("ai-desktops"), "environment": pulumi.String(environment)},
		})
		if err != nil {
			return err
		}

		// Pin to the first available AZ (alphabetically) to avoid AZs that
		// don't support common instance types (e.g. us-east-1d for t3.large).
		azs, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
			State: pulumi.StringRef("available"),
		})
		if err != nil {
			return fmt.Errorf("list availability zones: %w", err)
		}
		if len(azs.Names) == 0 {
			return fmt.Errorf("no available AZs found in region")
		}
		subnetAZ := azs.Names[0]

		subnet, err := ec2.NewSubnet(ctx, "ai-desktops-subnet", &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String("10.10.1.0/24"),
			AvailabilityZone:    pulumi.String(subnetAZ),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			Tags: pulumi.StringMap{
				"Name":        pulumi.String("ai-desktops-subnet"),
				"managed-by":  pulumi.String("ai-desktops"),
				"environment": pulumi.String(environment),
			},
		})
		if err != nil {
			return err
		}

		rt, err := ec2.NewRouteTable(ctx, "ai-desktops-rt", &ec2.RouteTableArgs{
			VpcId: vpc.ID(),
			Routes: ec2.RouteTableRouteArray{
				&ec2.RouteTableRouteArgs{
					CidrBlock: pulumi.String("0.0.0.0/0"),
					GatewayId: igw.ID(),
				},
			},
		})
		if err != nil {
			return err
		}

		if _, err := ec2.NewRouteTableAssociation(ctx, "ai-desktops-rta", &ec2.RouteTableAssociationArgs{
			SubnetId:     subnet.ID(),
			RouteTableId: rt.ID(),
		}); err != nil {
			return err
		}

		vpcIDOutput = vpc.ID().ToStringOutput()
		subnetID = subnet.ID().ToStringOutput()
	}

	// --- Security group ---
	// When WireGuard is enabled:
	//   - UDP wireguardPort is open to 0.0.0.0/0 (peers must reach the handshake endpoint)
	//   - SSH, HTTP, HTTPS are restricted to the WireGuard subnet (10.99.0.0/24)
	// When WireGuard is disabled:
	//   - SSH is open to operatorCIDR; HTTP/HTTPS open to 0.0.0.0/0
	sshCIDR := operatorCIDR
	servicesCIDR := "0.0.0.0/0"
	if wireguardEnabled {
		sshCIDR = wireguardSubnet
		servicesCIDR = wireguardSubnet
	}
	ingress := ec2.SecurityGroupIngressArray{
		&ec2.SecurityGroupIngressArgs{
			Protocol:    pulumi.String("tcp"),
			FromPort:    pulumi.Int(22),
			ToPort:      pulumi.Int(22),
			CidrBlocks:  pulumi.StringArray{pulumi.String(sshCIDR)},
			Description: pulumi.String("SSH"),
		},
		&ec2.SecurityGroupIngressArgs{
			Protocol:    pulumi.String("tcp"),
			FromPort:    pulumi.Int(80),
			ToPort:      pulumi.Int(80),
			CidrBlocks:  pulumi.StringArray{pulumi.String(servicesCIDR)},
			Description: pulumi.String("HTTP"),
		},
		&ec2.SecurityGroupIngressArgs{
			Protocol:    pulumi.String("tcp"),
			FromPort:    pulumi.Int(8080),
			ToPort:      pulumi.Int(8080),
			CidrBlocks:  pulumi.StringArray{pulumi.String(servicesCIDR)},
			Description: pulumi.String("noVNC HTTP"),
		},
		&ec2.SecurityGroupIngressArgs{
			Protocol:    pulumi.String("tcp"),
			FromPort:    pulumi.Int(8443),
			ToPort:      pulumi.Int(8443),
			CidrBlocks:  pulumi.StringArray{pulumi.String(servicesCIDR)},
			Description: pulumi.String("noVNC HTTPS"),
		},
	}
	if wireguardEnabled {
		ingress = append(ingress, &ec2.SecurityGroupIngressArgs{
			Protocol:    pulumi.String("udp"),
			FromPort:    pulumi.Int(wireguardPort),
			ToPort:      pulumi.Int(wireguardPort),
			CidrBlocks:  pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			Description: pulumi.String("WireGuard VPN (wg-aidesktops)"),
		})
	}
	sg, err := ec2.NewSecurityGroup(ctx, "ai-desktops-sg", &ec2.SecurityGroupArgs{
		VpcId:       vpcIDOutput,
		Description: pulumi.String("ai-desktops desktop security group"),
		Ingress:     ingress,
		Egress: ec2.SecurityGroupEgressArray{
			&ec2.SecurityGroupEgressArgs{
				Protocol:   pulumi.String("-1"),
				FromPort:   pulumi.Int(0),
				ToPort:     pulumi.Int(0),
				CidrBlocks: pulumi.StringArray{pulumi.String("0.0.0.0/0")},
			},
		},
		Tags: pulumi.StringMap{
			"Name":        pulumi.String("ai-desktops-sg"),
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	// --- IAM instance profile ---
	assumeRolePolicy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "ec2.amazonaws.com" },
    "Action": "sts:AssumeRole"
  }]
}`

	role, err := iam.NewRole(ctx, "ai-desktops-role", &iam.RoleArgs{
		AssumeRolePolicy: pulumi.String(assumeRolePolicy),
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	// SSM managed policy for Session Manager tunnel support.
	if _, err := iam.NewRolePolicyAttachment(ctx, "ai-desktops-ssm-policy", &iam.RolePolicyAttachmentArgs{
		Role:      role.Name,
		PolicyArn: pulumi.String("arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"),
	}); err != nil {
		return err
	}

	// Secrets Manager and SSM Parameter Store read access for secret retrieval.
	// GetParametersByPath is required for the wireguard peer sync script.
	secretsPolicy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "secretsmanager:GetSecretValue",
      "ssm:GetParameter",
      "ssm:GetParameters",
      "ssm:GetParametersByPath"
    ],
    "Resource": "*"
  }]
}`
	if _, err := iam.NewRolePolicy(ctx, "ai-desktops-secrets-policy", &iam.RolePolicyArgs{
		Role:   role.Name,
		Policy: pulumi.String(secretsPolicy),
	}); err != nil {
		return err
	}

	// Route53 write access for certbot DNS-01 challenge during TLS cert provisioning.
	route53Policy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "route53:GetChange",
      "route53:ChangeResourceRecordSets",
      "route53:ListHostedZones"
    ],
    "Resource": "*"
  }]
}`
	if _, err := iam.NewRolePolicy(ctx, "ai-desktops-route53-policy", &iam.RolePolicyArgs{
		Role:   role.Name,
		Policy: pulumi.String(route53Policy),
	}); err != nil {
		return err
	}

	instanceProfile, err := iam.NewInstanceProfile(ctx, "ai-desktops-profile", &iam.InstanceProfileArgs{
		Role: role.Name,
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	// --- EventBridge: auto-sync WireGuard peers on SSM changes ---
	// When wireguardEnabled, an EventBridge rule watches for changes to
	// /ai-desktops/wireguard/peers/* in SSM and triggers the
	// ai-desktops-wireguard-sync script on all running desktops via SSM Run Command.
	if wireguardEnabled {
		regionInfo, err := aws.GetRegion(ctx, nil, nil)
		if err != nil {
			return fmt.Errorf("get AWS region: %w", err)
		}

		// IAM role that EventBridge assumes to call SSM Run Command.
		eventBridgeAssumePolicy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "Service": "events.amazonaws.com" },
    "Action": "sts:AssumeRole"
  }]
}`
		eventsRole, err := iam.NewRole(ctx, "ai-desktops-wg-events-role", &iam.RoleArgs{
			AssumeRolePolicy: pulumi.String(eventBridgeAssumePolicy),
			Tags: pulumi.StringMap{
				"managed-by":  pulumi.String("ai-desktops"),
				"environment": pulumi.String(environment),
			},
		})
		if err != nil {
			return err
		}

		// Allow EventBridge to send SSM Run Command to instances tagged managed-by=ai-desktops.
		eventBridgePolicy := `{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "ssm:SendCommand",
      "Resource": "arn:aws:ec2:*:*:instance/*",
      "Condition": {
        "StringEquals": { "ec2:ResourceTag/managed-by": "ai-desktops" }
      }
    },
    {
      "Effect": "Allow",
      "Action": "ssm:SendCommand",
      "Resource": "arn:aws:ssm:*:*:document/AWS-RunShellScript"
    }
  ]
}`
		if _, err := iam.NewRolePolicy(ctx, "ai-desktops-wg-events-policy", &iam.RolePolicyArgs{
			Role:   eventsRole.Name,
			Policy: pulumi.String(eventBridgePolicy),
		}); err != nil {
			return err
		}

		// EventBridge rule: fire when any /ai-desktops/wireguard/peers/* parameter changes.
		wgPeersRule, err := cloudwatch.NewEventRule(ctx, "ai-desktops-wg-peers-rule", &cloudwatch.EventRuleArgs{
			Description: pulumi.String("Sync WireGuard peers on all desktops when SSM peer config changes"),
			EventPattern: pulumi.String(`{
  "source": ["aws.ssm"],
  "detail-type": ["Parameter Store Change"],
  "detail": {
    "name": [{"prefix": "/ai-desktops/wireguard/peers/"}],
    "operation": ["Create", "Update", "Delete", "LabelParameterVersion"]
  }
}`),
			Tags: pulumi.StringMap{
				"managed-by":  pulumi.String("ai-desktops"),
				"environment": pulumi.String(environment),
			},
		})
		if err != nil {
			return err
		}

		// EventBridge target: run ai-desktops-wireguard-sync on all desktop instances via SSM.
		ssmDocARN := pulumi.Sprintf("arn:aws:ssm:%s::document/AWS-RunShellScript", regionInfo.Name)
		if _, err := cloudwatch.NewEventTarget(ctx, "ai-desktops-wg-peers-target", &cloudwatch.EventTargetArgs{
			Rule:    wgPeersRule.Name,
			Arn:     ssmDocARN,
			RoleArn: eventsRole.Arn,
			RunCommandTargets: cloudwatch.EventTargetRunCommandTargetArray{
				&cloudwatch.EventTargetRunCommandTargetArgs{
					Key:    pulumi.String("tag:managed-by"),
					Values: pulumi.StringArray{pulumi.String("ai-desktops")},
				},
			},
			Input: pulumi.String(`{"commands":["/usr/local/bin/ai-desktops-wireguard-sync"],"workingDirectory":["/tmp"],"executionTimeout":["300"]}`),
		}); err != nil {
			return err
		}
	}

	// --- DynamoDB fleet table ---
	table, err := dynamodb.NewTable(ctx, "ai-desktops-fleet", &dynamodb.TableArgs{
		Name:        pulumi.String(fleetTable),
		BillingMode: pulumi.String("PAY_PER_REQUEST"),
		HashKey:     pulumi.String("desktop_id"),
		Attributes: dynamodb.TableAttributeArray{
			&dynamodb.TableAttributeArgs{
				Name: pulumi.String("desktop_id"),
				Type: pulumi.String("S"),
			},
		},
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	// --- DynamoDB AMI history table ---
	amiTable, err := dynamodb.NewTable(ctx, "ai-desktops-ami", &dynamodb.TableArgs{
		Name:        pulumi.String("ai-desktops-ami"),
		BillingMode: pulumi.String("PAY_PER_REQUEST"),
		HashKey:     pulumi.String("ami_id"),
		Attributes: dynamodb.TableAttributeArray{
			&dynamodb.TableAttributeArgs{
				Name: pulumi.String("ami_id"),
				Type: pulumi.String("S"),
			},
		},
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	// --- Outputs ---
	ctx.Export("vpcId", vpcIDOutput)
	ctx.Export("subnetId", subnetID)
	ctx.Export("securityGroupId", sg.ID())
	ctx.Export("instanceProfile", instanceProfile.Name)
	ctx.Export("zoneId", pulumi.String(zoneData.ZoneId))
	ctx.Export("zone", pulumi.String(zone))
	ctx.Export("fleetTable", table.Name)
	ctx.Export("amiTable", amiTable.Name)

	return nil
}
