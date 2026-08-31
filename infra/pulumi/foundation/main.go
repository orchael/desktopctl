package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/dynamodb"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/ec2"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/route53"
	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/secretsmanager"
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
	pulumiBackendBucket := cfg.Get("pulumiBackendBucket")
	if pulumiBackendBucket == "" {
		return fmt.Errorf("pulumiBackendBucket is required")
	}
	operatorCredentialsSecretName := cfg.Get("operatorCredentialsSecretName")
	if operatorCredentialsSecretName == "" {
		operatorCredentialsSecretName = fmt.Sprintf("/ai-desktops/%s/control-plane/aws-operator", environment)
	}

	caller, err := aws.GetCallerIdentity(ctx, nil)
	if err != nil {
		return fmt.Errorf("get AWS caller identity: %w", err)
	}
	partition := arnPartition(caller.Arn)
	accountID := caller.AccountId

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
	var subnetIDs pulumi.StringArray

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
		for _, id := range subnets.Ids {
			subnetIDs = append(subnetIDs, pulumi.String(id))
		}
	} else {
		// Create a VPC with public subnets across available AZs. Keeping the
		// original first subnet resource name avoids replacing existing stacks,
		// while new subnets give Spot launches another AZ when one pool is empty.
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

		azs, err := aws.GetAvailabilityZones(ctx, &aws.GetAvailabilityZonesArgs{
			State: pulumi.StringRef("available"),
		})
		if err != nil {
			return fmt.Errorf("list availability zones: %w", err)
		}
		if len(azs.Names) == 0 {
			return fmt.Errorf("no available AZs found in region")
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

		subnetCount := len(azs.Names)
		if subnetCount > 3 {
			subnetCount = 3
		}
		for i := 0; i < subnetCount; i++ {
			name := "ai-desktops-subnet"
			if i > 0 {
				name = fmt.Sprintf("ai-desktops-subnet-%d", i+1)
			}
			subnet, err := ec2.NewSubnet(ctx, name, &ec2.SubnetArgs{
				VpcId:               vpc.ID(),
				CidrBlock:           pulumi.String(fmt.Sprintf("10.10.%d.0/24", i+1)),
				AvailabilityZone:    pulumi.String(azs.Names[i]),
				MapPublicIpOnLaunch: pulumi.Bool(true),
				Tags: pulumi.StringMap{
					"Name":        pulumi.String(name),
					"managed-by":  pulumi.String("ai-desktops"),
					"environment": pulumi.String(environment),
				},
			})
			if err != nil {
				return err
			}
			if i == 0 {
				subnetID = subnet.ID().ToStringOutput()
			}
			subnetIDs = append(subnetIDs, subnet.ID().ToStringOutput())

			assocName := "ai-desktops-rta"
			if i > 0 {
				assocName = fmt.Sprintf("ai-desktops-rta-%d", i+1)
			}
			if _, err := ec2.NewRouteTableAssociation(ctx, assocName, &ec2.RouteTableAssociationArgs{
				SubnetId:     subnet.ID(),
				RouteTableId: rt.ID(),
			}); err != nil {
				return err
			}
		}

		vpcIDOutput = vpc.ID().ToStringOutput()
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
  }, {
    "Effect": "Deny",
    "Action": "secretsmanager:GetSecretValue",
    "Resource": "*",
    "Condition": {
      "StringEquals": {
        "secretsmanager:ResourceTag/ai-desktops-scope": "operator"
      }
    }
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

	operatorUserName := "ai-desktops-control-plane-" + environment
	operatorRoleName := "ai-desktops-control-plane-role-" + environment

	operatorUser, err := iam.NewUser(ctx, "ai-desktops-control-plane-user", &iam.UserArgs{
		Name: pulumi.String(operatorUserName),
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	operatorAssumeRolePolicy := operatorUser.Arn.ApplyT(func(userArn string) string {
		return fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": { "AWS": %q },
    "Action": "sts:AssumeRole"
  }]
}`, userArn)
	}).(pulumi.StringOutput)

	operatorRole, err := iam.NewRole(ctx, "ai-desktops-control-plane-role", &iam.RoleArgs{
		Name:             pulumi.String(operatorRoleName),
		AssumeRolePolicy: operatorAssumeRolePolicy,
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
		},
	})
	if err != nil {
		return err
	}

	operatorPolicy := controlPlanePolicy(partition, accountID, pulumiBackendBucket)
	if _, err := iam.NewRolePolicy(ctx, "ai-desktops-control-plane-policy", &iam.RolePolicyArgs{
		Role:   operatorRole.Name,
		Policy: pulumi.String(operatorPolicy),
	}); err != nil {
		return err
	}

	operatorAssumePolicy := operatorRole.Arn.ApplyT(func(roleArn string) string {
		return fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": "sts:AssumeRole",
    "Resource": %q
  }]
}`, roleArn)
	}).(pulumi.StringOutput)

	if _, err := iam.NewUserPolicy(ctx, "ai-desktops-control-plane-assume-role", &iam.UserPolicyArgs{
		User:   operatorUser.Name,
		Policy: operatorAssumePolicy,
	}); err != nil {
		return err
	}

	operatorAccessKey, err := iam.NewAccessKey(ctx, "ai-desktops-control-plane-access-key", &iam.AccessKeyArgs{
		User: operatorUser.Name,
	})
	if err != nil {
		return err
	}

	operatorSecret, err := secretsmanager.NewSecret(ctx, "ai-desktops-control-plane-credentials", &secretsmanager.SecretArgs{
		Name:        pulumi.String(operatorCredentialsSecretName),
		Description: pulumi.String("AWS control-plane credentials for ai-desktops " + environment),
		Tags: pulumi.StringMap{
			"managed-by":        pulumi.String("ai-desktops"),
			"environment":       pulumi.String(environment),
			"ai-desktops-scope": pulumi.String("operator"),
		},
	})
	if err != nil {
		return err
	}

	operatorSecretPayload := pulumi.All(
		operatorAccessKey.ID(),
		operatorAccessKey.Secret,
		operatorRole.Arn,
		operatorUser.Arn,
	).ApplyT(func(args []interface{}) (string, error) {
		payload := map[string]string{
			"OPERATOR_AWS_ACCESS_KEY_ID":     fmt.Sprint(args[0]),
			"OPERATOR_AWS_SECRET_ACCESS_KEY": fmt.Sprint(args[1]),
			"OPERATOR_ROLE_ARN":              fmt.Sprint(args[2]),
			"OPERATOR_USER_ARN":              fmt.Sprint(args[3]),
			"OPERATOR_ENVIRONMENT":           environment,
			"OPERATOR_SOURCE_PROFILE":        "ai-desktops-" + environment + "-user",
			"OPERATOR_ROLE_PROFILE":          "ai-desktops-" + environment,
		}
		b, err := json.Marshal(payload)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}).(pulumi.StringOutput)

	if _, err := secretsmanager.NewSecretVersion(ctx, "ai-desktops-control-plane-credentials-version", &secretsmanager.SecretVersionArgs{
		SecretId:     operatorSecret.ID(),
		SecretString: operatorSecretPayload,
	}); err != nil {
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
	ctx.Export("subnetIds", subnetIDs.ToStringArrayOutput())
	ctx.Export("securityGroupId", sg.ID())
	ctx.Export("instanceProfile", instanceProfile.Name)
	ctx.Export("zoneId", pulumi.String(zoneData.ZoneId))
	ctx.Export("zone", pulumi.String(zone))
	ctx.Export("fleetTable", table.Name)
	ctx.Export("amiTable", amiTable.Name)
	ctx.Export("operatorCredentialsSecretName", operatorSecret.Name)
	ctx.Export("operatorRoleArn", operatorRole.Arn)
	ctx.Export("operatorUserName", operatorUser.Name)

	return nil
}

func arnPartition(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) >= 2 && parts[1] != "" {
		return parts[1]
	}
	return "aws"
}

func controlPlanePolicy(partition, accountID, stateBucket string) string {
	return fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "IdentityChecks",
      "Effect": "Allow",
      "Action": ["sts:GetCallerIdentity"],
      "Resource": "*"
    },
    {
      "Sid": "PulumiStateBackend",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket",
        "s3:GetBucketLocation",
        "s3:GetBucketTagging",
        "s3:PutBucketTagging",
        "s3:GetBucketVersioning",
        "s3:PutBucketVersioning",
        "s3:GetEncryptionConfiguration",
        "s3:PutEncryptionConfiguration",
        "s3:GetBucketPublicAccessBlock",
        "s3:PutBucketPublicAccessBlock",
        "s3:ListBucket",
        "s3:ListBucketVersions",
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject"
      ],
      "Resource": [
        "arn:%[1]s:s3:::%[3]s",
        "arn:%[1]s:s3:::%[3]s/*"
      ]
    },
    {
      "Sid": "EC2AndPackerControlPlane",
      "Effect": "Allow",
      "Action": [
        "ec2:AllocateAddress",
        "ec2:AssociateAddress",
        "ec2:AssociateRouteTable",
        "ec2:AttachInternetGateway",
        "ec2:AuthorizeSecurityGroupEgress",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:CreateImage",
        "ec2:CreateInternetGateway",
        "ec2:CreateKeyPair",
        "ec2:CreateRoute",
        "ec2:CreateRouteTable",
        "ec2:CreateSecurityGroup",
        "ec2:CreateSnapshot",
        "ec2:CreateSubnet",
        "ec2:CreateTags",
        "ec2:CreateVolume",
        "ec2:CreateVpc",
        "ec2:DeleteInternetGateway",
        "ec2:DeleteKeyPair",
        "ec2:DeleteRoute",
        "ec2:DeleteRouteTable",
        "ec2:DeleteSecurityGroup",
        "ec2:DeleteSnapshot",
        "ec2:DeleteSubnet",
        "ec2:DeleteTags",
        "ec2:DeleteVolume",
        "ec2:DeleteVpc",
        "ec2:DeregisterImage",
        "ec2:Describe*",
        "ec2:DetachInternetGateway",
        "ec2:DisassociateAddress",
        "ec2:DisassociateRouteTable",
        "ec2:GetPasswordData",
        "ec2:ImportKeyPair",
        "ec2:ModifyImageAttribute",
        "ec2:ModifyInstanceAttribute",
        "ec2:ModifySubnetAttribute",
        "ec2:ModifyVpcAttribute",
        "ec2:ReleaseAddress",
        "ec2:ReplaceRoute",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:RevokeSecurityGroupIngress",
        "ec2:RunInstances",
        "ec2:StartInstances",
        "ec2:StopInstances",
        "ec2:TerminateInstances"
      ],
      "Resource": "*"
    },
    {
      "Sid": "DynamoDBCreateTables",
      "Effect": "Allow",
      "Action": [
        "dynamodb:CreateTable"
      ],
      "Resource": "*"
    },
    {
      "Sid": "DynamoDBFleetAndAmiTables",
      "Effect": "Allow",
      "Action": [
        "dynamodb:DeleteItem",
        "dynamodb:DeleteTable",
        "dynamodb:DescribeTable",
        "dynamodb:GetItem",
        "dynamodb:ListTagsOfResource",
        "dynamodb:PutItem",
        "dynamodb:Scan",
        "dynamodb:TagResource",
        "dynamodb:UpdateItem",
        "dynamodb:UpdateTable"
      ],
      "Resource": ["arn:%[1]s:dynamodb:*:%[2]s:table/ai-desktops-*"]
    },
    {
      "Sid": "Route53DesktopRecords",
      "Effect": "Allow",
      "Action": [
        "route53:ChangeResourceRecordSets",
        "route53:GetChange",
        "route53:GetHostedZone",
        "route53:ListHostedZones",
        "route53:ListResourceRecordSets"
      ],
      "Resource": "*"
    },
    {
      "Sid": "CreateAiDesktopsSecrets",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:CreateSecret"
      ],
      "Resource": "*",
      "Condition": {
        "StringLike": {
          "secretsmanager:Name": "/ai-desktops/*"
        }
      }
    },
    {
      "Sid": "SecretsAndParameters",
      "Effect": "Allow",
      "Action": [
        "secretsmanager:DescribeSecret",
        "secretsmanager:GetSecretValue",
        "secretsmanager:ListSecretVersionIds",
        "secretsmanager:PutSecretValue",
        "secretsmanager:TagResource",
        "ssm:GetParameter",
        "ssm:GetParameters"
      ],
      "Resource": [
        "arn:%[1]s:secretsmanager:*:%[2]s:secret:/ai-desktops/*",
        "arn:%[1]s:ssm:*:%[2]s:parameter/ai-desktops/*"
      ]
    },
    {
      "Sid": "SessionManagerTunnels",
      "Effect": "Allow",
      "Action": [
        "ssm:DescribeSessions",
        "ssm:StartSession",
        "ssm:TerminateSession"
      ],
      "Resource": "*"
    },
    {
      "Sid": "PassAiDesktopsInstanceRole",
      "Effect": "Allow",
      "Action": ["iam:PassRole"],
      "Resource": ["arn:%[1]s:iam::%[2]s:role/ai-desktops-*"]
    },
    {
      "Sid": "ManageAiDesktopsIamRoles",
      "Effect": "Allow",
      "Action": [
        "iam:AddRoleToInstanceProfile",
        "iam:AttachRolePolicy",
        "iam:CreateInstanceProfile",
        "iam:CreateRole",
        "iam:DeleteInstanceProfile",
        "iam:DeleteRole",
        "iam:DeleteRolePolicy",
        "iam:DetachRolePolicy",
        "iam:GetInstanceProfile",
        "iam:GetPolicy",
        "iam:GetPolicyVersion",
        "iam:GetRole",
        "iam:GetRolePolicy",
        "iam:ListAttachedRolePolicies",
        "iam:ListInstanceProfilesForRole",
        "iam:ListRolePolicies",
        "iam:PutRolePolicy",
        "iam:RemoveRoleFromInstanceProfile",
        "iam:TagInstanceProfile",
        "iam:TagRole",
        "iam:UntagInstanceProfile",
        "iam:UntagRole",
        "iam:UpdateAssumeRolePolicy"
      ],
      "Resource": [
        "arn:%[1]s:iam::%[2]s:role/ai-desktops-*",
        "arn:%[1]s:iam::%[2]s:instance-profile/ai-desktops-*",
        "arn:%[1]s:iam::%[2]s:policy/ai-desktops-*"
      ]
    },
    {
      "Sid": "ManageAiDesktopsIamUsers",
      "Effect": "Allow",
      "Action": [
        "iam:CreateAccessKey",
        "iam:CreateUser",
        "iam:DeleteAccessKey",
        "iam:DeleteUser",
        "iam:DeleteUserPolicy",
        "iam:GetAccessKeyLastUsed",
        "iam:GetUser",
        "iam:GetUserPolicy",
        "iam:ListAccessKeys",
        "iam:ListUserPolicies",
        "iam:PutUserPolicy",
        "iam:TagUser",
        "iam:UntagUser",
        "iam:UpdateAccessKey"
      ],
      "Resource": [
        "arn:%[1]s:iam::%[2]s:user/ai-desktops-*"
      ]
    },
    {
      "Sid": "ReadAwsManagedPolicies",
      "Effect": "Allow",
      "Action": [
        "iam:GetPolicy",
        "iam:GetPolicyVersion"
      ],
      "Resource": [
        "arn:%[1]s:iam::aws:policy/*"
      ]
    }
  ]
}`, partition, accountID, stateBucket)
}
