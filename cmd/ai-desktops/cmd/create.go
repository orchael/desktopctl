package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"time"

	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/provision"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/repo"
	"github.com/spf13/cobra"
)

var (
	createOwner         string
	createRepos         []string
	createSecrets       []string
	createPreview       bool
	createEnv           string
	createAMI           string
	createVolumeSize    int
	createSwapSize      int
	createAVDs          []string
	createNestedVirt    bool
	createNestedVirtSet bool // true when --nested-virtualization was explicitly passed
	createMobile        bool
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new persistent AI coding desktop",
	Long: `create provisions a remote EC2 instance configured as an AI coding desktop
with novnc-desktop (Elementary), ai-agent-bridge, and developer tooling.

The --github-owner flag sets the owner boundary for all repositories on this
desktop. When at least one --repo is provided the owner is inferred from the
first repository URL and --github-owner becomes optional. --github-owner is
required only when no --repo flags are given. Mixed-owner repositories are
rejected before any infrastructure is changed.`,
	Args: cobra.NoArgs,
	RunE: runCreate,
}

func init() {
	createCmd.Flags().StringVar(&createOwner, "github-owner", "", "GitHub organization or username (inferred from --repo when omitted)")
	createCmd.Flags().StringArrayVar(&createRepos, "repo", nil, "GitHub repository to clone (repeatable)")
	createCmd.Flags().StringArrayVar(&createSecrets, "secret", nil, "AWS Secrets Manager path whose JSON keys are injected into the ubuntu environment (repeatable)")
	createCmd.Flags().BoolVar(&createPreview, "preview", false, "preview infrastructure changes without applying")
	createCmd.Flags().StringVar(&createEnv, "env", "", "environment (prod|dev), overrides config")
	createCmd.Flags().StringVar(&createAMI, "ami", "", "override active AMI ID for this region (optional)")
	createCmd.Flags().IntVar(&createVolumeSize, "volume-size", 0, "root EBS volume size in GiB (default: config value, 100 if unset)")
	createCmd.Flags().IntVar(&createSwapSize, "swap-size", 0, "swap file size in GiB (default: 2× instance memory; 0 = auto; -1 = disable)")
	createCmd.Flags().StringArrayVar(&createAVDs, "avd", nil, "Android Virtual Device to create at boot: name:image[:device] (repeatable; quote the value to protect semicolons, e.g. --avd 'flutter_dev:system-images;android-35;google_apis;x86_64:pixel_6')")
	createCmd.Flags().BoolVar(&createNestedVirt, "nested-virtualization", false, "enable KVM nested virtualization (requires a supported Intel Nitro instance: c8i, m8i, r8i, c7i, m7i, r7i, i7i)")
	createCmd.Flags().BoolVar(&createMobile, "mobile", false, "shorthand for Flutter/Android development: enables nested virtualization, sets instance type to "+config.DefaultMobileInstanceType+" (if not overridden in config), and creates a default AVD ("+config.DefaultMobileAVDName+") when no --avd flags are given")
	rootCmd.AddCommand(createCmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	ctx := context.Background()

	// --mobile implies nested virtualization and a mobile-appropriate instance type.
	if createMobile {
		createNestedVirt = true
		createNestedVirtSet = true
		// Only upgrade the instance type when the operator hasn't set a specific type
		// in config (i.e. it's still the plain desktop default).
		if cfg.Desktop.InstanceType == config.DefaultInstanceType {
			cfg.Desktop.InstanceType = config.DefaultMobileInstanceType
		}
	}

	// --nested-virtualization flag overrides config when explicitly passed.
	if cmd.Flags().Changed("nested-virtualization") {
		createNestedVirtSet = true
	}
	nestedVirt := cfg.Desktop.NestedVirtualization
	if createNestedVirtSet {
		nestedVirt = createNestedVirt
	}

	if nestedVirt && !config.SupportsNestedVirt(cfg.Desktop.InstanceType) {
		return fmt.Errorf("nested virtualization requires a supported Intel Nitro instance type (c8i, m8i, r8i, c7i, m7i, r7i, i7i); got %q — set instance_type in config or use --mobile which defaults to %s", cfg.Desktop.InstanceType, config.DefaultMobileInstanceType)
	}

	// Fall back to config file owner when --github-owner not explicitly set.
	if createOwner == "" && cfg.GitHub.Owner != "" {
		createOwner = cfg.GitHub.Owner
	}

	// Validate repo inputs. Owner may be inferred from repos when createOwner is empty.
	repos, owner, err := parseAndValidateRepos(createOwner, createRepos)
	if err != nil {
		return err
	}

	// Owner is required; it must come from --github-owner, config, or be inferred from --repo.
	if owner == "" {
		return fmt.Errorf("--github-owner is required when no --repo is specified (or set github.owner in config)")
	}

	// Verify every repo is reachable before touching any infrastructure.
	for _, r := range repos {
		fmt.Fprintf(os.Stderr, "Checking repository %s ...\n", r)
		if err := r.CheckAccessible(ctx); err != nil {
			return err
		}
	}

	// Verify every --secret path exists in Secrets Manager before provisioning.
	if len(createSecrets) > 0 {
		awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
		if err != nil {
			return fmt.Errorf("load AWS config to validate secrets: %w", err)
		}
		for _, secretPath := range createSecrets {
			fmt.Fprintf(os.Stderr, "Checking secret %s ...\n", secretPath)
			ok, err := awsx.SecretExists(ctx, awsCfg, secretPath)
			if err != nil {
				return fmt.Errorf("check secret %s: %w", secretPath, err)
			}
			if !ok {
				return fmt.Errorf("secret %q not found in Secrets Manager (region %s)", secretPath, cfg.AWS.Region)
			}
		}
	}

	env := createEnv
	if env == "" {
		env = cfg.Fleet.Environment
	}

	zone, err := cfg.DNSZone()
	if err != nil {
		return err
	}

	if err := requireBackend(ctx); err != nil {
		return err
	}

	// Detect pre-baked AMI for this region (used for both request and cloud-init)
	amiID := ""
	if createAMI != "" {
		amiID = createAMI
	} else if cfg.Desktop.ActiveAMI != nil {
		if ami, ok := cfg.Desktop.ActiveAMI[cfg.AWS.Region]; ok {
			amiID = ami
		}
	}
	if amiID == "" {
		return fmt.Errorf("no AMI configured for region %s: run `ai-desktops ami build` first or supply --ami", cfg.AWS.Region)
	}

	// cfg.GitHub.GitHubSecret defaults to /ai-desktops/github/pat when github.owner
	// is absent from the config file. Re-derive from the effective owner so that
	// --github-owner on the CLI resolves to the correct path.
	gitHubSecret := cfg.GitHub.GitHubSecret
	if cfg.GitHub.Owner == "" {
		gitHubSecret = "/ai-desktops/" + owner + "/github"
	}

	req := &desktop.CreateRequest{
		GitHubOwner:   owner,
		Repos:         repoStrings(repos),
		Secrets:       createSecrets,
		InstanceType:  cfg.Desktop.InstanceType,
		NestedVirt:    nestedVirt,
		Zone:          zone,
		OperatorCIDR:  cfg.Desktop.OperatorCIDR,
		SSHKeyPath:    cfg.Desktop.SSHKeyPath,
		GitHubSecret:  gitHubSecret,
		BackendBucket: cfg.Pulumi.BackendBucket,
		Region:        cfg.AWS.Region,
		Profile:       cfg.AWS.Profile,
		AMIID:         amiID,
	}

	if err := req.Validate(); err != nil {
		return err
	}

	desktopID, err := desktop.GenerateID()
	if err != nil {
		return fmt.Errorf("generate desktop ID: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Creating desktop %s (env=%s, owner=%s, instance=%s) ...\n", desktopID, env, owner, cfg.Desktop.InstanceType)
	if nestedVirt {
		fmt.Fprintln(os.Stderr, "  Nested virtualization : enabled (KVM via NestedVirtualization=enabled)")
	}
	if len(req.AVDNames) > 0 {
		fmt.Fprintf(os.Stderr, "  AVDs                  : %s\n", strings.Join(req.AVDNames, ", "))
	}

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}

	// Read foundation stack outputs to get the subnet, SG, and instance profile
	// that the desktop stack requires.
	foundationWorkDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	foundationRef := pulumi.FoundationStackRef(backendURL, env, foundationWorkDir)
	foundationOutputs, err := runner.Outputs(ctx, foundationRef)
	if err != nil {
		return fmt.Errorf("read foundation stack outputs (run init-foundation first): %w", err)
	}
	if err := pulumi.ValidateFoundationOutputs(foundationOutputs); err != nil {
		return fmt.Errorf("foundation stack incomplete (run init-foundation first): %w", err)
	}

	desktopWorkDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "desktop")
	desktopRef := pulumi.DesktopStackRef(backendURL, desktopID, desktopWorkDir)

	volumeSize := createVolumeSize
	if volumeSize <= 0 {
		volumeSize = cfg.Desktop.VolumeSize
	}
	if volumeSize <= 0 {
		return fmt.Errorf("volume size must be a positive integer (got %d); set --volume-size or desktop.volume_size in config", volumeSize)
	}

	swapSizeGB, err := resolveSwapSize(createSwapSize, cfg.Desktop.InstanceType, volumeSize)
	if err != nil {
		return err
	}

	// Merge --avd flags with config-file defaults; CLI flags take precedence (replace, not append).
	// --mobile adds a default AVD when neither --avd nor config AVDs are set.
	avds := cfg.Desktop.AVDs
	if len(createAVDs) > 0 {
		parsed, err := parseAVDs(createAVDs)
		if err != nil {
			return err
		}
		avds = parsed
	} else if createMobile && len(avds) == 0 {
		avds = []config.AVDConfig{{
			Name:   config.DefaultMobileAVDName,
			Image:  config.DefaultMobileAVDImage,
			Device: config.DefaultMobileAVDDevice,
		}}
	}
	if len(avds) > 0 && amiID == "" {
		fmt.Fprintln(os.Stderr, "WARNING: --avd requires the Android SDK to be pre-installed in the AMI. "+
			"Run `ai-desktops ami build` to produce a compatible AMI, then set active_ami in your config.")
	}
	if len(avds) > 0 {
		names := make([]string, len(avds))
		for i, a := range avds {
			names[i] = a.Name
		}
		req.AVDNames = names
	}

	// Render cloud-init with PackagesPreInstalled set based on whether we have a pre-baked AMI.
	userData := ""
	hostname := desktop.Hostname(desktopID, zone)

	// Read the SSH public key so cloud-init can inject it into the ubuntu user's
	// authorized_keys.  A missing or unreadable key is non-fatal; the desktop
	// will still boot but SSH key-based login won't work.
	sshPubKey := ""
	if cfg.Desktop.SSHKeyPath != "" {
		if pubBytes, err := os.ReadFile(cfg.Desktop.SSHKeyPath + ".pub"); err == nil {
			sshPubKey = strings.TrimSpace(string(pubBytes))
		}
	}

	bootCfg := &provision.BootstrapConfig{
		DesktopID:            desktopID,
		Hostname:             hostname,
		GitHubOwner:          owner,
		Repos:                req.Repos,
		WorkspacePath:        "/workspace",
		BridgePort:           cfg.Agent.BridgePort,
		NoVNCHTTPPort:        provision.DefaultNoVNCHTTPPort,
		NoVNCHTTPSPort:       provision.DefaultNoVNCHTTPSPort,
		CertbotEmail:         "admin@orchael.ai",
		GitHubSecretPath:     gitHubSecret,
		AgentSecretPath:      cfg.GitHub.AgentSecret,
		DesktopSecretPaths:   createSecrets,
		AWSRegion:            cfg.AWS.Region,
		Environment:          env,
		PackagesPreInstalled: amiID != "",
		SSHPublicKey:         sshPubKey,
		GitUserName:          cfg.GitHub.GitUserName,
		GitUserEmail:         cfg.GitHub.GitUserEmail,
		SwapSizeGB:           swapSizeGB,
		AVDs:                 avds,
	}
	var renderErr error
	userData, renderErr = provision.RenderCloudInit(bootCfg)
	if renderErr != nil {
		return fmt.Errorf("render cloud-init: %w", renderErr)
	}

	stackCfg := pulumi.DesktopConfig(
		cfg.AWS.Region, desktopID, owner, zone, cfg.Desktop.InstanceType,
		foundationOutputs[pulumi.OutputSubnetID],
		foundationOutputs[pulumi.OutputSGID],
		foundationOutputs[pulumi.OutputInstanceProfile],
		cfg.Desktop.SSHKeyName,
		req.Repos,
		cfg.Agent.BridgePort,
		volumeSize,
		amiID,
		userData,
		env,
		nestedVirt,
	)

	if createPreview {
		fmt.Printf("Desktop ID    : %s\n", desktopID)
		fmt.Printf("Zone          : %s\n", zone)
		fmt.Printf("Hostname      : %s\n", hostname)
		fmt.Printf("Instance type : %s\n", cfg.Desktop.InstanceType)
		fmt.Printf("Nested virt   : %v\n", nestedVirt)
		fmt.Printf("Repos         : %v\n", createRepos)
		if len(req.AVDNames) > 0 {
			fmt.Printf("AVDs          : %s\n", strings.Join(req.AVDNames, ", "))
		}
		if amiID != "" {
			fmt.Printf("AMI           : %s (pre-baked, ~1min boot)\n", amiID)
		} else {
			fmt.Printf("AMI           : none (cloud-init bootstrap, ~5-10min boot)\n")
		}
		return runner.Preview(ctx, desktopRef, stackCfg, os.Stderr)
	}

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	mgr := desktop.NewManager(s)

	if err := mgr.CreateRecord(ctx, desktopID, req); err != nil {
		return fmt.Errorf("create fleet record: %w", err)
	}

	// Run pulumi up and update the record with outputs.
	ref := desktopRef
	outputs, err := runner.Up(ctx, ref, stackCfg, os.Stderr)
	if err != nil {
		_ = mgr.RecordFailure(ctx, desktopID, "create", err.Error())
		return fmt.Errorf("pulumi up: %w", err)
	}

	if err := mgr.UpdateFromOutputs(ctx, desktopID, outputs); err != nil {
		return fmt.Errorf("update fleet record: %w", err)
	}

	// Enable nested virtualization via ModifyInstanceCpuOptions (stop → modify → start).
	// The Pulumi AWS Go SDK does not yet expose the NestedVirtualization CpuOptions
	// field, so we apply it post-deploy using the AWS SDK directly.
	if nestedVirt {
		instanceID := outputs[pulumi.OutputInstanceID]
		if instanceID == "" {
			return fmt.Errorf("nested virtualization: instance ID not found in stack outputs")
		}
		if err := enableNestedVirt(ctx, instanceID, cfg.AWS.Region, cfg.AWS.Profile); err != nil {
			return fmt.Errorf("enable nested virtualization on %s: %w", instanceID, err)
		}
	}

	if err := mgr.MarkReady(ctx, desktopID, "provisioned"); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}

	nestedVirtStr := "false"
	if nestedVirt {
		nestedVirtStr = "true"
	}
	result := map[string]string{
		"desktop_id":            desktopID,
		"hostname":              hostname,
		"novnc_url":             desktop.NoVNCURL(hostname),
		"ssh_target":            desktop.SSHTarget(hostname),
		"stack":                 desktop.StackName(desktopID),
		"ami_id":                amiID,
		"region":                cfg.AWS.Region,
		"instance_type":         cfg.Desktop.InstanceType,
		"nested_virtualization": nestedVirtStr,
		"avd_names":             strings.Join(req.AVDNames, ", "),
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID    : %s\n", result["desktop_id"])
	fmt.Printf("Hostname      : %s\n", result["hostname"])
	fmt.Printf("Desktop URL   : %s\n", result["novnc_url"])
	fmt.Printf("SSH target    : %s\n", result["ssh_target"])
	fmt.Printf("Instance type : %s\n", result["instance_type"])
	fmt.Printf("Nested virt   : %s\n", result["nested_virtualization"])
	if result["avd_names"] != "" {
		fmt.Printf("AVDs          : %s\n", result["avd_names"])
	}
	fmt.Printf("AMI ID        : %s\n", result["ami_id"])
	fmt.Printf("Region        : %s\n", result["region"])
	return nil
}

func parseAndValidateRepos(owner string, rawRepos []string) ([]*repo.Repo, string, error) {
	if len(rawRepos) == 0 {
		return nil, owner, nil
	}
	repos, detectedOwner, err := repo.ParseAll(rawRepos)
	if err != nil {
		return nil, "", err
	}
	if owner == "" {
		// Infer owner from the first repo URL when --github-owner was not provided.
		return repos, detectedOwner, nil
	}
	if !strings.EqualFold(detectedOwner, owner) {
		return nil, "", fmt.Errorf("repository owner %q does not match --github-owner %q", detectedOwner, owner)
	}
	// Return the canonical owner from the repo URL so downstream values
	// (secret paths, config) are consistent regardless of flag casing.
	return repos, detectedOwner, nil
}

func repoStrings(repos []*repo.Repo) []string {
	if len(repos) == 0 {
		return nil
	}
	out := make([]string, len(repos))
	for i, r := range repos {
		out[i] = r.String()
	}
	return out
}

// avdNameRe allows alphanumerics, underscores, and hyphens — safe for shell args.
var avdNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// avdImageRe allows the system-images;<api>;<tag>;<abi> format used by sdkmanager.
var avdImageRe = regexp.MustCompile(`^[A-Za-z0-9_;.-]+$`)

// parseAVDs parses --avd flag values in name:image[:device] format.
func parseAVDs(specs []string) ([]config.AVDConfig, error) {
	avds := make([]config.AVDConfig, 0, len(specs))
	for _, spec := range specs {
		parts := strings.SplitN(spec, ":", 3)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid --avd %q: must be name:image or name:image:device", spec)
		}
		if !avdNameRe.MatchString(parts[0]) {
			return nil, fmt.Errorf("invalid --avd name %q: only alphanumerics, underscores, and hyphens are allowed", parts[0])
		}
		if !avdImageRe.MatchString(parts[1]) {
			return nil, fmt.Errorf("invalid --avd image %q: only alphanumerics and the characters _;.- are allowed", parts[1])
		}
		avd := config.AVDConfig{Name: parts[0], Image: parts[1]}
		if len(parts) == 3 {
			if parts[2] != "" && !avdNameRe.MatchString(parts[2]) {
				return nil, fmt.Errorf("invalid --avd device %q: only alphanumerics, underscores, and hyphens are allowed", parts[2])
			}
			avd.Device = parts[2]
		}
		avds = append(avds, avd)
	}
	return avds, nil
}

// enableNestedVirt stops the instance, sets NestedVirtualization=enabled via
// ModifyInstanceCpuOptions, then restarts it. This is necessary because the
// Pulumi AWS Go SDK does not yet expose the NestedVirtualization CpuOptions field.
func enableNestedVirt(ctx context.Context, instanceID, region, profile string) error {
	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	ec2Client := ec2sdk.NewFromConfig(awsCfg)

	fmt.Fprintf(os.Stderr, "Nested virt: stopping %s to apply NestedVirtualization=enabled ...\n", instanceID)
	_, err = ec2Client.StopInstances(ctx, &ec2sdk.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("stop instance: %w", err)
	}

	stopWaiter := ec2sdk.NewInstanceStoppedWaiter(ec2Client)
	if err := stopWaiter.Wait(ctx, &ec2sdk.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for instance stopped: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Nested virt: applying NestedVirtualization=enabled ...\n")
	_, err = ec2Client.ModifyInstanceCpuOptions(ctx, &ec2sdk.ModifyInstanceCpuOptionsInput{
		InstanceId:           &instanceID,
		NestedVirtualization: ec2types.NestedVirtualizationSpecificationEnabled,
	})
	if err != nil {
		// Start the instance back up even if modification fails.
		_, _ = ec2Client.StartInstances(ctx, &ec2sdk.StartInstancesInput{
			InstanceIds: []string{instanceID},
		})
		return fmt.Errorf("modify cpu options: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Nested virt: restarting %s ...\n", instanceID)
	_, err = ec2Client.StartInstances(ctx, &ec2sdk.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("start instance: %w", err)
	}

	runWaiter := ec2sdk.NewInstanceRunningWaiter(ec2Client)
	if err := runWaiter.Wait(ctx, &ec2sdk.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 5*time.Minute); err != nil {
		return fmt.Errorf("wait for instance running: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Nested virt: instance %s running with NestedVirtualization=enabled\n", instanceID)
	return nil
}

// resolveSwapSize determines the swap file size in GiB.
//
// flag values:
//
//	-1 — swap disabled (returns 0, no error)
//	 0 — auto: 2× instance memory (falls back to 4 GiB for unknown types)
//	>0 — explicit size in GiB
//
// An error is returned when the swap would leave fewer than 20 GiB on the root
// volume for the OS and application data.
func resolveSwapSize(flagValue int, instanceType string, volumeSizeGiB int) (int, error) {
	if flagValue < -1 {
		return 0, fmt.Errorf("invalid --swap-size %d: use -1 to disable swap, 0 for auto, or a positive integer for an explicit size in GiB", flagValue)
	}
	if flagValue == -1 {
		return 0, nil
	}
	swapSizeGiB := flagValue
	if swapSizeGiB == 0 {
		memGiB := provision.InstanceMemoryGiB(instanceType)
		if memGiB == 0 {
			// Unknown instance type: default to 4 GiB so swap is still created.
			memGiB = 4
		}
		swapSizeGiB = 2 * memGiB
	}
	const minOSReservedGiB = 20
	if swapSizeGiB+minOSReservedGiB > volumeSizeGiB {
		return 0, fmt.Errorf(
			"swap size %d GiB + minimum OS reservation %d GiB exceeds root volume size %d GiB; "+
				"increase --volume-size or reduce --swap-size (use -1 to disable swap)",
			swapSizeGiB, minOSReservedGiB, volumeSizeGiB,
		)
	}
	return swapSizeGiB, nil
}
