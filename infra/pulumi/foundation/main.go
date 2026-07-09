package main

import (
	"fmt"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
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
	environment := cfg.Get("environment")
	if environment == "" {
		environment = "dev"
	}
	fleetTable := cfg.Get("fleetTable")
	if fleetTable == "" {
		fleetTable = "ai-desktops-fleet-" + environment
	}
	operatorCIDR := cfg.Get("operatorCIDR")
	if operatorCIDR == "" {
		operatorCIDR = "0.0.0.0/0"
	}
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
	sg, err := ec2.NewSecurityGroup(ctx, "ai-desktops-sg", &ec2.SecurityGroupArgs{
		VpcId:       vpcIDOutput,
		Description: pulumi.String("ai-desktops desktop security group"),
		Ingress: ec2.SecurityGroupIngressArray{
			// SSH on port 22 from operator CIDR (configurable, defaults to 0.0.0.0/0 for dev).
			&ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String("tcp"),
				FromPort:    pulumi.Int(22),
				ToPort:      pulumi.Int(22),
				CidrBlocks:  pulumi.StringArray{pulumi.String(operatorCIDR)},
				Description: pulumi.String("SSH - configurable per environment"),
			},
			// HTTP on port 80 (for ACME challenges or service testing).
			&ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String("tcp"),
				FromPort:    pulumi.Int(80),
				ToPort:      pulumi.Int(80),
				CidrBlocks:  pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				Description: pulumi.String("HTTP - ACME challenges and service testing"),
			},
			// noVNC HTTP on 8080 (redirect to HTTPS) from everywhere.
			&ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String("tcp"),
				FromPort:    pulumi.Int(8080),
				ToPort:      pulumi.Int(8080),
				CidrBlocks:  pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				Description: pulumi.String("noVNC HTTP"),
			},
			// noVNC HTTPS on 8443 from everywhere.
			&ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String("tcp"),
				FromPort:    pulumi.Int(8443),
				ToPort:      pulumi.Int(8443),
				CidrBlocks:  pulumi.StringArray{pulumi.String("0.0.0.0/0")},
				Description: pulumi.String("noVNC HTTPS"),
			},
		},
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
	secretsPolicy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "secretsmanager:GetSecretValue",
      "ssm:GetParameter",
      "ssm:GetParameters"
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

	// CloudWatch Agent permissions: publish metrics and write logs.
	// The CloudWatch agent collects memory, disk, and swap metrics (not natively
	// reported by EC2) and forwards system logs for crash/OOM post-mortem analysis.
	cloudWatchPolicy := `{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "cloudwatch:PutMetricData",
      "logs:CreateLogGroup",
      "logs:CreateLogStream",
      "logs:PutLogEvents",
      "logs:DescribeLogStreams",
      "logs:PutRetentionPolicy"
    ],
    "Resource": "*"
  }]
}`
	if _, err := iam.NewRolePolicy(ctx, "ai-desktops-cloudwatch-policy", &iam.RolePolicyArgs{
		Role:   role.Name,
		Policy: pulumi.String(cloudWatchPolicy),
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

	// --- DynamoDB fleet table ---
	// pulumi.Aliases preserves the prior logical name ("ai-desktops-fleet") so
	// existing stacks don't force a delete/recreate on the next `pulumi up`.
	table, err := dynamodb.NewTable(ctx, "fleet-table", &dynamodb.TableArgs{
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
	}, pulumi.Aliases([]pulumi.Alias{{Name: pulumi.StringInput(pulumi.String("ai-desktops-fleet"))}}))
	if err != nil {
		return err
	}

	// --- DynamoDB AMI history table ---
	amiTableName := "ai-desktops-ami-" + environment
	amiTable, err := dynamodb.NewTable(ctx, "ai-desktops-ami", &dynamodb.TableArgs{
		Name:        pulumi.String(amiTableName),
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
