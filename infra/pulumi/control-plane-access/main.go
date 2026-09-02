package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pulumi/pulumi-aws/sdk/v6/go/aws/iam"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi/config"
)

func main() {
	pulumi.Run(run)
}

func run(ctx *pulumi.Context) error {
	cfg := config.New(ctx, "")
	environment := cfg.Get("environment")
	if environment == "" {
		environment = "dev"
	}
	fleetTable := cfg.Get("fleetTable")
	if fleetTable == "" {
		fleetTable = "ai-desktops-fleet-" + environment
	}
	amiTable := cfg.Get("amiTable")
	if amiTable == "" {
		amiTable = "ai-desktops-ami-" + environment
	}
	backendBucket := cfg.Require("backendBucket")
	hostedZoneArn := cfg.Require("hostedZoneArn")
	desktopRoleArn := cfg.Require("desktopRoleArn")
	secretPrefix := cfg.Get("secretPrefix")
	if secretPrefix == "" {
		secretPrefix = "/ai-desktops/"
	}
	externalID := cfg.RequireSecret("externalId")

	roleName := cfg.Get("roleName")
	if roleName == "" {
		roleName = defaultRoleName(environment)
	}
	userName := cfg.Get("bootstrapUserName")
	if userName == "" {
		userName = defaultBootstrapUserName(environment)
	}

	user, err := iam.NewUser(ctx, userName, &iam.UserArgs{
		Name: pulumi.String(userName),
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
			"component":   pulumi.String("control-plane-access"),
		},
	})
	if err != nil {
		return err
	}

	assumeRolePolicy := pulumi.All(user.Arn, externalID).ApplyT(func(args []any) (string, error) {
		userArn := args[0].(string)
		externalID := args[1].(string)
		return policyJSON(map[string]any{
			"Version": "2012-10-17",
			"Statement": []map[string]any{{
				"Effect":    "Allow",
				"Principal": map[string]any{"AWS": userArn},
				"Action":    "sts:AssumeRole",
				"Condition": map[string]any{
					"StringEquals": map[string]string{"sts:ExternalId": externalID},
				},
			}},
		})
	}).(pulumi.StringOutput)

	role, err := iam.NewRole(ctx, roleName, &iam.RoleArgs{
		Name:             pulumi.String(roleName),
		AssumeRolePolicy: assumeRolePolicy,
		Tags: pulumi.StringMap{
			"managed-by":  pulumi.String("ai-desktops"),
			"environment": pulumi.String(environment),
			"component":   pulumi.String("control-plane-access"),
		},
	})
	if err != nil {
		return err
	}

	userPolicy := role.Arn.ApplyT(func(roleArn string) (string, error) {
		return policyJSON(assumeOnlyPolicy(roleArn))
	}).(pulumi.StringOutput)
	if _, err := iam.NewUserPolicy(ctx, userName+"-assume-role", &iam.UserPolicyArgs{
		User:   user.Name,
		Policy: userPolicy,
	}); err != nil {
		return err
	}

	rolePolicy, err := controlPlaneRolePolicy(fleetTable, amiTable, backendBucket, hostedZoneArn, desktopRoleArn, secretPrefix)
	if err != nil {
		return err
	}
	if _, err := iam.NewRolePolicy(ctx, roleName+"-fleet-access", &iam.RolePolicyArgs{
		Role:   role.Name,
		Policy: pulumi.String(rolePolicy),
	}); err != nil {
		return err
	}

	key, err := iam.NewAccessKey(ctx, userName, &iam.AccessKeyArgs{
		User: user.Name,
	})
	if err != nil {
		return err
	}

	ctx.Export("ROLE_ARN", role.Arn)
	ctx.Export("EXTERNAL_ID", externalID)
	ctx.Export("ACCESS_KEY", key.ID())
	ctx.Export("SECRET", pulumi.ToSecret(key.Secret))
	ctx.Export("roleArn", role.Arn)
	ctx.Export("userName", user.Name)
	ctx.Export("accessKeyId", key.ID())
	ctx.Export("secretAccessKey", pulumi.ToSecret(key.Secret))
	ctx.Export("externalId", externalID)
	ctx.Export("fleetTable", pulumi.String(fleetTable))
	ctx.Export("amiTable", pulumi.String(amiTable))
	ctx.Export("backendBucket", pulumi.String(backendBucket))
	return nil
}

func assumeOnlyPolicy(roleArn string) map[string]any {
	return map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect":   "Allow",
			"Action":   "sts:AssumeRole",
			"Resource": roleArn,
		}},
	}
}

func defaultRoleName(environment string) string {
	return "ai-desktops-control-plane-" + environment
}

func defaultBootstrapUserName(environment string) string {
	return "ai-desktop-user-" + environment
}

func controlPlaneRolePolicy(fleetTable, amiTable, backendBucket, hostedZoneArn, desktopRoleArn, secretPrefix string) (string, error) {
	ddbFleetArn := fmt.Sprintf("arn:aws:dynamodb:*:*:table/%s", fleetTable)
	ddbAMIArn := fmt.Sprintf("arn:aws:dynamodb:*:*:table/%s", amiTable)
	secretArn := fmt.Sprintf("arn:aws:secretsmanager:*:*:secret:%s*", secretPrefix)
	parameterArn := fmt.Sprintf("arn:aws:ssm:*:*:parameter/%s*", strings.TrimPrefix(secretPrefix, "/"))
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Sid":    "FleetTables",
				"Effect": "Allow",
				"Action": []string{
					"dynamodb:DescribeTable",
					"dynamodb:GetItem",
					"dynamodb:PutItem",
					"dynamodb:Scan",
					"dynamodb:UpdateItem",
					"dynamodb:DeleteItem",
				},
				"Resource": []string{ddbFleetArn, ddbAMIArn},
			},
			{
				"Sid":      "EC2FleetDescribe",
				"Effect":   "Allow",
				"Action":   "ec2:Describe*",
				"Resource": "*",
			},
			{
				"Sid":      "EC2FleetLaunch",
				"Effect":   "Allow",
				"Action":   "ec2:RunInstances",
				"Resource": "*",
				"Condition": map[string]any{
					"StringEquals": map[string]string{
						"aws:RequestTag/managed-by": "ai-desktops",
					},
				},
			},
			{
				"Sid":    "EC2FleetLifecycle",
				"Effect": "Allow",
				"Action": []string{
					"ec2:StartInstances",
					"ec2:StopInstances",
					"ec2:TerminateInstances",
					"ec2:CreateTags",
					"ec2:ModifyInstanceAttribute",
				},
				"Resource": "*",
				"Condition": map[string]any{
					"StringEquals": map[string]string{
						"ec2:ResourceTag/managed-by": "ai-desktops",
					},
				},
			},
			{
				"Sid":      "PassDesktopRole",
				"Effect":   "Allow",
				"Action":   "iam:PassRole",
				"Resource": desktopRoleArn,
			},
			{
				"Sid":    "SSMSessions",
				"Effect": "Allow",
				"Action": []string{
					"ssm:StartSession",
					"ssm:TerminateSession",
					"ssm:DescribeSessions",
					"ssm:GetConnectionStatus",
				},
				"Resource": "*",
			},
			{
				"Sid":    "SecretsManagerNamespace",
				"Effect": "Allow",
				"Action": []string{
					"secretsmanager:CreateSecret",
					"secretsmanager:DescribeSecret",
					"secretsmanager:GetSecretValue",
					"secretsmanager:PutSecretValue",
					"secretsmanager:TagResource",
				},
				"Resource": secretArn,
			},
			{
				"Sid":    "SSMParameterNamespace",
				"Effect": "Allow",
				"Action": []string{
					"ssm:GetParameter",
					"ssm:GetParameters",
				},
				"Resource": parameterArn,
			},
			{
				"Sid":    "Route53ZoneRecords",
				"Effect": "Allow",
				"Action": []string{
					"route53:ListResourceRecordSets",
					"route53:ChangeResourceRecordSets",
				},
				"Resource": hostedZoneArn,
			},
			{
				"Sid":      "Route53Changes",
				"Effect":   "Allow",
				"Action":   "route53:GetChange",
				"Resource": "arn:aws:route53:::change/*",
			},
			{
				"Sid":    "PulumiBackend",
				"Effect": "Allow",
				"Action": []string{
					"s3:GetObject",
					"s3:PutObject",
					"s3:DeleteObject",
					"s3:ListBucket",
					"s3:GetBucketLocation",
				},
				"Resource": []string{
					fmt.Sprintf("arn:aws:s3:::%s", backendBucket),
					fmt.Sprintf("arn:aws:s3:::%s/*", backendBucket),
				},
			},
			{
				"Sid":    "Logs",
				"Effect": "Allow",
				"Action": []string{
					"logs:CreateLogGroup",
					"logs:CreateLogStream",
					"logs:PutLogEvents",
					"logs:DescribeLogStreams",
				},
				"Resource": "*",
			},
		},
	}
	return policyJSON(policy)
}

func policyJSON(policy map[string]any) (string, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
