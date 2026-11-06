package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/provision"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
)

const avdProvisionMarker = "# --- provision Android Virtual Devices via Ansible ---"

var legacyAVDVariables = regexp.MustCompile(`echo '([A-Za-z0-9+/=]+)' \| base64 -d > /tmp/avd-vars.json`)

// restoreAVDNames reads the AVD definitions actually saved for a new boot.
// Names alone in the fleet record are insufficient: recreation needs the image
// and device settings retained in cloud-init or the version 1 bootstrap spec.
func restoreAVDNames(d *store.Desktop) ([]string, error) {
	if d.RestoreSpec == nil {
		return nil, fmt.Errorf("desktop %s lacks saved restore settings", d.DesktopID)
	}
	switch d.RestoreSpec.Version {
	case 1:
		var boot provision.BootstrapConfig
		if err := json.Unmarshal([]byte(d.RestoreSpec.BootstrapJSON), &boot); err != nil {
			return nil, fmt.Errorf("decode saved AVD bootstrap settings: %w", err)
		}
		names := make([]string, 0, len(boot.AVDs))
		for _, avd := range boot.AVDs {
			if avd.Name == "" || avd.Image == "" {
				return nil, fmt.Errorf("desktop %s saved AVD definition is incomplete", d.DesktopID)
			}
			names = append(names, avd.Name)
		}
		return names, nil
	case 2:
		cloud, err := decodedUserData(d.RestoreSpec.Config["userDataBase64"])
		if err != nil {
			return nil, err
		}
		if strings.Count(cloud, avdProvisionMarker) != 1 {
			return nil, fmt.Errorf("desktop %s has no unambiguous AVD provisioning step", d.DesktopID)
		}
		matches := legacyAVDVariables.FindAllStringSubmatch(cloud, -1)
		if len(matches) != 1 {
			return nil, fmt.Errorf("desktop %s has no unambiguous saved AVD definitions", d.DesktopID)
		}
		payload, err := base64.StdEncoding.DecodeString(matches[0][1])
		if err != nil {
			return nil, fmt.Errorf("decode saved AVD definitions: %w", err)
		}
		var vars struct {
			AVDs []struct {
				Name  string `json:"name"`
				Image string `json:"image"`
			} `json:"avds"`
		}
		if err := json.Unmarshal(payload, &vars); err != nil {
			return nil, fmt.Errorf("parse saved AVD definitions: %w", err)
		}
		names := make([]string, 0, len(vars.AVDs))
		for _, avd := range vars.AVDs {
			if avd.Name == "" || avd.Image == "" {
				return nil, fmt.Errorf("desktop %s saved AVD definition is incomplete", d.DesktopID)
			}
			names = append(names, avd.Name)
		}
		return names, nil
	default:
		return nil, fmt.Errorf("desktop %s has unsupported restore version %d", d.DesktopID, d.RestoreSpec.Version)
	}
}

func validateAVDRestore(d *store.Desktop) error {
	if d.RestoreSpec == nil {
		return fmt.Errorf("desktop %s has no restore specification", d.DesktopID)
	}
	if d.NestedVirt && d.RestoreSpec.Config["nestedVirtualization"] != "true" {
		return fmt.Errorf("desktop %s restore settings do not enable nested virtualization", d.DesktopID)
	}
	saved, err := restoreAVDNames(d)
	if err != nil {
		return err
	}
	if len(saved) == 0 || len(saved) != len(d.AVDNames) {
		return fmt.Errorf("desktop %s saved AVD definitions differ from fleet record", d.DesktopID)
	}
	want := slices.Clone(d.AVDNames)
	slices.Sort(want)
	slices.Sort(saved)
	if !slices.Equal(saved, want) {
		return fmt.Errorf("desktop %s saved AVD definitions differ from fleet record", d.DesktopID)
	}
	return nil
}

func nestedRestoreLaunchParams(d *store.Desktop, config pulumi.StackConfig) (*instanceLaunchParams, error) {
	if !d.NestedVirt || config["nestedVirtualization"] != "true" {
		return nil, fmt.Errorf("desktop %s has no nested virtualization launch settings", d.DesktopID)
	}
	volumeSize, err := strconv.Atoi(config["volumeSize"])
	if err != nil || volumeSize <= 0 {
		return nil, fmt.Errorf("desktop %s has invalid saved root volume size", d.DesktopID)
	}
	for _, key := range []string{"amiId", "instanceType", "subnetId", "securityGroupId", "instanceProfile", "userDataBase64"} {
		if config[key] == "" {
			return nil, fmt.Errorf("desktop %s restore settings lack %s", d.DesktopID, key)
		}
	}
	return &instanceLaunchParams{
		amiID: config["amiId"], instanceType: config["instanceType"], subnetID: config["subnetId"],
		sgID: config["securityGroupId"], instanceProfile: config["instanceProfile"], sshKeyName: config["sshKeyName"],
		userDataBase64: config["userDataBase64"], volumeSize: volumeSize, hostname: d.Hostname,
		desktopID: d.DesktopID, githubOwner: d.GitHubOwner, environment: d.Environment,
		marketType: config["marketType"], spotMaxPrice: config["spotMaxPrice"],
	}, nil
}

func prepareNestedUnpark(ctx context.Context, s store.Store, d *store.Desktop, config pulumi.StackConfig) error {
	return prepareNestedUnparkWith(ctx, s, d, config,
		func(ctx context.Context, params *instanceLaunchParams, persist func(string) error) (string, error) {
			instanceID, err := launchNestedVirtInstance(ctx, d.Region, cfg.AWS.Profile, params, persist)
			if err != nil && instanceID != "" {
				recorded, getErr := s.Get(context.Background(), d.DesktopID)
				if getErr == nil && recorded.InstanceID != instanceID {
					err = errors.Join(err, terminatePrelaunchedInstance(context.Background(), d.Region, cfg.AWS.Profile, instanceID))
				}
			}
			return instanceID, err
		},
		func(ctx context.Context, instanceID string) (*awsx.EC2InstanceStatus, error) {
			awsCfg, err := awsx.LoadConfig(ctx, d.Region, cfg.AWS.Profile)
			if err != nil {
				return nil, err
			}
			return awsx.InstanceStatus(ctx, awsCfg, instanceID)
		})
}

func prepareNestedUnparkWith(ctx context.Context, s store.Store, d *store.Desktop, config pulumi.StackConfig,
	launch func(context.Context, *instanceLaunchParams, func(string) error) (string, error),
	inspect func(context.Context, string) (*awsx.EC2InstanceStatus, error),
) error {
	params, err := nestedRestoreLaunchParams(d, config)
	if err != nil {
		return err
	}
	current, err := s.Get(ctx, d.DesktopID)
	if err != nil {
		return err
	}
	if current.State != store.StateRestoring || current.UnparkOperationToken != d.UnparkOperationToken {
		return store.ErrStateConflict
	}
	instanceID := current.InstanceID
	if instanceID == "" {
		instanceID, err = launch(ctx, params, func(id string) error {
			owned, err := s.Get(ctx, d.DesktopID)
			if err != nil {
				return err
			}
			if owned.State != store.StateRestoring || owned.UnparkOperationToken != d.UnparkOperationToken || owned.InstanceID != "" {
				return store.ErrStateConflict
			}
			owned.InstanceID = id
			return s.Update(ctx, owned)
		})
		if err != nil {
			return fmt.Errorf("prelaunch nested virtualization instance: %w", err)
		}
		persisted, err := s.Get(ctx, d.DesktopID)
		if err != nil {
			return fmt.Errorf("check prelaunched instance %s fleet record: %w", instanceID, err)
		}
		if persisted.InstanceID != instanceID || persisted.State != store.StateRestoring || persisted.UnparkOperationToken != d.UnparkOperationToken {
			return fmt.Errorf("prelaunched instance %s was not safely recorded in the restoring fleet record: %w", instanceID, store.ErrStateConflict)
		}
	}
	status, err := inspect(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("inspect nested virtualization instance %s: %w", instanceID, err)
	}
	if status == nil || status.State != "running" || status.NestedVirtualization != "enabled" || status.InstanceType != params.instanceType || status.ImageID != params.amiID || status.SubnetID != params.subnetID || !slices.Contains(status.SecurityGroupIDs, params.sgID) {
		return fmt.Errorf("instance %s does not match nested virtualization restore settings", instanceID)
	}
	config["importInstanceId"] = instanceID
	return nil
}
