package cmd

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/orchael/ai-desktops/internal/config"
	"github.com/orchael/ai-desktops/internal/provision"
	"github.com/orchael/ai-desktops/internal/pulumi"
	"github.com/orchael/ai-desktops/internal/store"
)

func TestCreateBootstrapSecretsRenderSeparateSources(t *testing.T) {
	const agentPath = "/ai-desktops/acme/agents"
	const desktopPath = "/application/local"
	bootCfg := &provision.BootstrapConfig{DesktopID: "d-secrets", AWSRegion: "us-east-2"}
	tracked := desktopSecretPaths(agentPath, []string{desktopPath, agentPath})
	applyBootstrapSecretSources(bootCfg, agentPath, tracked)

	out, err := provision.RenderCloudInit(bootCfg)
	if err != nil {
		t.Fatalf("RenderCloudInit: %v", err)
	}
	agentStart := strings.Index(out, "# --- retrieve AI provider API keys and write bridgectl agents.env ---")
	desktopStart := strings.Index(out, "# --- retrieve desktop secrets and inject into ubuntu environment ---")
	if agentStart < 0 || desktopStart <= agentStart {
		t.Fatalf("rendered cloud-init missing ordered secret sections")
	}
	agentBlock, desktopBlock := out[agentStart:desktopStart], out[desktopStart:]
	if !strings.Contains(agentBlock, agentPath) || strings.Contains(agentBlock, desktopPath) {
		t.Fatalf("agent block has incorrect sources")
	}
	if !strings.Contains(desktopBlock, desktopPath) || strings.Contains(desktopBlock, agentPath) {
		t.Fatalf("desktop block has incorrect sources")
	}
}

func TestCreateCmd_stepCAProvisionerDefault(t *testing.T) {
	flag := createCmd.Flags().Lookup("step-ca-provisioner")
	if flag == nil {
		t.Fatal("step-ca-provisioner flag not registered")
	}
	if flag.DefValue != "admin" {
		t.Fatalf("step-ca-provisioner default = %q, want admin", flag.DefValue)
	}
}

func TestCreateCmd_spotFlagsRegistered(t *testing.T) {
	if flag := createCmd.Flags().Lookup("spot"); flag == nil {
		t.Fatal("spot flag not registered")
	}
	if flag := createCmd.Flags().Lookup("spot-max-price"); flag == nil {
		t.Fatal("spot-max-price flag not registered")
	}
	if flag := createCmd.Flags().Lookup("instance-types"); flag == nil {
		t.Fatal("instance-types flag not registered")
	}
	if flag := createCmd.Flags().Lookup("create-timeout"); flag == nil {
		t.Fatal("create-timeout flag not registered")
	}
}

func TestResolveVolumeSize(t *testing.T) {
	tests := []struct {
		name        string
		cliFlag     int
		configValue int
		mobile      bool
		avds        []string
		want        int
	}{
		{name: "default normal", want: config.DefaultVolumeSize},
		{name: "default mobile flag", mobile: true, want: config.DefaultMobileVolumeSize},
		{name: "default avd flag", avds: []string{"flutter_dev:system-images;android-35;google_apis;x86_64"}, want: config.DefaultMobileVolumeSize},
		{name: "config override normal", configValue: 80, want: 80},
		{name: "config override mobile", configValue: 80, mobile: true, want: 80},
		{name: "cli override normal", cliFlag: 120, want: 120},
		{name: "cli override mobile", cliFlag: 120, mobile: true, want: 120},
		{name: "cli overrides config", cliFlag: 120, configValue: 80, want: 120},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveVolumeSize(tc.cliFlag, tc.configValue, tc.mobile, tc.avds)
			if got != tc.want {
				t.Errorf("resolveVolumeSize(%d, %d, %v, %v) = %d, want %d",
					tc.cliFlag, tc.configValue, tc.mobile, tc.avds, got, tc.want)
			}
		})
	}
}

func TestCreateCmd_npmGitHubScopeFlagsRegistered(t *testing.T) {
	if flag := createCmd.Flags().Lookup("npm-github-scope"); flag == nil {
		t.Fatal("npm-github-scope flag not registered")
	}
	if flag := createCmd.Flags().Lookup("no-npm-github-scopes"); flag == nil {
		t.Fatal("no-npm-github-scopes flag not registered")
	}
}

func TestCreateCmd_workspaceFlagsRegistered(t *testing.T) {
	if flag := createCmd.Flags().Lookup("name"); flag == nil {
		t.Fatal("name flag not registered")
	}
	if flag := createCmd.Flags().Lookup("workspace-mode"); flag == nil {
		t.Fatal("workspace-mode flag not registered")
	}
	if flag := createCmd.Flags().Lookup("workspace-name"); flag == nil {
		t.Fatal("workspace-name flag not registered")
	}
}

func TestValidateSpotMaxPrice(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "empty"},
		{name: "positive decimal", value: "0.12"},
		{name: "positive integer", value: "1"},
		{name: "zero", value: "0", wantErr: true},
		{name: "negative", value: "-0.1", wantErr: true},
		{name: "nan", value: "NaN", wantErr: true},
		{name: "infinity", value: "+Inf", wantErr: true},
		{name: "not a number", value: "cheap", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSpotMaxPrice(tt.value)
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateDesktopName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr string
	}{
		{name: "orchael-factory-dev"},
		{name: "  orchael-factory-dev  "},
		{name: ""},
		{name: "bad/name", wantErr: "desktop name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDesktopName(tt.name)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestFoundationSubnetIDs(t *testing.T) {
	got := foundationSubnetIDs(map[string]string{
		pulumi.OutputSubnetIDs: " subnet-2,subnet-3,subnet-2 ",
		pulumi.OutputSubnetID:  "subnet-1",
	})
	want := []string{"subnet-2", "subnet-3", "subnet-1"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("foundationSubnetIDs = %v, want %v", got, want)
	}
}

func TestSelectCreateSubnetsOnDemandUsesFirstSubnetOnly(t *testing.T) {
	got, err := selectCreateSubnets(context.Background(), "us-east-1", "", []string{"t3.large"}, store.MarketOnDemand, map[string]string{
		pulumi.OutputSubnetIDs: "subnet-1,subnet-2",
		pulumi.OutputSubnetID:  "subnet-legacy",
	})
	if err != nil {
		t.Fatalf("selectCreateSubnets: %v", err)
	}
	if len(got) != 1 || got[0].subnetID != "subnet-1" {
		t.Fatalf("selectCreateSubnets = %+v, want only subnet-1", got)
	}
	if got[0].instanceType != "t3.large" {
		t.Fatalf("instance type = %q, want t3.large", got[0].instanceType)
	}
}

func TestResolveCreateInstanceTypes(t *testing.T) {
	tests := []struct {
		name    string
		in      resolveCreateInstanceTypesInput
		want    []string
		wantErr string
	}{
		{
			name: "on demand uses single instance type",
			in: resolveCreateInstanceTypesInput{
				marketType:          store.MarketOnDemand,
				configInstanceType:  "m7i.xlarge",
				defaultInstanceType: config.DefaultInstanceType,
			},
			want: []string{"m7i.xlarge"},
		},
		{
			name: "instance types require spot",
			in: resolveCreateInstanceTypesInput{
				marketType:           store.MarketOnDemand,
				configInstanceType:   "m7i.xlarge",
				flagInstanceTypes:    []string{"m6i.xlarge"},
				flagInstanceTypesSet: true,
				defaultInstanceType:  config.DefaultInstanceType,
			},
			wantErr: "--instance-types requires --spot",
		},
		{
			name: "spot uses config pool",
			in: resolveCreateInstanceTypesInput{
				marketType:          store.MarketSpot,
				configInstanceType:  "t3.large",
				configInstanceTypes: []string{"m6i.xlarge", "m5.xlarge"},
				defaultInstanceType: config.DefaultInstanceType,
			},
			want: []string{"m6i.xlarge", "m5.xlarge"},
		},
		{
			name: "spot flag pool trims splits and dedupes",
			in: resolveCreateInstanceTypesInput{
				marketType:           store.MarketSpot,
				configInstanceType:   "t3.large",
				flagInstanceTypes:    []string{" m6i.xlarge,m5.xlarge ", "m6i.xlarge"},
				flagInstanceTypesSet: true,
				defaultInstanceType:  config.DefaultInstanceType,
			},
			want: []string{"m6i.xlarge", "m5.xlarge"},
		},
		{
			name: "spot single instance type override remains supported",
			in: resolveCreateInstanceTypesInput{
				marketType:          store.MarketSpot,
				configInstanceType:  "m7i.xlarge",
				flagInstanceTypeSet: true,
				defaultInstanceType: config.DefaultInstanceType,
			},
			want: []string{"m7i.xlarge"},
		},
		{
			name: "spot rejects ambiguous flags",
			in: resolveCreateInstanceTypesInput{
				marketType:           store.MarketSpot,
				configInstanceType:   "m7i.xlarge",
				flagInstanceTypeSet:  true,
				flagInstanceTypes:    []string{"m6i.xlarge"},
				flagInstanceTypesSet: true,
				defaultInstanceType:  config.DefaultInstanceType,
			},
			wantErr: "--instance-type and --instance-types cannot be used together",
		},
		{
			name: "nested spot filters unsupported candidates",
			in: resolveCreateInstanceTypesInput{
				marketType:             store.MarketSpot,
				configInstanceType:     "t3.large",
				configInstanceTypes:    []string{"m6i.xlarge", "m7i.xlarge", "m5.xlarge"},
				nestedVirtualization:   true,
				mobileDefaultInstance:  config.DefaultMobileInstanceType,
				defaultInstanceType:    config.DefaultInstanceType,
				defaultSpotInstanceSet: config.DefaultSpotInstanceTypes,
			},
			want: []string{"m7i.xlarge"},
		},
		{
			name: "nested on demand rejects unsupported type",
			in: resolveCreateInstanceTypesInput{
				marketType:            store.MarketOnDemand,
				configInstanceType:    "m6i.xlarge",
				nestedVirtualization:  true,
				mobileDefaultInstance: config.DefaultMobileInstanceType,
				defaultInstanceType:   config.DefaultInstanceType,
			},
			wantErr: "nested virtualization requires",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCreateInstanceTypes(tt.in)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("instance types = %v, want %v", got, tt.want)
			}
		})
	}
}

type fakeSpotPlacementClient struct {
	subnets []ec2types.Subnet
	prices  []ec2types.SpotPrice
}

func (f fakeSpotPlacementClient) DescribeSubnets(context.Context, *ec2sdk.DescribeSubnetsInput, ...func(*ec2sdk.Options)) (*ec2sdk.DescribeSubnetsOutput, error) {
	return &ec2sdk.DescribeSubnetsOutput{Subnets: f.subnets}, nil
}

func (f fakeSpotPlacementClient) DescribeSpotPriceHistory(context.Context, *ec2sdk.DescribeSpotPriceHistoryInput, ...func(*ec2sdk.Options)) (*ec2sdk.DescribeSpotPriceHistoryOutput, error) {
	return &ec2sdk.DescribeSpotPriceHistoryOutput{SpotPriceHistory: f.prices}, nil
}

func TestSelectSpotSubnetsByPrice(t *testing.T) {
	client := fakeSpotPlacementClient{
		subnets: []ec2types.Subnet{
			{SubnetId: aws.String("subnet-a"), AvailabilityZone: aws.String("us-east-2a")},
			{SubnetId: aws.String("subnet-b"), AvailabilityZone: aws.String("us-east-2b")},
			{SubnetId: aws.String("subnet-c"), AvailabilityZone: aws.String("us-east-2c")},
		},
		prices: []ec2types.SpotPrice{
			{AvailabilityZone: aws.String("us-east-2a"), InstanceType: ec2types.InstanceTypeM7iXlarge, SpotPrice: aws.String("0.0779")},
			{AvailabilityZone: aws.String("us-east-2b"), InstanceType: ec2types.InstanceTypeM7iXlarge, SpotPrice: aws.String("0.0639")},
			{AvailabilityZone: aws.String("us-east-2c"), InstanceType: ec2types.InstanceTypeM7iXlarge, SpotPrice: aws.String("0.0701")},
			{AvailabilityZone: aws.String("us-east-2a"), InstanceType: ec2types.InstanceTypeM6iXlarge, SpotPrice: aws.String("0.0546")},
			{AvailabilityZone: aws.String("us-east-2b"), InstanceType: ec2types.InstanceTypeM6iXlarge, SpotPrice: aws.String("0.0543")},
			{AvailabilityZone: aws.String("us-east-2c"), InstanceType: ec2types.InstanceTypeM6iXlarge, SpotPrice: aws.String("0.0519")},
		},
	}
	got, err := selectSpotSubnetsByPrice(context.Background(), client, []string{"m7i.xlarge", "m6i.xlarge"}, []string{"subnet-a", "subnet-b", "subnet-c"})
	if err != nil {
		t.Fatalf("selectSpotSubnetsByPrice: %v", err)
	}
	order := []string{
		got[0].subnetID + "/" + got[0].instanceType,
		got[1].subnetID + "/" + got[1].instanceType,
		got[2].subnetID + "/" + got[2].instanceType,
	}
	want := []string{"subnet-c/m6i.xlarge", "subnet-b/m6i.xlarge", "subnet-a/m6i.xlarge"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("spot placement order = %v, want %v", order, want)
	}
}

func TestIsCreateCapacityError(t *testing.T) {
	if !isCreateCapacityError(errors.New("Server.InsufficientInstanceCapacity: currently do not have sufficient m7i.2xlarge capacity")) {
		t.Fatal("expected insufficient capacity error to match")
	}
	if isCreateCapacityError(errors.New("access denied")) {
		t.Fatal("did not expect non-capacity error to match")
	}
}

func TestResolveCreateIntegrations(t *testing.T) {
	tests := []struct {
		name          string
		in            resolveCreateIntegrationsInput
		wantTailnet   string
		wantTailscale bool
		wantStepCA    string
		wantStepCAOn  bool
		wantProv      string
		wantFP        string
		wantErr       string
	}{
		{
			name: "config alone does not enable tailscale or step-ca",
			in: resolveCreateIntegrationsInput{
				configTailscaleNetwork:  "config-tailnet",
				configStepCA:            "ca.config.ts.net",
				configStepCAProvisioner: "admin",
				configStepCAFingerprint: "config-fp",
			},
		},
		{
			name: "tailscale flag enables tailscale and configured step-ca",
			in: resolveCreateIntegrationsInput{
				tailscale:               true,
				configTailscaleNetwork:  "config-tailnet",
				configStepCA:            "ca.config.ts.net",
				configStepCAProvisioner: "ai-desktops",
				configStepCAFingerprint: "config-fp",
			},
			wantTailnet:   "config-tailnet",
			wantTailscale: true,
			wantStepCA:    "ca.config.ts.net",
			wantStepCAOn:  true,
			wantProv:      "ai-desktops",
			wantFP:        "config-fp",
		},
		{
			name: "tailscale flag requires tailnet",
			in: resolveCreateIntegrationsInput{
				tailscale: true,
			},
			wantErr: "Tailscale requires --tailscale-network",
		},
		{
			name: "tailscale network flag enables tailscale and overrides config",
			in: resolveCreateIntegrationsInput{
				tailscaleNetwork:       "flag-tailnet",
				tailscaleNetworkSet:    true,
				configTailscaleNetwork: "config-tailnet",
				configStepCA:           "ca.config.ts.net",
			},
			wantTailnet:   "flag-tailnet",
			wantTailscale: true,
		},
		{
			name: "step-ca requires fingerprint",
			in: resolveCreateIntegrationsInput{
				stepCA:                  "ca.flag.ts.net",
				stepCASet:               true,
				configStepCAProvisioner: "admin",
			},
			wantErr: "step-ca fingerprint must be set",
		},
		{
			name: "step-ca flag enables step-ca without tailscale",
			in: resolveCreateIntegrationsInput{
				stepCA:                 "ca.flag.ts.net",
				stepCASet:              true,
				stepCAProvisioner:      "ops",
				stepCAProvisionerSet:   true,
				stepCAFingerprint:      "flag-fp",
				stepCAFingerprintSet:   true,
				configTailscaleNetwork: "config-tailnet",
			},
			wantStepCA:   "ca.flag.ts.net",
			wantStepCAOn: true,
			wantProv:     "ops",
			wantFP:       "flag-fp",
		},
		{
			name: "step-ca fingerprint falls back to environment",
			in: resolveCreateIntegrationsInput{
				stepCA:                  "ca.flag.ts.net",
				stepCASet:               true,
				configStepCAProvisioner: "admin",
				envStepCAFingerprint:    "env-fp",
			},
			wantStepCA:   "ca.flag.ts.net",
			wantStepCAOn: true,
			wantProv:     "admin",
			wantFP:       "env-fp",
		},
		{
			name: "empty step-ca provisioner fails when step-ca enabled",
			in: resolveCreateIntegrationsInput{
				stepCA:               "ca.flag.ts.net",
				stepCASet:            true,
				stepCAProvisioner:    "",
				stepCAProvisionerSet: true,
			},
			wantErr: "step-ca provisioner must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveCreateIntegrations(tt.in)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.tailscaleEnabled != tt.wantTailscale {
				t.Errorf("tailscaleEnabled = %v, want %v", got.tailscaleEnabled, tt.wantTailscale)
			}
			if got.tailscaleNetwork != tt.wantTailnet {
				t.Errorf("tailscaleNetwork = %q, want %q", got.tailscaleNetwork, tt.wantTailnet)
			}
			if got.stepCAEnabled != tt.wantStepCAOn {
				t.Errorf("stepCAEnabled = %v, want %v", got.stepCAEnabled, tt.wantStepCAOn)
			}
			if got.stepCA != tt.wantStepCA {
				t.Errorf("stepCA = %q, want %q", got.stepCA, tt.wantStepCA)
			}
			if got.stepCAProvisioner != tt.wantProv {
				t.Errorf("stepCAProvisioner = %q, want %q", got.stepCAProvisioner, tt.wantProv)
			}
			if got.stepCAFingerprint != tt.wantFP {
				t.Errorf("stepCAFingerprint = %q, want %q", got.stepCAFingerprint, tt.wantFP)
			}
		})
	}
}

func TestResolveStepCAClients_ConfigAndFlags(t *testing.T) {
	dir := t.TempDir()
	pubPath := filepath.Join(dir, "mark.pub")
	if err := os.WriteFile(pubPath, []byte("ssh-ed25519 AAAA mark\n"), 0600); err != nil {
		t.Fatal(err)
	}

	clients, err := resolveStepCAClients(
		[]config.StepCAClientConfig{
			{Issuer: "config-client", PublicKey: "ssh-ed25519 BBBB config"},
		},
		[]string{"issuer=mark-macbook,public-key-path=" + pubPath + ",required=true"},
	)
	if err != nil {
		t.Fatalf("resolveStepCAClients: %v", err)
	}
	if len(clients) != 2 {
		t.Fatalf("clients = %d, want 2", len(clients))
	}
	if clients[0].Issuer != "config-client" || clients[0].PublicKey != "ssh-ed25519 BBBB config" {
		t.Fatalf("config client not preserved: %+v", clients[0])
	}
	if clients[1].Issuer != "mark-macbook" {
		t.Fatalf("flag client issuer = %q", clients[1].Issuer)
	}
	if clients[1].PublicKey != "ssh-ed25519 AAAA mark" {
		t.Fatalf("flag client public key = %q", clients[1].PublicKey)
	}
	if !clients[1].Required {
		t.Fatal("flag client required should be true")
	}
}

func TestResolveStepCAClients_InvalidIssuer(t *testing.T) {
	_, err := resolveStepCAClients([]config.StepCAClientConfig{
		{Issuer: "../bad", PublicKey: "ssh-ed25519 AAAA bad"},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "issuer") {
		t.Fatalf("error = %v, want issuer validation error", err)
	}
}

func TestPrepareIntegrationSecrets_previewDoesNotRequireSecretEnv(t *testing.T) {
	t.Setenv("TAILSCALE_AUTHKEY", "")
	t.Setenv("STEP_CA_PROVISIONER_PASSWORD", "")

	tailscalePath, stepCAPath, err := prepareIntegrationSecrets(
		context.Background(),
		true,
		"acme",
		"dev",
		"invalid-region-for-preview-test",
		"invalid-profile-for-preview-test",
		"/ai-desktops/acme",
		"acme-tailnet",
		"ca.acme-tailnet.ts.net",
	)
	if err != nil {
		t.Fatalf("prepareIntegrationSecrets preview returned error: %v", err)
	}
	if tailscalePath != "/ai-desktops/acme/tailscale/acme-tailnet" {
		t.Fatalf("tailscale path = %q", tailscalePath)
	}
	if stepCAPath != "/ai-desktops/acme/step-ca/ca.acme-tailnet.ts.net" {
		t.Fatalf("step-ca path = %q", stepCAPath)
	}
}

func TestIntegrationSecretHasKey(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        string
		want        bool
		wantErr     bool
		errContains string
	}{
		{
			name:   "secret contains key",
			status: http.StatusOK,
			body:   `{"SecretString":"{\"TS_AUTHKEY\":\"tskey-auth-test\"}"}`,
			want:   true,
		},
		{
			name:   "secret missing key",
			status: http.StatusOK,
			body:   `{"SecretString":"{\"OTHER\":\"value\"}"}`,
		},
		{
			name:   "secret key blank",
			status: http.StatusOK,
			body:   `{"SecretString":"{\"TS_AUTHKEY\":\"  \"}"}`,
		},
		{
			name:   "secret not found",
			status: http.StatusBadRequest,
			body:   `{"__type":"ResourceNotFoundException","Message":"not found"}`,
		},
		{
			name:        "invalid json secret",
			status:      http.StatusOK,
			body:        `{"SecretString":"not-json"}`,
			wantErr:     true,
			errContains: "parse integration secret",
		},
		{
			name:        "permission error",
			status:      http.StatusBadRequest,
			body:        `{"__type":"AccessDeniedException","Message":"denied"}`,
			wantErr:     true,
			errContains: "read integration secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if target := r.Header.Get("X-Amz-Target"); !strings.HasSuffix(target, "GetSecretValue") {
					t.Fatalf("unexpected AWS target %q", target)
				}
				w.Header().Set("Content-Type", "application/x-amz-json-1.1")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			got, err := integrationSecretHasKey(context.Background(), makeSecretsManagerConfig(srv.URL), "/ai-desktops/acme/tailscale/acme", "TS_AUTHKEY")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("error = %q, want substring %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGzipBase64UserData(t *testing.T) {
	const userData = "#cloud-config\nruncmd:\n  - echo hello\n"

	encoded, err := gzipBase64UserData(userData)
	if err != nil {
		t.Fatalf("gzipBase64UserData returned error: %v", err)
	}
	compressed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(decoded) != userData {
		t.Fatalf("decoded user-data = %q, want %q", string(decoded), userData)
	}
}

func TestSecretPathSlugDisallowsSlash(t *testing.T) {
	got := secretPathSlug("/team/tailnet/name/")
	if got != "team-tailnet-name" {
		t.Fatalf("secretPathSlug with slashes = %q, want team-tailnet-name", got)
	}
}

func TestParseAndValidateRepos(t *testing.T) {
	tests := []struct {
		name        string
		owner       string
		rawRepos    []string
		wantOwner   string
		wantCount   int
		wantErr     bool
		errContains string
	}{
		{
			name:      "no repos, owner provided",
			owner:     "acme",
			rawRepos:  nil,
			wantOwner: "acme",
			wantCount: 0,
		},
		{
			name:      "no repos, no owner",
			owner:     "",
			rawRepos:  nil,
			wantOwner: "",
			wantCount: 0,
		},
		{
			name:      "repos provided, owner inferred",
			owner:     "",
			rawRepos:  []string{"github.com/acme/app-one", "github.com/acme/app-two"},
			wantOwner: "acme",
			wantCount: 2,
		},
		{
			name:      "repos provided, owner matches flag",
			owner:     "acme",
			rawRepos:  []string{"github.com/acme/app-one"},
			wantOwner: "acme",
			wantCount: 1,
		},
		{
			name:      "repos provided, owner matches flag case-insensitively",
			owner:     "ACME",
			rawRepos:  []string{"github.com/acme/app-one"},
			wantOwner: "acme", // canonical owner from the repo URL, not the flag value
			wantCount: 1,
		},
		{
			name:        "repos provided, owner mismatch",
			owner:       "other",
			rawRepos:    []string{"github.com/acme/app-one"},
			wantErr:     true,
			errContains: "does not match --github-owner",
		},
		{
			name:     "repos provided, invalid URL",
			owner:    "",
			rawRepos: []string{"https://gitlab.com/acme/app"},
			wantErr:  true,
		},
		{
			name:     "repos provided, mixed owners",
			owner:    "",
			rawRepos: []string{"github.com/acme/app-one", "github.com/other/app-two"},
			wantErr:  true,
		},
		{
			name:      "single SSH repo, owner inferred",
			owner:     "",
			rawRepos:  []string{"git@github.com:myorg/myrepo.git"},
			wantOwner: "myorg",
			wantCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repos, owner, err := parseAndValidateRepos(tt.owner, tt.rawRepos)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if owner != tt.wantOwner {
				t.Errorf("owner: got %q, want %q", owner, tt.wantOwner)
			}
			if len(repos) != tt.wantCount {
				t.Errorf("repo count: got %d, want %d", len(repos), tt.wantCount)
			}
		})
	}
}

func TestEnsureDesktopNameAvailable(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	if err := s.Create(ctx, &store.Desktop{
		DesktopID:   "d-001",
		DesktopName: "factory-dev",
		Environment: "dev",
		State:       store.StateReady,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ensureDesktopNameAvailable(ctx, s, "dev", "factory-dev"); err == nil {
		t.Fatal("expected duplicate name error")
	}
	if err := ensureDesktopNameAvailable(ctx, s, "prod", "factory-dev"); err != nil {
		t.Fatalf("prod duplicate should be allowed: %v", err)
	}
}

func TestEnsureDesktopNameAvailableIgnoresTerminated(t *testing.T) {
	ctx := context.Background()
	s := store.NewInMemoryStore()
	if err := s.Create(ctx, &store.Desktop{
		DesktopID:   "d-terminated",
		DesktopName: "factory-dev",
		Environment: "dev",
		State:       store.StateTerminated,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := ensureDesktopNameAvailable(ctx, s, "dev", "factory-dev"); err != nil {
		t.Fatalf("terminated duplicate should be allowed: %v", err)
	}
}

func TestParseAVDs(t *testing.T) {
	tests := []struct {
		name        string
		specs       []string
		wantLen     int
		wantErr     bool
		errContains string
	}{
		{
			name:    "name:image",
			specs:   []string{"flutter_dev:system-images;android-35;google_apis;x86_64"},
			wantLen: 1,
		},
		{
			name:    "name:image:device",
			specs:   []string{"flutter_dev:system-images;android-35;google_apis;x86_64:pixel_6"},
			wantLen: 1,
		},
		{
			name:    "multiple AVDs",
			specs:   []string{"avd1:system-images;android-35;google_apis;x86_64", "avd2:system-images;android-33;google_apis;x86_64:pixel_4"},
			wantLen: 2,
		},
		{
			name:        "missing image",
			specs:       []string{"flutter_dev"},
			wantErr:     true,
			errContains: "invalid --avd",
		},
		{
			name:        "empty name",
			specs:       []string{":system-images;android-35;google_apis;x86_64"},
			wantErr:     true,
			errContains: "invalid --avd",
		},
		{
			name:        "empty image",
			specs:       []string{"flutter_dev:"},
			wantErr:     true,
			errContains: "invalid --avd",
		},
		{
			name:    "empty list",
			specs:   []string{},
			wantLen: 0,
		},
		{
			name:        "name with shell metacharacter rejected",
			specs:       []string{"flutter$(evil):system-images;android-35;google_apis;x86_64"},
			wantErr:     true,
			errContains: "invalid --avd name",
		},
		{
			name:        "image with shell metacharacter rejected",
			specs:       []string{"flutter_dev:system-images;android-35;google_apis;x86_64$(evil)"},
			wantErr:     true,
			errContains: "invalid --avd image",
		},
		{
			name:        "device with shell metacharacter rejected",
			specs:       []string{"flutter_dev:system-images;android-35;google_apis;x86_64:pixel$(evil)"},
			wantErr:     true,
			errContains: "invalid --avd device",
		},
		{
			name:    "name with hyphens allowed",
			specs:   []string{"my-avd:system-images;android-35;google_apis;x86_64"},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAVDs(tt.specs)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tt.wantLen {
				t.Errorf("len(got) = %d, want %d", len(got), tt.wantLen)
			}
		})
	}

	t.Run("fields are populated correctly", func(t *testing.T) {
		got, err := parseAVDs([]string{"my_avd:system-images;android-35;google_apis;x86_64:pixel_6"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 AVD, got %d", len(got))
		}
		if got[0].Name != "my_avd" {
			t.Errorf("Name = %q, want %q", got[0].Name, "my_avd")
		}
		if got[0].Image != "system-images;android-35;google_apis;x86_64" {
			t.Errorf("Image = %q, want %q", got[0].Image, "system-images;android-35;google_apis;x86_64")
		}
		if got[0].Device != "pixel_6" {
			t.Errorf("Device = %q, want %q", got[0].Device, "pixel_6")
		}
	})

	t.Run("device is optional", func(t *testing.T) {
		got, err := parseAVDs([]string{"my_avd:system-images;android-35;google_apis;x86_64"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got[0].Device != "" {
			t.Errorf("Device should be empty when not specified, got %q", got[0].Device)
		}
	})
}

func TestResolveSwapSize(t *testing.T) {
	tests := []struct {
		name         string
		flagValue    int
		instanceType string
		volumeGiB    int
		wantSwap     int
		wantErr      bool
		errContains  string
	}{
		{
			name:         "disabled with -1",
			flagValue:    -1,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     0,
		},
		{
			name:         "values below -1 are rejected",
			flagValue:    -2,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantErr:      true,
			errContains:  "invalid --swap-size",
		},
		{
			name:         "auto: 2x memory for t3.large (8 GiB RAM → 16 GiB swap)",
			flagValue:    0,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     16,
		},
		{
			name:         "auto: 2x memory for m5.xlarge (16 GiB RAM → 32 GiB swap)",
			flagValue:    0,
			instanceType: "m5.xlarge",
			volumeGiB:    100,
			wantSwap:     32,
		},
		{
			name:         "auto: unknown instance type falls back to 4 GiB → 8 GiB swap",
			flagValue:    0,
			instanceType: "x99.mega",
			volumeGiB:    100,
			wantSwap:     8,
		},
		{
			name:         "explicit size",
			flagValue:    4,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     4,
		},
		{
			name:         "swap + OS reservation exactly fits volume",
			flagValue:    80,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantSwap:     80,
		},
		{
			name:         "swap + OS reservation exceeds volume",
			flagValue:    81,
			instanceType: "t3.large",
			volumeGiB:    100,
			wantErr:      true,
			errContains:  "exceeds root volume size",
		},
		{
			name:         "auto swap is capped at 32 GiB",
			flagValue:    0,
			instanceType: "r5.2xlarge", // 64 GiB RAM → 128 GiB swap
			volumeGiB:    100,
			wantSwap:     32,
		},
		{
			name:         "capped auto swap can still exceed small volume",
			flagValue:    0,
			instanceType: "r5.2xlarge",
			volumeGiB:    40,
			wantErr:      true,
			errContains:  "exceeds root volume size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSwapSize(tt.flagValue, tt.instanceType, tt.volumeGiB)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (swap=%d)", got)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantSwap {
				t.Errorf("resolveSwapSize(%d, %q, %d) = %d, want %d",
					tt.flagValue, tt.instanceType, tt.volumeGiB, got, tt.wantSwap)
			}
		})
	}
}
