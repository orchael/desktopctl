# Android Emulator on AWS: Options 1 & 2

The x86_64 Android emulator requires KVM hardware acceleration. The current `t3.medium` build
instance does not expose KVM, so `flutter emulators --launch` crashes immediately. Two options
are available that keep the existing Packer/Ansible workflow intact.

---

## Option 1 — Nested virtualization on a Nitro x86_64 instance

AWS enabled nested virtualization on standard Nitro-based EC2 instances in February 2026.
Passing `NestedVirtualization=enabled` in `--cpu-options` makes `/dev/kvm` available inside
the instance. The existing x86_64 AVD and system image work without any changes.

### What to change

**`packer/ubuntu-desktop.pkr.hcl`** — change `instance_type` and add `cpu_options`:

```hcl
source "amazon-ebs" "ubuntu" {
  instance_type = "c7i.xlarge"   # was t3.medium

  cpu_options {
    core_count       = 2
    threads_per_core = 2
    amd_svm_enabled  = false
  }

  # Enable nested virtualization so the Android emulator can use /dev/kvm
  # Supported on C7i, C8i, M7i, M8i, R7i, R8i and their flex/id variants.
  # https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
  run_tags = {}
}
```

> **Note:** Packer's `amazon-ebs` source does not yet expose a `cpu_options` block directly.
> The workaround is to launch the builder via a launch template that has
> `NestedVirtualization=enabled` set, or to use `override_json` in the source config.
> The simplest path: add a `temporary_iam_instance_profile_policy_document` and use the
> `run_volume_tags` / `launch_block_device_mappings` to pass a launch template ARN that
> has the nested virt option baked in.
>
> Alternatively, use the AWS CLI `run-instances` equivalent by creating a launch template:
>
> ```bash
> aws ec2 create-launch-template \
>   --launch-template-name packer-nested-virt \
>   --launch-template-data '{
>     "InstanceType": "c7i.xlarge",
>     "CpuOptions": {"AmdSevSnp": "disabled"},
>     "Placement": {},
>     "MetadataOptions": {"HttpTokens": "required"}
>   }'
> # Then reference the template in the Packer source:
> # launch_template { ... }
> ```

**Packer source change (launch_template approach):**

```hcl
source "amazon-ebs" "ubuntu" {
  ami_name      = "ai-desktops-${var.bridgectl_version}-{{timestamp}}"
  instance_type = "c7i.xlarge"
  region        = var.aws_region
  source_ami    = var.source_ami

  launch_template {
    launch_template_name    = "packer-nested-virt"
    launch_template_version = "$Latest"
  }
  # ... rest of config unchanged
}
```

### No playbook changes required

The existing system image (`system-images;android-35;google_apis;x86_64`) and AVD remain
correct. KVM will be detected automatically by the emulator at boot.

### Acceptance test

```bash
# On the built instance:
ls /dev/kvm                          # must exist
flutter emulators                    # must list flutter_dev
flutter emulators --launch flutter_dev
adb wait-for-device
flutter devices                      # must show emulator-5554
```

### Cost

| Instance | vCPU | RAM | On-demand (us-east-1) |
|---|---|---|---|
| `t3.medium` (current) | 2 | 4 GB | ~$0.0416/hr |
| `c7i.xlarge` (option 1) | 4 | 8 GB | ~$0.179/hr |

The Packer build runs for ~20 min, so the cost delta is about $0.045 per AMI build.

---

## Option 2 — ARM64 instance with ARM64 Android system image

On an ARM64 host (e.g., `c7g.xlarge` / Graviton3), the Android emulator runs ARM64 system
images at native speed — no KVM needed because there is no instruction-set translation.

### What to change

**`packer/ubuntu-desktop.pkr.hcl`** — change instance type and source AMI variable to ARM64:

```hcl
source "amazon-ebs" "ubuntu" {
  instance_type = "c7g.xlarge"   # Graviton3, ARM64
  # source_ami must be an arm64 Ubuntu 24.04 AMI
  # The CLI's ami resolve step must select arm64 AMIs for this config
}
```

**`packer/variables.pkrvars.hcl`** — no changes needed; `source_ami` is resolved at build time.

**`packer/playbook.yml`** — change the system image package:

```yaml
- name: Install Android SDK components
  ansible.builtin.shell: |
    runuser -l ubuntu -c 'ANDROID_HOME=/opt/android-sdk /opt/android-sdk/cmdline-tools/latest/bin/sdkmanager \
      "platform-tools" \
      "emulator" \
      "build-tools;35.0.1" \
      "platforms;android-35" \
      "system-images;android-35;google_apis;arm64-v8a"'   # was x86_64
```

**`packer/playbook.yml`** — change the AVD creation task:

```yaml
- name: Create flutter_dev AVD
  ansible.builtin.shell: |
    runuser -l ubuntu -c '... avdmanager create avd \
      -k "system-images;android-35;google_apis;arm64-v8a" ...'
```

### Trade-offs

| | Option 1 (x86_64 + KVM) | Option 2 (ARM64 native) |
|---|---|---|
| KVM required | Yes (via nested virt) | No |
| System image availability | Full (x86_64 has more images) | Good (arm64 Google APIs available) |
| Google Play support | Yes (`google_apis_playstore`) | Limited |
| Flutter compatibility | Full | Full |
| Instance cost | ~$0.179/hr (c7i.xlarge) | ~$0.145/hr (c7g.xlarge) |
| Source AMI | x86_64 Ubuntu | arm64 Ubuntu (different AMI ID) |
| Packer complexity | Launch template for nested virt | Source AMI must be arm64 |

### Acceptance test

```bash
# On the built instance:
uname -m                             # must print aarch64
flutter emulators                    # must list flutter_dev
flutter emulators --launch flutter_dev
adb wait-for-device
flutter devices                      # must show emulator-5554
```

---

## Testing plan

1. **Branch `feat/flutter-sdk-support`** (current) — baseline with `build-tools;35.0.1` and
   `/etc/environment` fixes applied. Build a new AMI from this branch first to confirm the
   toolchain validates without KVM changes.

2. **Branch `feat/android-emulator-nested-virt`** — implement Option 1.
   - Create a launch template with `NestedVirtualization=enabled` in the target AWS account.
   - Update `packer/ubuntu-desktop.pkr.hcl` to use `c7i.xlarge` and reference the template.
   - Build AMI and run acceptance tests.

3. **Branch `feat/android-emulator-arm64`** (optional, if Option 1 has issues) — implement
   Option 2.
   - Update instance type, system image, and AVD creation in the playbook.
   - Resolve an arm64 Ubuntu source AMI.
   - Build AMI and run acceptance tests.

Pick Option 1 first — it requires fewer playbook changes and keeps the x86_64 toolchain
that the rest of the Flutter ecosystem is optimised for.
