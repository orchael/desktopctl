package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/orchael/desktopctl/internal/desktop"
)

const profileBootstrapTimeout = 15 * time.Minute

type profileBootstrapRunner interface {
	Online(context.Context, string) (bool, error)
	Start(context.Context, string) (string, error)
	Result(context.Context, string, string) (string, error)
}

type ssmProfileBootstrapRunner struct {
	client *ssm.Client
}

func (r *ssmProfileBootstrapRunner) Online(ctx context.Context, instanceID string) (bool, error) {
	out, err := r.client.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
		Filters: []ssmtypes.InstanceInformationStringFilter{{
			Key:    aws.String("InstanceIds"),
			Values: []string{instanceID},
		}},
	})
	if err != nil {
		return false, fmt.Errorf("query SSM instance state: %w", err)
	}
	for _, instance := range out.InstanceInformationList {
		if aws.ToString(instance.InstanceId) == instanceID && instance.PingStatus == ssmtypes.PingStatusOnline {
			return true, nil
		}
	}
	return false, nil
}

func (r *ssmProfileBootstrapRunner) Start(ctx context.Context, instanceID string) (string, error) {
	out, err := r.client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String("AWS-RunShellScript"),
		Comment:      aws.String("Wait for ai-desktops desktop profile bootstrap"),
		Parameters: map[string][]string{
			"commands":         {"cloud-init status --wait >/dev/null 2>&1"},
			"executionTimeout": {"900"},
		},
	})
	if err != nil {
		return "", fmt.Errorf("start bootstrap wait through SSM: %w", err)
	}
	if out.Command == nil || aws.ToString(out.Command.CommandId) == "" {
		return "", fmt.Errorf("SSM returned no bootstrap wait command ID")
	}
	return aws.ToString(out.Command.CommandId), nil
}

func (r *ssmProfileBootstrapRunner) Result(ctx context.Context, instanceID, commandID string) (string, error) {
	out, err := r.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		InstanceId: aws.String(instanceID),
		CommandId:  aws.String(commandID),
	})
	if err != nil {
		var pending *ssmtypes.InvocationDoesNotExist
		if errors.As(err, &pending) {
			return "Pending", nil
		}
		return "", fmt.Errorf("read bootstrap wait result: %w", err)
	}
	if out.Status == ssmtypes.CommandInvocationStatusSuccess && out.ResponseCode != 0 {
		return "Failed", nil
	}
	return string(out.Status), nil
}

func waitForProfileBootstrap(ctx context.Context, runner profileBootstrapRunner, instanceID string, interval time.Duration) error {
	if instanceID == "" {
		return fmt.Errorf("desktop has no instance ID for profile bootstrap check")
	}
	if runner == nil {
		return fmt.Errorf("profile bootstrap checker is unavailable")
	}
	for {
		online, err := runner.Online(ctx, instanceID)
		if err != nil {
			return err
		}
		if online {
			break
		}
		if err := waitProfilePoll(ctx, interval); err != nil {
			return err
		}
	}
	commandID, err := runner.Start(ctx, instanceID)
	if err != nil {
		return err
	}
	for {
		status, err := runner.Result(ctx, instanceID, commandID)
		if err != nil {
			return err
		}
		switch status {
		case "Success":
			return nil
		case "Failed", "Cancelled", "TimedOut":
			return fmt.Errorf("cloud-init bootstrap returned %s", status)
		case "Pending", "InProgress", "Delayed", "Cancelling":
			if err := waitProfilePoll(ctx, interval); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected bootstrap command status %q", status)
		}
	}
}

func waitProfilePoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func completeCreateReadiness(ctx context.Context, mgr *desktop.Manager, desktopID, desktopProfile, instanceID string, runner profileBootstrapRunner, interval time.Duration) error {
	if desktopProfile != "" {
		if err := waitForProfileBootstrap(ctx, runner, instanceID, interval); err != nil {
			recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			message := "desktop profile bootstrap failed: " + err.Error()
			if recordErr := mgr.RecordFailure(recordCtx, desktopID, "desktop-profile", message); recordErr != nil {
				return fmt.Errorf("%s; also failed to record fleet state: %w", message, recordErr)
			}
			return fmt.Errorf("%s (desktop retained for diagnosis)", message)
		}
	}
	if err := mgr.MarkReady(ctx, desktopID, "provisioned"); err != nil {
		return fmt.Errorf("mark ready: %w", err)
	}
	return nil
}
