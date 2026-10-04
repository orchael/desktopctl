package cmd

import (
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
)

func updateDesktopFromPulumiOutputs(d *store.Desktop, outputs map[string]string) {
	if v := outputs[pulumi.OutputHostname]; v != "" {
		d.Hostname = v
	}
	if v := outputs[pulumi.OutputNoVNCURL]; v != "" {
		d.NoVNCURL = v
	}
	if v := outputs[pulumi.OutputSSHTarget]; v != "" {
		d.SSHTarget = v
	}
}
