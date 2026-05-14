package main

import (
	"fmt"

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
				"Name":       pulumi.String("ai-desktops-vpc"),
				"managed-by": pulumi.String("ai-desktops"),
			},
		})
		if err != nil {
			return err
		}

		igw, err := ec2.NewInternetGateway(ctx, "ai-desktops-igw", &ec2.InternetGatewayArgs{
			VpcId: vpc.ID(),
			Tags:  pulumi.StringMap{"Name": pulumi.String("ai-desktops-igw"), "managed-by": pulumi.String("ai-desktops")},
		})
		if err != nil {
			return err
		}

		subnet, err := ec2.NewSubnet(ctx, "ai-desktops-subnet", &ec2.SubnetArgs{
			VpcId:               vpc.ID(),
			CidrBlock:           pulumi.String("10.10.1.0/24"),
			MapPublicIpOnLaunch: pulumi.Bool(true),
			Tags: pulumi.StringMap{
				"Name":       pulumi.String("ai-desktops-subnet"),
				"managed-by": pulumi.String("ai-desktops"),
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
			// SSH from operator CIDR.
			&ec2.SecurityGroupIngressArgs{
				Protocol:   pulumi.String("tcp"),
				FromPort:   pulumi.Int(22),
				ToPort:     pulumi.Int(22),
				CidrBlocks: pulumi.StringArray{pulumi.String(operatorCIDR)},
				Description: pulumi.String("SSH operator access"),
			},
			// noVNC HTTPS (443) from everywhere.
			&ec2.SecurityGroupIngressArgs{
				Protocol:    pulumi.String("tcp"),
				FromPort:    pulumi.Int(443),
				ToPort:      pulumi.Int(443),
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
			"Name":       pulumi.String("ai-desktops-sg"),
			"managed-by": pulumi.String("ai-desktops"),
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
			"managed-by": pulumi.String("ai-desktops"),
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

	instanceProfile, err := iam.NewInstanceProfile(ctx, "ai-desktops-profile", &iam.InstanceProfileArgs{
		Role: role.Name,
		Tags: pulumi.StringMap{
			"managed-by": pulumi.String("ai-desktops"),
		},
	})
	if err != nil {
		return err
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
			"managed-by": pulumi.String("ai-desktops"),
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

	return nil
}
