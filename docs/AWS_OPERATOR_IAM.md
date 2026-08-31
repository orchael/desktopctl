# AWS operator IAM

`ai-desktops` needs an AWS principal that can run control-plane calls from the CLI, Pulumi, and Packer. The foundation Pulumi stack creates an environment-specific IAM user, IAM role, access key, and Secrets Manager secret for that purpose.

An administrator profile is still required for the first `ai-desktops bootstrap` and `ai-desktops init-foundation` run. After foundation is applied, use the generated credentials and role profile for normal control-plane operations.

## Recommended secret name

Use the default foundation-stack secret name:

```text
/ai-desktops/<env>/control-plane/aws-operator
```

Examples:

```text
/ai-desktops/dev/control-plane/aws-operator
/ai-desktops/test/control-plane/aws-operator
/ai-desktops/prod/control-plane/aws-operator
```

The secret is environment-specific because each foundation stack uses its own `environment` value.

## What Pulumi creates

For environment `dev`, the foundation stack creates:

- IAM user: `ai-desktops-control-plane-dev`
- IAM role: `ai-desktops-control-plane-role-dev`
- IAM access key for the user
- User inline policy allowing only `sts:AssumeRole` into the environment role
- Role inline policy for AWS, Pulumi, and Packer control-plane calls
- Secrets Manager secret: `/ai-desktops/dev/control-plane/aws-operator`

The secret payload has this shape:

```json
{
  "aws_access_key_id": "AKIA...",
  "aws_secret_access_key": "...",
  "role_arn": "arn:aws:iam::<account-id>:role/ai-desktops-control-plane-role-dev",
  "user_arn": "arn:aws:iam::<account-id>:user/ai-desktops-control-plane-dev",
  "environment": "dev",
  "source_profile": "ai-desktops-dev-user",
  "role_profile": "ai-desktops-dev"
}
```

## Create the credentials

Run foundation with an administrator-capable AWS profile:

```bash
ai-desktops bootstrap
ai-desktops init-foundation --env dev
```

`init-foundation` prints the operator secret name after the stack applies.

To override the default secret name outside the CLI, set this Pulumi config value in `infra/pulumi/foundation` before applying the stack:

```bash
pulumi config set operatorCredentialsSecretName /ai-desktops/dev/control-plane/aws-operator
```

The default is recommended unless you need to integrate with an existing naming convention.

## Configure local AWS profiles

Read the generated secret with the administrator profile:

```bash
aws secretsmanager get-secret-value \
  --profile admin \
  --secret-id /ai-desktops/dev/control-plane/aws-operator \
  --query SecretString \
  --output text
```

Put the access key on a source profile and use the role profile for `ai-desktops`:

```ini
[profile ai-desktops-dev-user]
aws_access_key_id = <aws_access_key_id from secret>
aws_secret_access_key = <aws_secret_access_key from secret>

[profile ai-desktops-dev]
role_arn = <role_arn from secret>
source_profile = ai-desktops-dev-user
region = us-east-2
```

Then set the app config to use the role profile:

```yaml
aws:
  region: us-east-2
  profile: ai-desktops-dev
```

Pulumi and the AWS SDK both use the same shared AWS profile chain, so the assumed role is used by `ai-desktops init-foundation`, `ai-desktops create`, lifecycle commands, and AMI builds. Keep `bootstrap` on an admin/bootstrap profile if the role has not yet been created or if the state bucket does not exist.

## Permission scope

The role policy covers the control-plane operations used by this repository:

- Pulumi S3 state backend access for the configured bucket.
- EC2 networking, instance, AMI, snapshot, and key-pair calls used by Pulumi, lifecycle commands, nested virtualization launches, and Packer.
- DynamoDB fleet and AMI history tables named `ai-desktops-*`.
- Route53 hosted-zone lookup and desktop record changes.
- Secrets Manager and SSM reads/writes under `/ai-desktops/*`.
- SSM Session Manager tunnel calls.
- IAM role and instance-profile management for `ai-desktops-*` resources.
- `iam:PassRole` for the desktop instance role created by the foundation stack.

The role is intentionally scoped to `ai-desktops-*` resource names where AWS supports practical resource scoping. EC2 and Route53 use broader resource scope because many of the required APIs either do not support narrow resource ARNs or need hosted-zone discovery before the exact zone ID is known.

## Validate

```bash
aws sts get-caller-identity --profile ai-desktops-dev
ai-desktops init-foundation --env dev --preview
```
