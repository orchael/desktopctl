package cmd

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type fakeIdentityClient struct {
	output *sts.GetCallerIdentityOutput
	err    error
}

func (f fakeIdentityClient) GetCallerIdentity(context.Context, *sts.GetCallerIdentityInput, ...func(*sts.Options)) (*sts.GetCallerIdentityOutput, error) {
	return f.output, f.err
}

func TestValidateAWSIdentity(t *testing.T) {
	account, arn, err := validateAWSIdentity(context.Background(), fakeIdentityClient{output: &sts.GetCallerIdentityOutput{
		Account: aws.String("123456789012"), Arn: aws.String("arn:aws:sts::123456789012:assumed-role/desktop/session"),
	}})
	if err != nil || account != "123456789012" || arn == "" {
		t.Fatalf("identity = %q, %q, %v", account, arn, err)
	}
	_, _, err = validateAWSIdentity(context.Background(), fakeIdentityClient{err: errors.New("denied")})
	if err == nil {
		t.Fatal("expected STS error")
	}
	_, _, err = validateAWSIdentity(context.Background(), fakeIdentityClient{output: &sts.GetCallerIdentityOutput{}})
	if err == nil {
		t.Fatal("expected missing account rejection")
	}
}

func TestDestroyAlias(t *testing.T) {
	if command, _, err := rootCmd.Find([]string{"destroy", "d-0123abcd"}); err != nil || command != terminateCmd {
		t.Fatalf("destroy command = %v, %v", command, err)
	}
}
