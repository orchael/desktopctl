package desktop

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/store"
)

// ReconcileNestedInstances confirms that both the recorded instance and any
// tagged prelaunch instance are gone before an EFS claim can be released.
func ReconcileNestedInstances(ctx context.Context, d *store.Desktop, profile string,
	list func(context.Context, string, string, *store.Desktop) ([]string, error),
	terminate func(context.Context, string, string, string) error,
) error {
	if !d.NestedVirt {
		return nil
	}
	ids, err := list(ctx, d.Region, profile, d)
	if err != nil {
		return fmt.Errorf("list nested instances for desktop %s: %w", d.DesktopID, err)
	}
	if d.InstanceID != "" {
		ids = append(ids, d.InstanceID)
	}
	if len(ids) == 0 && (d.State != store.StateParked || d.ParkedAt == "") {
		return fmt.Errorf("cannot confirm absence of nested instance for desktop %s after unknown launch outcome", d.DesktopID)
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if err := terminate(ctx, d.Region, profile, id); err != nil {
			return fmt.Errorf("verify nested instance %s terminated: %w", id, err)
		}
	}
	return nil
}

func ReconcileNestedInstancesAWS(ctx context.Context, d *store.Desktop, profile string) error {
	return ReconcileNestedInstances(ctx, d, profile, listNestedInstancesAWS, terminateNestedInstanceAWS)
}

func listNestedInstancesAWS(ctx context.Context, region, profile string, d *store.Desktop) ([]string, error) {
	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		return nil, err
	}
	client := ec2.NewFromConfig(awsCfg)
	paginator := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{Filters: []ec2types.Filter{
		{Name: aws.String("tag:managed-by"), Values: []string{"desktopctl"}},
		{Name: aws.String("tag:desktop-id"), Values: []string{d.DesktopID}},
		{Name: aws.String("tag:environment"), Values: []string{d.Environment}},
	}})
	var ids []string
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, reservation := range page.Reservations {
			for _, instance := range reservation.Instances {
				if id := aws.ToString(instance.InstanceId); id != "" {
					ids = append(ids, id)
				}
			}
		}
	}
	return ids, nil
}

func terminateNestedInstanceAWS(ctx context.Context, region, profile, id string) error {
	awsCfg, err := awsx.LoadConfig(ctx, region, profile)
	if err != nil {
		return err
	}
	client := ec2.NewFromConfig(awsCfg)
	out, err := client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		return err
	}
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			if instance.State != nil && instance.State.Name == ec2types.InstanceStateNameTerminated {
				return nil
			}
		}
	}
	if _, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: []string{id}}); err != nil {
		return err
	}
	return ec2.NewInstanceTerminatedWaiter(client).Wait(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}}, 2*time.Minute)
}
