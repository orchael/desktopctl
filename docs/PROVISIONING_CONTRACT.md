# desktopctl worker contract (initial version)

`orchael/desktops` invokes the versioned `ai-desktops` CLI from a background worker. This is the cross-language boundary: the worker does not implement AWS resources or Pulumi. The CLI still works independently for an operator. Desktops owns account records and durable job reconciliation.

## Credentials and account identity

The worker first assumes the customer's role with AWS STS, checks that the returned account ID matches the selected connection, and starts a child process with `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and `AWS_SESSION_TOKEN` in its environment. These are temporary session credentials. The worker must provide a minimal environment with no `AWS_PROFILE` and a desktopctl config whose `aws.profile` is empty. Both the AWS SDK and the Pulumi subprocess then consume the same temporary credentials. Do not place credentials in arguments, Pulumi config, stdout, run records, or a persisted config file. Refresh/reassume before expiry; a single session must last longer than the bounded operation or the worker must resume safely.

```sh
ai-desktops --config /run/desktopctl/config.yaml --region ca-central-1 aws validate --account-id 123456789012 --json
```

`aws validate` uses STS `GetCallerIdentity` and returns `account_id`, `arn`, and `region`. The ARN identifies the temporary role session, not a stored customer key.

## Create and lifecycle

The worker assigns a stable `d-` plus eight lowercase hexadecimal character desktop ID before queueing. It records the ID and uses it for every attempt. `create --desktop-id` rejects an existing fleet record; DynamoDB also conditionally rejects concurrent record creation. On a Pulumi failure, the CLI destroys the attempted stack. If stack or prelaunched-instance cleanup fails, the fleet record stays failed so an automated retry cannot allocate another instance without operator reconciliation. On successful cleanup, the record is removed and the same ID may be retried. Desktops must lock one operation per desktop and inspect the stack and live instance before retrying after an interrupted process.

```sh
ai-desktops --config /run/desktopctl/config.yaml --region ca-central-1 \
  create --desktop-id d-0123abcd --name test-desktop \
  --github-owner example --desktop-profile example/developer-profile \
  --instance-type t3.large --json

ai-desktops --config /run/desktopctl/config.yaml status d-0123abcd --json
ai-desktops --config /run/desktopctl/config.yaml stop d-0123abcd
ai-desktops --config /run/desktopctl/config.yaml start d-0123abcd
ai-desktops --config /run/desktopctl/config.yaml destroy d-0123abcd --force
```

`--profile` remains the AWS shared-profile flag for CLI compatibility. `--desktop-profile` is a repository reference; there is no built-in `developer` alias yet. `--instance-type` uses desktopctl's existing EC2 size model. `destroy` aliases `terminate`. The worker should consume `--json` output where available and persist an allowlisted diagnostic summary, not raw process logs that may contain operator-controlled values.

The CLI create path waits for cloud-init completion when a desktop profile or `--bootstrap-packages` is selected. The Desktops worker additionally checks the bridgectl binary and desktop service over SSM before reporting tenant `READY`. A subsequent extraction can put create, readiness, start, stop, and destroy behind one Go provisioning service shared by the CLI and job adapter.

## Configuration prerequisites

The standard CLI path requires desktopctl's Pulumi S3 backend, fleet DynamoDB table, foundation stack, Route53 zone, and a compatible AMI in the selected region. `bootstrap`, `init-foundation`, and AMI selection remain explicit operator steps for that path. The SaaS worker path below performs these steps through desktopctl without a DNS zone. The customer's AssumeRole policy must grant only the AWS actions those steps and the chosen lifecycle path require; see [AWS operator IAM](AWS_OPERATOR_IAM.md). An IAM role alone does not make the provisioning stack ready to run.

## AssumeRole SaaS mode

For customer accounts without a desktop DNS zone, set `saas_mode: true` and `saas_instance_profile_name` in the generated desktopctl config and use `init-foundation --saas`. This keeps the original CLI foundation behavior for existing operators while omitting Route53 lookup, inbound desktop ports, EFS resources used only by optional shared workspaces, and the foundation's long-lived operator IAM user/key/secret. `create --no-dns --bootstrap-packages --ami <novnc-desktop base AMI>` reuses the same Pulumi desktop program and cloud-init renderer, skips Route53 and GitHub secret setup, and installs agent packages at boot. A plain Ubuntu server AMI has no desktop service and cannot pass readiness. The base AMI must be launchable by the customer account in the selected region. The resulting hostname is a placeholder and is not a public session URL; Bridge enrollment remains the future remote-access seam.

The Desktops worker calls `bootstrap`, `init-foundation --saas`, then `create` with a stable ID. These commands are idempotent at the foundation and backend layers. The worker uses a per-organization state bucket and fleet table in the customer's account. Temporary credentials remain in the child-process environment. A customer role must permit the exact bootstrap, foundation, lifecycle, and SSM readiness APIs used by this path; the SaaS role template in Desktops owns that policy.
