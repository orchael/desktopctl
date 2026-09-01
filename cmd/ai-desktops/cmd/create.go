package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secretsmanagertypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/orchael/ai-desktops/internal/awsx"
	"github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/desktop"
	"github.com/orchael/ai-desktops/internal/provision"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/repo"
	"github.com/orchael/ai-desktops/internal/store"
	"github.com/spf13/cobra"
)

var (
	createOwner             string
	createName              string
	createRepos             []string
	createSecrets           []string
	createPreview           bool
	createWorkspaceMode     string
	createWorkspaceName     string
	createEnv               string
	createAMI               string
	createVolumeSize        int
	createSwapSize          int
	createAVDs              []string
	createNestedVirt        bool
	createNestedVirtSet     bool // true when --nested-virtualization was explicitly passed
	createMobile            bool
	createInstanceType      string
	createInstanceTypes     []string
	createSpot              bool
	createSpotMaxPrice      string
	createTimeout           time.Duration
	createTailscale         bool
	createTailscaleNet      string
	createStepCA            string
	createStepCAProv        string
	createStepCAFP          string
	createStepCAClients     []string
	createNPMGitHubScopes   []string
	createNoNPMGitHubScopes bool
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new persistent AI coding desktop",
	Long: `create provisions a remote EC2 instance configured as an AI coding desktop
with novnc-desktop (Elementary), bridgectl, and developer tooling.

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
	createCmd.Flags().StringVar(&createName, "name", "", "desktop name, unique among non-terminated desktops in the environment")
	createCmd.Flags().StringArrayVar(&createRepos, "repo", nil, "GitHub repository to clone (repeatable)")
	createCmd.Flags().StringArrayVar(&createSecrets, "secret", nil, "AWS Secrets Manager path whose JSON keys are injected into the ubuntu environment (repeatable)")
	createCmd.Flags().BoolVar(&createPreview, "preview", false, "preview infrastructure changes without applying")
	createCmd.Flags().StringVar(&createWorkspaceMode, "workspace-mode", workspaceModeLocal, "workspace storage mode (local|efs)")
	createCmd.Flags().StringVar(&createWorkspaceName, "workspace-name", "", "existing retained EFS workspace to mount at /workspace")
	createCmd.Flags().StringVar(&createEnv, "env", "", "environment (prod|dev), overrides config")
	createCmd.Flags().StringVar(&createAMI, "ami", "", "override active AMI ID for this region (optional)")
	createCmd.Flags().IntVar(&createVolumeSize, "volume-size", 0, "root EBS volume size in GiB (default: config value, 100 if unset)")
	createCmd.Flags().IntVar(&createSwapSize, "swap-size", 0, "swap file size in GiB (default: min(2× instance memory, 32); 0 = auto; -1 = disable)")
	createCmd.Flags().StringArrayVar(&createAVDs, "avd", nil, "Android Virtual Device to create at boot: name:image[:device] (repeatable; quote the value to protect semicolons, e.g. --avd 'flutter_dev:system-images;android-35;google_apis;x86_64:pixel_6')")
	createCmd.Flags().BoolVar(&createNestedVirt, "nested-virtualization", false, "enable KVM nested virtualization (requires a supported Intel Nitro instance: c8i, m8i, r8i, c7i, m7i, r7i, i7i)")
	createCmd.Flags().BoolVar(&createMobile, "mobile", false, "shorthand for Flutter/Android development: enables nested virtualization, sets instance type to "+config.DefaultMobileInstanceType+" (if not overridden in config), and creates a default AVD ("+config.DefaultMobileAVDName+") when no --avd flags are given")
	createCmd.Flags().StringVar(&createInstanceType, "instance-type", "", "EC2 instance type (overrides config and --mobile default, e.g. m8i.xlarge, c7i.xlarge, m7i.large)")
	createCmd.Flags().StringSliceVar(&createInstanceTypes, "instance-types", nil, "EC2 Spot instance types to consider (comma-separated; repeatable; default: "+strings.Join(config.DefaultSpotInstanceTypes, ",")+")")
	createCmd.Flags().BoolVar(&createSpot, "spot", false, "launch the desktop as a persistent Spot instance that stops on interruption")
	createCmd.Flags().StringVar(&createSpotMaxPrice, "spot-max-price", "", "maximum hourly Spot price in USD (requires --spot; default is AWS on-demand ceiling)")
	createCmd.Flags().DurationVar(&createTimeout, "create-timeout", 5*time.Minute, "maximum time to wait for each infrastructure create attempt before cleaning up")
	createCmd.Flags().BoolVar(&createTailscale, "tailscale", false, "attach the desktop to Tailscale using --tailscale-network or network.tailscale_network from config")
	createCmd.Flags().StringVar(&createTailscaleNet, "tailscale-network", "", "Tailscale tailnet/network name; also enables Tailscale and requires TAILSCALE_AUTHKEY or an existing integration secret")
	createCmd.Flags().StringVar(&createStepCA, "step-ca", "", "bootstrap bridgectl trust and host certificate from this step-ca DNS name; requires STEP_CA_PROVISIONER_PASSWORD or an existing integration secret, and a fingerprint via --step-ca-fingerprint, pki.step_ca_fingerprint, or STEP_CA_FINGERPRINT")
	createCmd.Flags().StringVar(&createStepCAProv, "step-ca-provisioner", "admin", "step-ca provisioner name used with --step-ca")
	createCmd.Flags().StringVar(&createStepCAFP, "step-ca-fingerprint", "", "step-ca root certificate fingerprint; required when --step-ca is set (may also be supplied via pki.step_ca_fingerprint or STEP_CA_FINGERPRINT)")
	createCmd.Flags().StringArrayVar(&createStepCAClients, "step-ca-client", nil, "remote bridgectl client to trust at startup: issuer=<name>,public-key-path=<path>[,required=true] (repeatable; requires step-ca)")
	createCmd.Flags().StringArrayVar(&createNPMGitHubScopes, "npm-github-scope", nil, "npm package scope to resolve from GitHub Packages on the desktop, e.g. @myorg (repeatable; default: github.npm_github_scopes)")
	createCmd.Flags().BoolVar(&createNoNPMGitHubScopes, "no-npm-github-scopes", false, "ignore github.npm_github_scopes from config for this desktop")
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

	// --instance-type overrides config and --mobile's default.
	if createInstanceType != "" {
		cfg.Desktop.InstanceType = createInstanceType
	}

	// --nested-virtualization flag overrides config when explicitly passed.
	if cmd.Flags().Changed("nested-virtualization") {
		createNestedVirtSet = true
	}
	nestedVirt := cfg.Desktop.NestedVirtualization
	if createNestedVirtSet {
		nestedVirt = createNestedVirt
	}

	if createSpotMaxPrice != "" && !createSpot {
		return fmt.Errorf("--spot-max-price requires --spot")
	}
	if err := validateSpotMaxPrice(createSpotMaxPrice); err != nil {
		return err
	}
	if createTimeout <= 0 {
		return fmt.Errorf("--create-timeout must be positive")
	}
	createWorkspaceMode = strings.TrimSpace(createWorkspaceMode)
	if createWorkspaceMode == "" {
		createWorkspaceMode = workspaceModeLocal
	}
	if createWorkspaceMode != workspaceModeLocal && createWorkspaceMode != workspaceModeEFS {
		return fmt.Errorf("--workspace-mode must be local or efs")
	}
	if createWorkspaceMode == workspaceModeLocal && strings.TrimSpace(createWorkspaceName) != "" {
		return fmt.Errorf("--workspace-name requires --workspace-mode efs")
	}
	if createWorkspaceMode == workspaceModeEFS {
		if err := validateWorkspaceName(createWorkspaceName); err != nil {
			return fmt.Errorf("--workspace-name: %w", err)
		}
	}
	createName = strings.TrimSpace(createName)
	if createName != "" {
		if err := validateDesktopName(createName); err != nil {
			return fmt.Errorf("--name: %w", err)
		}
	}
	marketType := store.MarketOnDemand
	if createSpot {
		marketType = store.MarketSpot
	}
	instanceTypes, err := resolveCreateInstanceTypes(resolveCreateInstanceTypesInput{
		marketType:             marketType,
		configInstanceType:     cfg.Desktop.InstanceType,
		configInstanceTypes:    cfg.Desktop.InstanceTypes,
		flagInstanceTypeSet:    cmd.Flags().Changed("instance-type"),
		flagInstanceTypes:      createInstanceTypes,
		flagInstanceTypesSet:   cmd.Flags().Changed("instance-types"),
		nestedVirtualization:   nestedVirt,
		mobileDefaultInstance:  config.DefaultMobileInstanceType,
		defaultInstanceType:    config.DefaultInstanceType,
		defaultSpotInstanceSet: config.DefaultSpotInstanceTypes,
	})
	if err != nil {
		return err
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
	integrations, err := resolveCreateIntegrations(resolveCreateIntegrationsInput{
		tailscale:               createTailscale,
		tailscaleNetwork:        createTailscaleNet,
		tailscaleNetworkSet:     cmd.Flags().Changed("tailscale-network"),
		stepCA:                  createStepCA,
		stepCASet:               cmd.Flags().Changed("step-ca"),
		stepCAProvisioner:       createStepCAProv,
		stepCAProvisionerSet:    cmd.Flags().Changed("step-ca-provisioner"),
		stepCAFingerprint:       createStepCAFP,
		stepCAFingerprintSet:    cmd.Flags().Changed("step-ca-fingerprint"),
		configTailscaleNetwork:  cfg.Network.TailscaleNetwork,
		configStepCA:            cfg.PKI.StepCAServer,
		configStepCAProvisioner: cfg.PKI.StepCAProvisioner,
		configStepCAFingerprint: cfg.PKI.StepCAFingerprint,
		envStepCAFingerprint:    strings.TrimSpace(os.Getenv("STEP_CA_FINGERPRINT")),
	})
	if err != nil {
		return err
	}
	tailscaleNetwork := integrations.tailscaleNetwork
	stepCAServer := integrations.stepCA
	stepCAProvisioner := integrations.stepCAProvisioner
	stepCAFingerprint := integrations.stepCAFingerprint
	var stepCAClients []provision.StepCAClient
	if stepCAServer != "" || len(createStepCAClients) > 0 {
		var err error
		stepCAClients, err = resolveStepCAClients(cfg.PKI.StepCAClients, createStepCAClients)
		if err != nil {
			return err
		}
		if len(stepCAClients) > 0 && stepCAServer == "" {
			return fmt.Errorf("step-ca clients require --step-ca or pki.step_ca_server")
		}
	}

	env := createEnv
	if env == "" {
		env = cfg.Fleet.Environment
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

	tailscaleSecretPath := ""
	stepCASecretPath := ""
	if integrations.tailscaleEnabled || integrations.stepCAEnabled {
		var err error
		tailscaleSecretPath, stepCASecretPath, err = prepareIntegrationSecrets(ctx, createPreview, owner, env, cfg.AWS.Region, cfg.AWS.Profile, cfg.Operator.Secret, tailscaleNetwork, stepCAServer)
		if err != nil {
			return err
		}
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
		DesktopName:   createName,
		GitHubOwner:   owner,
		Repos:         repoStrings(repos),
		Secrets:       createSecrets,
		TailscaleNet:  tailscaleNetwork,
		StepCAServer:  stepCAServer,
		InstanceType:  instanceTypes[0],
		NestedVirt:    nestedVirt,
		MarketType:    marketType,
		Zone:          zone,
		OperatorCIDR:  cfg.Desktop.OperatorCIDR,
		SSHKeyPath:    cfg.Desktop.SSHKeyPath,
		GitHubSecret:  gitHubSecret,
		BackendBucket: cfg.Pulumi.BackendBucket,
		Region:        cfg.AWS.Region,
		Environment:   env,
		Profile:       cfg.AWS.Profile,
		AMIID:         amiID,
		WorkspaceMode: createWorkspaceMode,
		WorkspaceName: createWorkspaceName,
	}

	if err := req.Validate(); err != nil {
		return err
	}

	desktopID, err := desktop.GenerateID()
	if err != nil {
		return fmt.Errorf("generate desktop ID: %w", err)
	}

	if marketType == store.MarketSpot {
		fmt.Fprintf(os.Stderr, "Creating desktop %s (env=%s, owner=%s, instances=%s, market=%s) ...\n", desktopID, env, owner, strings.Join(instanceTypes, ","), marketType)
	} else {
		fmt.Fprintf(os.Stderr, "Creating desktop %s (env=%s, owner=%s, instance=%s, market=%s) ...\n", desktopID, env, owner, instanceTypes[0], marketType)
	}
	if nestedVirt {
		fmt.Fprintln(os.Stderr, "  Nested virtualization : enabled (KVM via NestedVirtualization=enabled)")
	}
	if marketType == store.MarketSpot {
		fmt.Fprintln(os.Stderr, "  Spot market           : enabled (persistent, stop on interruption)")
		if createSpotMaxPrice != "" {
			fmt.Fprintf(os.Stderr, "  Spot max price        : %s\n", createSpotMaxPrice)
		}
	}
	if integrations.tailscaleEnabled {
		fmt.Fprintf(os.Stderr, "  Tailscale network     : %s\n", tailscaleNetwork)
	}
	if integrations.stepCAEnabled {
		fmt.Fprintf(os.Stderr, "  step-ca server        : %s (provisioner=%s)\n", stepCAServer, stepCAProvisioner)
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

	var s store.Store
	var ws store.WorkspaceStore
	var attachedWorkspace *store.Workspace
	if createWorkspaceMode == workspaceModeEFS || createName != "" {
		s, err = openStore(ctx)
		if err != nil {
			return err
		}
		if createName != "" {
			if err := ensureDesktopNameAvailable(ctx, s, env, createName); err != nil {
				return err
			}
		}
	}
	if createWorkspaceMode == workspaceModeEFS {
		var ok bool
		ws, ok = s.(store.WorkspaceStore)
		if !ok {
			return fmt.Errorf("configured store does not support workspaces")
		}
		attachedWorkspace, err = ws.GetWorkspace(ctx, env, createWorkspaceName)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("workspace %q not found in environment %q; create it with `ai-desktops workspace create` first", createWorkspaceName, env)
			}
			return err
		}
		if attachedWorkspace.State == store.WorkspaceStateDeleted {
			return fmt.Errorf("workspace %q is deleted", createWorkspaceName)
		}
		if attachedWorkspace.AttachedDesktopID != "" {
			return fmt.Errorf("workspace %q is already attached to desktop %q", createWorkspaceName, attachedWorkspace.AttachedDesktopID)
		}
		if !strings.EqualFold(attachedWorkspace.GitHubOwner, owner) {
			return fmt.Errorf("workspace %q owner %q does not match desktop owner %q", createWorkspaceName, attachedWorkspace.GitHubOwner, owner)
		}
		if got, want := store.RepoFingerprint(repoStrings(repos)), attachedWorkspace.RepoFingerprint; got != want {
			return fmt.Errorf("workspace %q repo set does not match create repo set", createWorkspaceName)
		}
		if attachedWorkspace.EFSFileSystemID == "" || attachedWorkspace.EFSAccessPointID == "" {
			return fmt.Errorf("workspace %q is missing EFS attachment metadata", createWorkspaceName)
		}
		req.WorkspaceID = attachedWorkspace.WorkspaceID
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

	subnets, err := selectCreateSubnets(ctx, cfg.AWS.Region, cfg.AWS.Profile, instanceTypes, marketType, foundationOutputs)
	if err != nil {
		return err
	}
	selectedInstanceType := subnets[0].instanceType
	req.InstanceType = selectedInstanceType
	subnetID := subnets[0].subnetID
	costLabel := ""
	if marketType == store.MarketSpot {
		fmt.Fprintf(os.Stderr, "  Spot selected type    : %s\n", selectedInstanceType)
		if subnetID != foundationOutputs[pulumi.OutputSubnetID] {
			fmt.Fprintf(os.Stderr, "  Spot subnet           : %s\n", subnetID)
		}
		if subnets[0].price > 0 {
			costLabel = formatHourlyCost(&awsx.InstanceCostEstimate{
				MarketType:   store.MarketSpot,
				InstanceType: selectedInstanceType,
				Region:       cfg.AWS.Region,
				USDPerHour:   subnets[0].price,
				Source:       "EC2 Spot price history",
			})
		}
	}
	if costLabel == "" {
		awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
		if err != nil {
			costLabel = "unavailable (" + err.Error() + ")"
		} else {
			costLabel = estimateHourlyCostLabel(ctx, awsCfg, cfg.AWS.Region, selectedInstanceType, marketType)
		}
	}
	if costLabel != "" {
		fmt.Fprintf(os.Stderr, "  Estimated cost        : %s\n", costLabel)
	}

	swapSizeGB, err := resolveSwapSize(createSwapSize, selectedInstanceType, volumeSize)
	if err != nil {
		return err
	}

	// AVDs are only activated when --avd or --mobile is explicitly passed.
	// Config-file AVDs are ignored on plain desktop creates.
	var avds []config.AVDConfig
	if len(createAVDs) > 0 {
		parsed, err := parseAVDs(createAVDs)
		if err != nil {
			return err
		}
		avds = parsed
	} else if createMobile {
		avds = cfg.Desktop.AVDs
		if len(avds) == 0 {
			avds = []config.AVDConfig{{
				Name:   config.DefaultMobileAVDName,
				Image:  config.DefaultMobileAVDImage,
				Device: config.DefaultMobileAVDDevice,
			}}
		}
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
	if len(req.AVDNames) > 0 {
		fmt.Fprintf(os.Stderr, "  AVDs                  : %s\n", strings.Join(req.AVDNames, ", "))
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
	npmGitHubScopes := []string(nil)
	if !createNoNPMGitHubScopes {
		npmGitHubScopes = append(npmGitHubScopes, cfg.GitHub.NPMGitHubScopes...)
	}
	npmGitHubScopes = append(npmGitHubScopes, createNPMGitHubScopes...)
	npmGitHubScopes, err = provision.NormalizeNPMGitHubScopes(npmGitHubScopes)
	if err != nil {
		return err
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
		TailscaleNetwork:     tailscaleNetwork,
		TailscaleSecretPath:  tailscaleSecretPath,
		StepCAServerDNS:      stepCAServer,
		StepCAFingerprint:    stepCAFingerprint,
		StepCAProvisioner:    stepCAProvisioner,
		StepCASecretPath:     stepCASecretPath,
		StepCAClients:        stepCAClients,
		AWSRegion:            cfg.AWS.Region,
		Environment:          env,
		WorkspaceMode:        createWorkspaceMode,
		WorkspaceName:        createWorkspaceName,
		PackagesPreInstalled: amiID != "",
		SSHPublicKey:         sshPubKey,
		GitUserName:          cfg.GitHub.GitUserName,
		GitUserEmail:         cfg.GitHub.GitUserEmail,
		NPMGitHubScopes:      npmGitHubScopes,
		SwapSizeGB:           swapSizeGB,
		AVDs:                 avds,
	}
	if attachedWorkspace != nil {
		bootCfg.EFSFileSystemID = attachedWorkspace.EFSFileSystemID
		bootCfg.EFSAccessPointID = attachedWorkspace.EFSAccessPointID
	}
	var renderErr error
	userData, renderErr = provision.RenderCloudInit(bootCfg)
	if renderErr != nil {
		return fmt.Errorf("render cloud-init: %w", renderErr)
	}
	userDataBase64, err := gzipBase64UserData(userData)
	if err != nil {
		return fmt.Errorf("compress cloud-init user-data: %w", err)
	}
	stackCfg := pulumi.DesktopConfig(
		cfg.AWS.Region, desktopID, createName, owner, zone, selectedInstanceType,
		subnetID,
		foundationOutputs[pulumi.OutputSGID],
		foundationOutputs[pulumi.OutputInstanceProfile],
		cfg.Desktop.SSHKeyName,
		req.Repos,
		cfg.Agent.BridgePort,
		volumeSize,
		amiID,
		userDataBase64,
		env,
		nestedVirt,
		marketType,
		createSpotMaxPrice,
		pulumi.WorkspaceConfig{
			Mode:             createWorkspaceMode,
			Name:             createWorkspaceName,
			EFSFileSystemID:  workspaceEFSFileSystemID(attachedWorkspace),
			EFSAccessPointID: workspaceEFSAccessPointID(attachedWorkspace),
		},
	)

	if createPreview {
		fmt.Printf("Desktop ID    : %s\n", desktopID)
		if createName != "" {
			fmt.Printf("Desktop name  : %s\n", createName)
		}
		fmt.Printf("Zone          : %s\n", zone)
		fmt.Printf("Hostname      : %s\n", hostname)
		fmt.Printf("Instance type : %s\n", selectedInstanceType)
		fmt.Printf("Market type   : %s\n", marketType)
		if costLabel != "" {
			fmt.Printf("Estimated cost: %s\n", costLabel)
		}
		if marketType == store.MarketSpot {
			fmt.Printf("Instance types: %s\n", strings.Join(instanceTypes, ","))
		}
		if createSpotMaxPrice != "" {
			fmt.Printf("Spot max price: %s\n", createSpotMaxPrice)
		}
		fmt.Printf("Nested virt   : %v\n", nestedVirt)
		if integrations.tailscaleEnabled {
			fmt.Printf("Tailscale     : %s\n", tailscaleNetwork)
		}
		if integrations.stepCAEnabled {
			fmt.Printf("step-ca       : %s\n", stepCAServer)
		}
		fmt.Printf("Repos         : %v\n", createRepos)
		fmt.Printf("Workspace mode: %s\n", createWorkspaceMode)
		if createWorkspaceName != "" {
			fmt.Printf("Workspace     : %s\n", createWorkspaceName)
		}
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

	if s == nil {
		s, err = openStore(ctx)
		if err != nil {
			return err
		}
	}
	if ws == nil {
		ws, _ = s.(store.WorkspaceStore)
	}
	mgr := desktop.NewManager(s)

	workspaceReserved := false
	if createWorkspaceMode == workspaceModeEFS {
		if ws == nil {
			return fmt.Errorf("configured store does not support workspaces")
		}
		if err := ws.AttachWorkspace(ctx, env, createWorkspaceName, desktopID, createName); err != nil {
			if errors.Is(err, store.ErrWorkspaceAttached) {
				return fmt.Errorf("workspace %q is already attached", createWorkspaceName)
			}
			return fmt.Errorf("attach workspace: %w", err)
		}
		workspaceReserved = true
	}

	if err := mgr.CreateRecord(ctx, desktopID, req); err != nil {
		if workspaceReserved {
			_ = ws.DetachWorkspace(ctx, env, createWorkspaceName, desktopID)
		}
		return fmt.Errorf("create fleet record: %w", err)
	}

	// Run pulumi up and update the record with outputs. Spot creates can fail
	// because one AZ has no capacity, so try each candidate subnet and clean up
	// failed stack attempts before moving on.
	ref := desktopRef
	var outputs map[string]string
	var lastErr error
	for i, candidate := range subnets {
		prelaunchedInstanceID := ""
		if i > 0 {
			fmt.Fprintf(os.Stderr, "Retrying Spot create with %s in subnet %s", candidate.instanceType, candidate.subnetID)
			if candidate.az != "" {
				fmt.Fprintf(os.Stderr, " (%s)", candidate.az)
			}
			fmt.Fprintln(os.Stderr, " ...")
		}
		stackCfg["subnetId"] = candidate.subnetID
		stackCfg["instanceType"] = candidate.instanceType
		delete(stackCfg, "importInstanceId")
		if nestedVirt {
			lp := &instanceLaunchParams{
				amiID:           amiID,
				instanceType:    candidate.instanceType,
				subnetID:        candidate.subnetID,
				sgID:            foundationOutputs[pulumi.OutputSGID],
				instanceProfile: foundationOutputs[pulumi.OutputInstanceProfile],
				sshKeyName:      cfg.Desktop.SSHKeyName,
				userDataBase64:  userDataBase64,
				volumeSize:      volumeSize,
				hostname:        hostname,
				desktopID:       desktopID,
				githubOwner:     owner,
				environment:     env,
				marketType:      marketType,
				spotMaxPrice:    createSpotMaxPrice,
			}
			launchCtx, cancel := context.WithTimeout(ctx, createTimeout)
			importID, launchErr := launchNestedVirtInstance(launchCtx, cfg.AWS.Region, cfg.AWS.Profile, lp)
			timedOut := launchCtx.Err() == context.DeadlineExceeded
			cancel()
			if launchErr != nil {
				lastErr = fmt.Errorf("launch nested-virt instance: %w", launchErr)
				if marketType != store.MarketSpot || (!timedOut && !isCreateCapacityError(launchErr)) || i == len(subnets)-1 {
					break
				}
				fmt.Fprintf(os.Stderr, "Spot nested-virt launch failed for %s in subnet %s; trying the next candidate.\n", candidate.instanceType, candidate.subnetID)
				continue
			}
			prelaunchedInstanceID = importID
			stackCfg["importInstanceId"] = importID
		}
		attemptCtx, cancel := context.WithTimeout(ctx, createTimeout)
		outputs, err = runner.Up(attemptCtx, ref, stackCfg, os.Stderr)
		timedOut := attemptCtx.Err() == context.DeadlineExceeded
		cancel()
		if err == nil {
			outputs["instanceType"] = candidate.instanceType
			req.InstanceType = candidate.instanceType
			lastErr = nil
			break
		}
		lastErr = err
		cleanupCreateAttempt(context.Background(), runner, ref, desktopID, err)
		terminatePrelaunchedInstance(context.Background(), cfg.AWS.Region, cfg.AWS.Profile, prelaunchedInstanceID)
		if marketType != store.MarketSpot || (!timedOut && !isCreateCapacityError(err)) || i == len(subnets)-1 {
			break
		}
		fmt.Fprintf(os.Stderr, "Spot create failed for %s in subnet %s; cleaned up failed attempt before trying the next candidate.\n", candidate.instanceType, candidate.subnetID)
	}
	if lastErr != nil {
		_ = mgr.RecordFailure(ctx, desktopID, "create", lastErr.Error())
		_ = s.Delete(ctx, desktopID)
		if workspaceReserved {
			_ = ws.DetachWorkspace(ctx, env, createWorkspaceName, desktopID)
		}
		return fmt.Errorf("create failed and was cleaned up: %w", lastErr)
	}

	if err := mgr.UpdateFromOutputs(ctx, desktopID, outputs); err != nil {
		return fmt.Errorf("update fleet record: %w", err)
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
		"desktop_name":          createName,
		"hostname":              hostname,
		"novnc_url":             desktop.NoVNCURL(hostname),
		"ssh_target":            desktop.SSHTarget(hostname),
		"stack":                 desktop.StackName(desktopID),
		"ami_id":                amiID,
		"region":                cfg.AWS.Region,
		"instance_type":         req.InstanceType,
		"instance_types":        strings.Join(instanceTypes, ","),
		"market_type":           marketType,
		"estimated_cost":        costLabel,
		"spot_max_price":        createSpotMaxPrice,
		"nested_virtualization": nestedVirtStr,
		"avd_names":             strings.Join(req.AVDNames, ", "),
		"tailscale_network":     tailscaleNetwork,
		"step_ca_server":        stepCAServer,
		"workspace_mode":        createWorkspaceMode,
		"workspace_name":        createWorkspaceName,
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	fmt.Printf("Desktop ID    : %s\n", result["desktop_id"])
	if result["desktop_name"] != "" {
		fmt.Printf("Desktop name  : %s\n", result["desktop_name"])
	}
	fmt.Printf("Hostname      : %s\n", result["hostname"])
	fmt.Printf("Desktop URL   : %s\n", result["novnc_url"])
	fmt.Printf("SSH target    : %s\n", result["ssh_target"])
	fmt.Printf("Instance type : %s\n", result["instance_type"])
	fmt.Printf("Market type   : %s\n", result["market_type"])
	if result["estimated_cost"] != "" {
		fmt.Printf("Estimated cost: %s\n", result["estimated_cost"])
	}
	if result["spot_max_price"] != "" {
		fmt.Printf("Spot max price: %s\n", result["spot_max_price"])
	}
	fmt.Printf("Nested virt   : %s\n", result["nested_virtualization"])
	if result["avd_names"] != "" {
		fmt.Printf("AVDs          : %s\n", result["avd_names"])
	}
	if result["tailscale_network"] != "" {
		fmt.Printf("Tailscale     : %s\n", result["tailscale_network"])
	}
	if result["step_ca_server"] != "" {
		fmt.Printf("step-ca       : %s\n", result["step_ca_server"])
	}
	fmt.Printf("Workspace mode: %s\n", result["workspace_mode"])
	if result["workspace_name"] != "" {
		fmt.Printf("Workspace     : %s\n", result["workspace_name"])
	}
	fmt.Printf("AMI ID        : %s\n", result["ami_id"])
	fmt.Printf("Region        : %s\n", result["region"])
	return nil
}

var secretPathSlugRe = regexp.MustCompile(`[^A-Za-z0-9_+=.@-]+`)

func gzipBase64UserData(userData string) (string, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(userData)); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

func validateSpotMaxPrice(value string) error {
	if value == "" {
		return nil
	}
	price, err := strconv.ParseFloat(value, 64)
	if err != nil || price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return fmt.Errorf("--spot-max-price must be a finite positive number")
	}
	return nil
}

type resolveCreateInstanceTypesInput struct {
	marketType             string
	configInstanceType     string
	configInstanceTypes    []string
	flagInstanceTypeSet    bool
	flagInstanceTypes      []string
	flagInstanceTypesSet   bool
	nestedVirtualization   bool
	mobileDefaultInstance  string
	defaultInstanceType    string
	defaultSpotInstanceSet []string
}

func resolveCreateInstanceTypes(in resolveCreateInstanceTypesInput) ([]string, error) {
	instanceType := strings.TrimSpace(in.configInstanceType)
	if instanceType == "" {
		instanceType = in.defaultInstanceType
	}

	if in.marketType != store.MarketSpot {
		if in.flagInstanceTypesSet {
			return nil, fmt.Errorf("--instance-types requires --spot")
		}
		if in.nestedVirtualization && !config.SupportsNestedVirt(instanceType) {
			return nil, fmt.Errorf("nested virtualization requires a supported Intel Nitro instance type (c8i, m8i, r8i, c7i, m7i, r7i, i7i); got %q - set instance_type in config or use --mobile which defaults to %s", instanceType, in.mobileDefaultInstance)
		}
		return []string{instanceType}, nil
	}

	if in.flagInstanceTypeSet && in.flagInstanceTypesSet {
		return nil, fmt.Errorf("--instance-type and --instance-types cannot be used together for Spot creates")
	}

	var candidates []string
	switch {
	case in.flagInstanceTypesSet:
		candidates = normalizeInstanceTypes(in.flagInstanceTypes)
	case in.flagInstanceTypeSet:
		candidates = normalizeInstanceTypes([]string{instanceType})
	default:
		candidates = normalizeInstanceTypes(in.configInstanceTypes)
		if len(candidates) == 0 {
			candidates = normalizeInstanceTypes(in.defaultSpotInstanceSet)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("--instance-types must include at least one instance type")
	}

	if !in.nestedVirtualization {
		return candidates, nil
	}
	supported := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if config.SupportsNestedVirt(candidate) {
			supported = append(supported, candidate)
		}
	}
	if len(supported) == 0 {
		return nil, fmt.Errorf("nested virtualization requires at least one supported Intel Nitro instance type in --instance-types (c8i, m8i, r8i, c7i, m7i, r7i, i7i)")
	}
	if len(supported) != len(candidates) {
		fmt.Fprintf(os.Stderr, "WARNING: removed unsupported nested virtualization Spot instance types; using %s\n", strings.Join(supported, ","))
	}
	return supported, nil
}

func normalizeInstanceTypes(values []string) []string {
	var out []string
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			instanceType := strings.TrimSpace(part)
			if instanceType == "" || containsString(out, instanceType) {
				continue
			}
			out = append(out, instanceType)
		}
	}
	return out
}

type spotPlacementClient interface {
	DescribeSubnets(context.Context, *ec2sdk.DescribeSubnetsInput, ...func(*ec2sdk.Options)) (*ec2sdk.DescribeSubnetsOutput, error)
	DescribeSpotPriceHistory(context.Context, *ec2sdk.DescribeSpotPriceHistoryInput, ...func(*ec2sdk.Options)) (*ec2sdk.DescribeSpotPriceHistoryOutput, error)
}

type spotSubnetSelection struct {
	subnetID     string
	az           string
	instanceType string
	price        float64
}

func selectCreateSubnets(ctx context.Context, region, profile string, instanceTypes []string, marketType string, outputs map[string]string) ([]spotSubnetSelection, error) {
	candidates := foundationSubnetIDs(outputs)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("foundation stack outputs %q and %q are missing or empty", pulumi.OutputSubnetIDs, pulumi.OutputSubnetID)
	}
	if len(instanceTypes) == 0 {
		return nil, fmt.Errorf("no instance type candidates")
	}
	if marketType != store.MarketSpot {
		return subnetSelectionsFromIDs(instanceTypes[:1], candidates[:1]), nil
	}
	if len(candidates) == 1 {
		return subnetSelectionsFromIDs(instanceTypes, candidates), nil
	}

	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not load AWS config for Spot subnet selection; falling back to alternate subnet order: %v\n", err)
		return subnetSelectionsFallback(instanceTypes, candidates), nil
	}
	selections, err := selectSpotSubnetsByPrice(ctx, ec2sdk.NewFromConfig(awsCfg), instanceTypes, candidates)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not compare Spot subnets; falling back to alternate subnet order: %v\n", err)
		return subnetSelectionsFallback(instanceTypes, candidates), nil
	}
	if len(selections) > 0 && selections[0].az != "" {
		fmt.Fprintf(os.Stderr, "  Spot AZ               : %s ($%.4f/hr)\n", selections[0].az, selections[0].price)
	}
	return selections, nil
}

func foundationSubnetIDs(outputs map[string]string) []string {
	var ids []string
	add := func(value string) {
		for _, part := range strings.Split(value, ",") {
			id := strings.TrimSpace(part)
			if id == "" || containsString(ids, id) {
				continue
			}
			ids = append(ids, id)
		}
	}
	add(outputs[pulumi.OutputSubnetIDs])
	add(outputs[pulumi.OutputSubnetID])
	return ids
}

func subnetSelectionsFromIDs(instanceTypes, subnetIDs []string) []spotSubnetSelection {
	selections := make([]spotSubnetSelection, 0, len(instanceTypes)*len(subnetIDs))
	for _, id := range subnetIDs {
		for _, instanceType := range instanceTypes {
			selections = append(selections, spotSubnetSelection{subnetID: id, instanceType: instanceType})
		}
	}
	return selections
}

func subnetSelectionsFallback(instanceTypes, subnetIDs []string) []spotSubnetSelection {
	selections := make([]spotSubnetSelection, 0, len(instanceTypes)*len(subnetIDs))
	for i := len(subnetIDs) - 1; i >= 0; i-- {
		for _, instanceType := range instanceTypes {
			selections = append(selections, spotSubnetSelection{subnetID: subnetIDs[i], instanceType: instanceType})
		}
	}
	return selections
}

func selectSpotSubnetsByPrice(ctx context.Context, client spotPlacementClient, instanceTypes []string, subnetIDs []string) ([]spotSubnetSelection, error) {
	if len(subnetIDs) == 0 {
		return nil, fmt.Errorf("no subnet candidates")
	}
	if len(instanceTypes) == 0 {
		return nil, fmt.Errorf("no instance type candidates")
	}
	subnets, err := client.DescribeSubnets(ctx, &ec2sdk.DescribeSubnetsInput{
		SubnetIds: subnetIDs,
	})
	if err != nil {
		return nil, fmt.Errorf("describe subnets: %w", err)
	}
	subnetAZs := make(map[string]string, len(subnets.Subnets))
	subnetByAZ := make(map[string]string, len(subnets.Subnets))
	for _, subnet := range subnets.Subnets {
		id := aws.ToString(subnet.SubnetId)
		az := aws.ToString(subnet.AvailabilityZone)
		if id == "" || az == "" || !containsString(subnetIDs, id) {
			continue
		}
		subnetAZs[id] = az
		if subnetByAZ[az] == "" {
			subnetByAZ[az] = id
		}
	}
	if len(subnetAZs) == 0 {
		return nil, fmt.Errorf("none of the candidate subnets were described")
	}

	start := time.Now().Add(-1 * time.Hour)
	awsInstanceTypes := make([]ec2types.InstanceType, 0, len(instanceTypes))
	for _, instanceType := range instanceTypes {
		awsInstanceTypes = append(awsInstanceTypes, ec2types.InstanceType(instanceType))
	}
	prices, err := client.DescribeSpotPriceHistory(ctx, &ec2sdk.DescribeSpotPriceHistoryInput{
		InstanceTypes:       awsInstanceTypes,
		ProductDescriptions: []string{"Linux/UNIX"},
		StartTime:           aws.Time(start),
		MaxResults:          aws.Int32(1000),
	})
	if err != nil {
		return nil, fmt.Errorf("describe spot price history: %w", err)
	}

	byPlacement := make(map[string]spotSubnetSelection, len(subnetIDs)*len(instanceTypes))
	for _, price := range prices.SpotPriceHistory {
		az := aws.ToString(price.AvailabilityZone)
		subnetID := subnetByAZ[az]
		if subnetID == "" {
			continue
		}
		instanceType := string(price.InstanceType)
		if !containsString(instanceTypes, instanceType) {
			continue
		}
		value, err := strconv.ParseFloat(aws.ToString(price.SpotPrice), 64)
		if err != nil {
			continue
		}
		key := subnetID + "\x00" + instanceType
		existing, ok := byPlacement[key]
		if !ok || value < existing.price {
			byPlacement[key] = spotSubnetSelection{subnetID: subnetID, az: az, instanceType: instanceType, price: value}
		}
	}
	if len(byPlacement) == 0 {
		return nil, fmt.Errorf("no Linux/UNIX Spot prices found for %s in candidate subnet AZs", strings.Join(instanceTypes, ","))
	}
	selections := make([]spotSubnetSelection, 0, len(subnetIDs)*len(instanceTypes))
	for _, subnetID := range subnetIDs {
		for _, instanceType := range instanceTypes {
			key := subnetID + "\x00" + instanceType
			if selection, ok := byPlacement[key]; ok {
				selections = append(selections, selection)
			}
		}
	}
	sort.SliceStable(selections, func(i, j int) bool {
		return selections[i].price < selections[j].price
	})
	for _, subnetID := range subnetIDs {
		for _, instanceType := range instanceTypes {
			key := subnetID + "\x00" + instanceType
			if _, ok := byPlacement[key]; ok {
				continue
			}
			selections = append(selections, spotSubnetSelection{subnetID: subnetID, az: subnetAZs[subnetID], instanceType: instanceType})
		}
	}
	return selections, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func cleanupCreateAttempt(ctx context.Context, runner *pulumi.Runner, ref *pulumi.StackRef, desktopID string, cause error) {
	cleanupCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	fmt.Fprintf(os.Stderr, "Create attempt for %s failed; cleaning up stack %s ...\n", desktopID, ref.StackName)
	if err := runner.Destroy(cleanupCtx, ref, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: cleanup for %s failed after create error %v: %v\n", desktopID, cause, err)
	}
}

func terminatePrelaunchedInstance(ctx context.Context, region, profile, instanceID string) {
	if instanceID == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	awsCfg, err := awsx.LoadConfig(cleanupCtx, region, profile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not load AWS config to terminate pre-launched instance %s: %v\n", instanceID, err)
		return
	}
	ec2Client := ec2sdk.NewFromConfig(awsCfg)
	if _, err := ec2Client.TerminateInstances(cleanupCtx, &ec2sdk.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: could not terminate pre-launched instance %s after create failure: %v\n", instanceID, err)
	}
}

func isCreateCapacityError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "insufficientinstancecapacity") ||
		strings.Contains(msg, "insufficientspotinstancecapacity") ||
		strings.Contains(msg, "insufficient capacity") ||
		strings.Contains(msg, "capacity-not-available") ||
		strings.Contains(msg, "there is no spot capacity available") ||
		strings.Contains(msg, "currently do not have sufficient")
}

type resolveCreateIntegrationsInput struct {
	tailscale bool

	tailscaleNetwork    string
	tailscaleNetworkSet bool

	stepCA    string
	stepCASet bool

	stepCAProvisioner    string
	stepCAProvisionerSet bool

	stepCAFingerprint    string
	stepCAFingerprintSet bool

	configTailscaleNetwork  string
	configStepCA            string
	configStepCAProvisioner string
	configStepCAFingerprint string
	envStepCAFingerprint    string
}

type resolvedCreateIntegrations struct {
	tailscaleEnabled  bool
	tailscaleNetwork  string
	stepCAEnabled     bool
	stepCA            string
	stepCAProvisioner string
	stepCAFingerprint string
}

func resolveCreateIntegrations(in resolveCreateIntegrationsInput) (resolvedCreateIntegrations, error) {
	tailscaleNetwork := strings.TrimSpace(in.configTailscaleNetwork)
	if in.tailscaleNetworkSet {
		tailscaleNetwork = strings.TrimSpace(in.tailscaleNetwork)
	}
	tailscaleEnabled := in.tailscale || in.tailscaleNetworkSet
	if tailscaleEnabled && tailscaleNetwork == "" {
		return resolvedCreateIntegrations{}, fmt.Errorf("Tailscale requires --tailscale-network or network.tailscale_network in config")
	}
	if !tailscaleEnabled {
		tailscaleNetwork = ""
	}

	stepCA := strings.TrimSpace(in.configStepCA)
	if in.stepCASet {
		stepCA = strings.TrimSpace(in.stepCA)
	}
	stepCAEnabled := in.stepCASet || (in.tailscale && stepCA != "")
	if !stepCAEnabled {
		stepCA = ""
	}

	stepCAProvisioner := strings.TrimSpace(in.configStepCAProvisioner)
	if in.stepCAProvisionerSet {
		stepCAProvisioner = strings.TrimSpace(in.stepCAProvisioner)
	}
	if stepCAEnabled && stepCAProvisioner == "" {
		return resolvedCreateIntegrations{}, fmt.Errorf("step-ca provisioner must not be empty when step-ca is configured")
	}

	stepCAFingerprint := strings.TrimSpace(in.configStepCAFingerprint)
	if in.stepCAFingerprintSet {
		stepCAFingerprint = strings.TrimSpace(in.stepCAFingerprint)
	}
	if stepCAEnabled && stepCAFingerprint == "" {
		stepCAFingerprint = strings.TrimSpace(in.envStepCAFingerprint)
	}
	if stepCAEnabled && stepCAFingerprint == "" {
		return resolvedCreateIntegrations{}, fmt.Errorf("step-ca fingerprint must be set with --step-ca-fingerprint, pki.step_ca_fingerprint, or STEP_CA_FINGERPRINT when step-ca is configured")
	}
	if !stepCAEnabled {
		stepCAProvisioner = ""
		stepCAFingerprint = ""
	}

	return resolvedCreateIntegrations{
		tailscaleEnabled:  tailscaleEnabled,
		tailscaleNetwork:  tailscaleNetwork,
		stepCAEnabled:     stepCAEnabled,
		stepCA:            stepCA,
		stepCAProvisioner: stepCAProvisioner,
		stepCAFingerprint: stepCAFingerprint,
	}, nil
}

func prepareIntegrationSecrets(ctx context.Context, preview bool, owner, env, region, profile, operatorSecretPath, tailscaleNetwork, stepCAServer string) (string, string, error) {
	tailscaleSecretPath := ""
	if tailscaleNetwork != "" {
		tailscaleSecretPath = fmt.Sprintf("/ai-desktops/%s/tailscale/%s", owner, secretPathSlug(tailscaleNetwork))
	}
	stepCASecretPath := ""
	if stepCAServer != "" {
		stepCASecretPath = fmt.Sprintf("/ai-desktops/%s/step-ca/%s", owner, secretPathSlug(stepCAServer))
	}
	if preview {
		return tailscaleSecretPath, stepCASecretPath, nil
	}

	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		return "", "", fmt.Errorf("load AWS config to store integration secrets: %w", err)
	}
	if tailscaleNetwork != "" {
		authKey := strings.TrimSpace(os.Getenv("TAILSCALE_AUTHKEY"))
		if authKey == "" {
			ok, err := integrationSecretHasKey(ctx, awsCfg, tailscaleSecretPath, "TS_AUTHKEY")
			if err != nil {
				return "", "", err
			}
			if !ok {
				return "", "", fmt.Errorf("TAILSCALE_AUTHKEY must be set or existing secret %q must contain TS_AUTHKEY when Tailscale is configured", tailscaleSecretPath)
			}
		} else {
			payload, err := json.Marshal(map[string]string{"TS_AUTHKEY": authKey})
			if err != nil {
				return "", "", fmt.Errorf("marshal Tailscale secret: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Storing Tailscale auth key at %s ...\n", tailscaleSecretPath)
			if err := storeIntegrationSecret(ctx, awsCfg, tailscaleSecretPath, string(payload), owner, env, "Tailscale"); err != nil {
				return "", "", fmt.Errorf("store Tailscale auth key secret: %w", err)
			}
		}
		tailscaleAPIKey := strings.TrimSpace(os.Getenv("TAILSCALE_API_KEY"))
		if tailscaleAPIKey != "" {
			if operatorSecretPath == "" {
				operatorSecretPath = defaultOperatorSecretPath(owner)
			}
			fmt.Fprintf(os.Stderr, "Storing Tailscale API key at %s ...\n", operatorSecretPath)
			if err := storeOperatorSecretValue(ctx, awsCfg, operatorSecretPath, "TAILSCALE_API_KEY", tailscaleAPIKey, owner, env); err != nil {
				return "", "", fmt.Errorf("store Tailscale API key operator secret: %w", err)
			}
		}
	}
	if stepCAServer != "" {
		password := strings.TrimSpace(os.Getenv("STEP_CA_PROVISIONER_PASSWORD"))
		if password == "" {
			ok, err := integrationSecretHasKey(ctx, awsCfg, stepCASecretPath, "STEP_CA_PROVISIONER_PASSWORD")
			if err != nil {
				return "", "", err
			}
			if !ok {
				return "", "", fmt.Errorf("STEP_CA_PROVISIONER_PASSWORD must be set or existing secret %q must contain STEP_CA_PROVISIONER_PASSWORD when step-ca is configured", stepCASecretPath)
			}
		} else {
			payload, err := json.Marshal(map[string]string{"STEP_CA_PROVISIONER_PASSWORD": password})
			if err != nil {
				return "", "", fmt.Errorf("marshal step-ca secret: %w", err)
			}
			fmt.Fprintf(os.Stderr, "Storing step-ca provisioner secret at %s ...\n", stepCASecretPath)
			if err := storeIntegrationSecret(ctx, awsCfg, stepCASecretPath, string(payload), owner, env, "step-ca"); err != nil {
				return "", "", fmt.Errorf("store step-ca provisioner secret: %w", err)
			}
		}
	}
	return tailscaleSecretPath, stepCASecretPath, nil
}

func integrationSecretHasKey(ctx context.Context, awsCfg aws.Config, secretID, key string) (bool, error) {
	svc := secretsmanager.NewFromConfig(awsCfg)
	out, err := svc.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	})
	if err != nil {
		var notFound *secretsmanagertypes.ResourceNotFoundException
		if errors.As(err, &notFound) {
			return false, nil
		}
		return false, fmt.Errorf("read integration secret %q: %w", secretID, err)
	}
	if out.SecretString == nil {
		return false, nil
	}
	values := map[string]string{}
	if err := json.Unmarshal([]byte(*out.SecretString), &values); err != nil {
		return false, fmt.Errorf("parse integration secret %q: %w", secretID, err)
	}
	return strings.TrimSpace(values[key]) != "", nil
}

func secretPathSlug(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "/")
	s = secretPathSlugRe.ReplaceAllString(s, "-")
	if s == "" {
		return "default"
	}
	return s
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

func validateDesktopName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	if !workspaceNameRe.MatchString(name) {
		return fmt.Errorf("desktop name %q must be 3-64 characters and contain only letters, numbers, dots, underscores, and hyphens", name)
	}
	return nil
}

func ensureDesktopNameAvailable(ctx context.Context, s store.Store, env, name string) error {
	name = strings.TrimSpace(name)
	desktops, err := s.List(ctx)
	if err != nil {
		return fmt.Errorf("check desktop name availability: %w", err)
	}
	for _, d := range desktops {
		if d.State == store.StateTerminated {
			continue
		}
		dEnv := d.Environment
		if dEnv == "" {
			dEnv = env
		}
		if dEnv == env && d.DesktopName == name {
			return fmt.Errorf("desktop name %q is already in use by desktop %q", name, d.DesktopID)
		}
	}
	return nil
}

func workspaceEFSFileSystemID(w *store.Workspace) string {
	if w == nil {
		return ""
	}
	return w.EFSFileSystemID
}

func workspaceEFSAccessPointID(w *store.Workspace) string {
	if w == nil {
		return ""
	}
	return w.EFSAccessPointID
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

func resolveStepCAClients(configClients []config.StepCAClientConfig, flagSpecs []string) ([]provision.StepCAClient, error) {
	clients := make([]config.StepCAClientConfig, 0, len(configClients)+len(flagSpecs))
	clients = append(clients, configClients...)
	for _, spec := range flagSpecs {
		client, err := parseStepCAClientSpec(spec)
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}

	resolved := make([]provision.StepCAClient, 0, len(clients))
	for i, client := range clients {
		issuer := strings.TrimSpace(client.Issuer)
		if !validStepCAClientIssuer(issuer) {
			return nil, fmt.Errorf("step-ca client %d issuer %q must start with an alphanumeric character and contain only alphanumerics, hyphens, underscores, or dots", i, client.Issuer)
		}
		publicKey := strings.TrimSpace(client.PublicKey)
		if publicKey == "" && strings.TrimSpace(client.PublicKeyPath) != "" {
			b, err := os.ReadFile(strings.TrimSpace(client.PublicKeyPath))
			if err != nil {
				return nil, fmt.Errorf("read step-ca client %q public key %q: %w", issuer, client.PublicKeyPath, err)
			}
			publicKey = strings.TrimSpace(string(b))
		}
		if publicKey == "" {
			return nil, fmt.Errorf("step-ca client %q requires public_key or public_key_path", issuer)
		}
		resolved = append(resolved, provision.StepCAClient{
			Issuer:    issuer,
			PublicKey: publicKey,
			Required:  client.Required,
		})
	}
	return resolved, nil
}

func parseStepCAClientSpec(spec string) (config.StepCAClientConfig, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return config.StepCAClientConfig{}, fmt.Errorf("--step-ca-client must not be empty")
	}

	var client config.StepCAClientConfig
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return config.StepCAClientConfig{}, fmt.Errorf("invalid --step-ca-client part %q; expected key=value", part)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "issuer":
			client.Issuer = value
		case "public-key-path", "public_key_path", "key-path", "key_path":
			client.PublicKeyPath = value
		case "public-key", "public_key":
			client.PublicKey = value
		case "required":
			switch strings.ToLower(value) {
			case "true", "1", "yes":
				client.Required = true
			case "false", "0", "no", "":
				client.Required = false
			default:
				return config.StepCAClientConfig{}, fmt.Errorf("invalid required value %q in --step-ca-client %q", value, spec)
			}
		default:
			return config.StepCAClientConfig{}, fmt.Errorf("unknown --step-ca-client key %q", key)
		}
	}
	return client, nil
}

var stepCAClientIssuerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func validStepCAClientIssuer(issuer string) bool {
	return stepCAClientIssuerPattern.MatchString(issuer)
}

type instanceLaunchParams struct {
	amiID           string
	instanceType    string
	subnetID        string
	sgID            string
	instanceProfile string
	sshKeyName      string
	userDataBase64  string
	volumeSize      int
	hostname        string
	desktopID       string
	githubOwner     string
	environment     string
	marketType      string
	spotMaxPrice    string
}

// launchNestedVirtInstance launches an EC2 instance directly via RunInstances with
// CpuOptions.NestedVirtualization=enabled, then returns the instance ID so that
// Pulumi can import it instead of creating a new one.
//
// This avoids the stop/modify/start cycle that would otherwise be needed when
// setting NestedVirtualization post-launch via ModifyInstanceCpuOptions — which
// releases and reassigns the public IP, causing Route53 to point at the stale address.
//
// TODO: remove this workaround once the Pulumi AWS Go SDK exposes NestedVirtualization
// on InstanceCpuOptionsArgs. The instance launch can then be handled entirely by
// the Pulumi desktop stack program (infra/pulumi/desktop/main.go).
// Track: https://github.com/pulumi/pulumi-aws/issues/XXXX
func launchNestedVirtInstance(ctx context.Context, region, profile string, p *instanceLaunchParams) (string, error) {
	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		return "", fmt.Errorf("load AWS config: %w", err)
	}
	ec2Client := ec2sdk.NewFromConfig(awsCfg)

	fmt.Fprintf(os.Stderr, "Nested virt: launching %s with NestedVirtualization=enabled ...\n", p.instanceType)

	input := &ec2sdk.RunInstancesInput{
		ImageId:      aws.String(p.amiID),
		InstanceType: ec2types.InstanceType(p.instanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		CpuOptions: &ec2types.CpuOptionsRequest{
			NestedVirtualization: ec2types.NestedVirtualizationSpecificationEnabled,
		},
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{
			Name: aws.String(p.instanceProfile),
		},
		// UserData is already gzip-compressed and base64-encoded. Cloud-init
		// detects gzip user data, and compression keeps the EC2 API payload under
		// the 16 KiB raw user-data limit.
		UserData: aws.String(p.userDataBase64),
		// AssociatePublicIpAddress must be set via NetworkInterfaces in a VPC subnet;
		// it is not available as a top-level parameter when SubnetId is also specified.
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{
			{
				DeviceIndex:              aws.Int32(0),
				SubnetId:                 aws.String(p.subnetID),
				Groups:                   []string{p.sgID},
				AssociatePublicIpAddress: aws.Bool(true),
			},
		},
		BlockDeviceMappings: []ec2types.BlockDeviceMapping{
			{
				DeviceName: aws.String("/dev/sda1"),
				Ebs: &ec2types.EbsBlockDevice{
					VolumeSize:          aws.Int32(int32(p.volumeSize)),
					VolumeType:          ec2types.VolumeTypeGp3,
					Encrypted:           aws.Bool(true),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		},
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(p.hostname)},
					{Key: aws.String("managed-by"), Value: aws.String("ai-desktops")},
					{Key: aws.String("desktop-id"), Value: aws.String(p.desktopID)},
					{Key: aws.String("github-owner"), Value: aws.String(p.githubOwner)},
					{Key: aws.String("environment"), Value: aws.String(p.environment)},
				},
			},
		},
	}
	if p.sshKeyName != "" {
		input.KeyName = aws.String(p.sshKeyName)
	}
	if p.marketType == store.MarketSpot {
		spotOptions := &ec2types.SpotMarketOptions{
			InstanceInterruptionBehavior: ec2types.InstanceInterruptionBehaviorStop,
			SpotInstanceType:             ec2types.SpotInstanceTypePersistent,
		}
		if p.spotMaxPrice != "" {
			spotOptions.MaxPrice = aws.String(p.spotMaxPrice)
		}
		input.InstanceMarketOptions = &ec2types.InstanceMarketOptionsRequest{
			MarketType:  ec2types.MarketTypeSpot,
			SpotOptions: spotOptions,
		}
	}

	result, err := ec2Client.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("run instance: %w", err)
	}
	if len(result.Instances) == 0 {
		return "", fmt.Errorf("run instance returned no instances")
	}

	instanceID := aws.ToString(result.Instances[0].InstanceId)
	fmt.Fprintf(os.Stderr, "Nested virt: instance %s launched, waiting for running state ...\n", instanceID)

	waiter := ec2sdk.NewInstanceRunningWaiter(ec2Client)
	if err := waiter.Wait(ctx, &ec2sdk.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	}, 5*time.Minute); err != nil {
		// Best-effort terminate to avoid leaving a billable instance behind.
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, _ = ec2Client.TerminateInstances(cleanupCtx, &ec2sdk.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})
		return "", fmt.Errorf("wait for instance running: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Nested virt: instance %s running with NestedVirtualization=enabled\n", instanceID)
	return instanceID, nil
}

// resolveSwapSize determines the swap file size in GiB.
//
// flag values:
//
//	-1 — swap disabled (returns 0, no error)
//	 0 — auto: 2× instance memory, capped at 32 GiB (falls back to 4 GiB for unknown types)
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
		const maxAutoSwapSizeGiB = 32
		memGiB := provision.InstanceMemoryGiB(instanceType)
		if memGiB == 0 {
			// Unknown instance type: default to 4 GiB so swap is still created.
			memGiB = 4
		}
		swapSizeGiB = 2 * memGiB
		if swapSizeGiB > maxAutoSwapSizeGiB {
			swapSizeGiB = maxAutoSwapSizeGiB
		}
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
