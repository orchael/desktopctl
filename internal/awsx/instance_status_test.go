package awsx

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

func TestInstanceStatusHibernationConfiguration(t *testing.T) {
	for _, value := range []string{"true", "false", ""} {
		t.Run("configured="+value, func(t *testing.T) {
			hibernation := ""
			if value != "" {
				hibernation = fmt.Sprintf("<hibernationOptions><configured>%s</configured></hibernationOptions>", value)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/xml")
				fmt.Fprintf(w, `<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet><item><instancesSet><item><instanceId>i-test</instanceId><instanceState><name>running</name></instanceState>%s</item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, hibernation)
			}))
			defer server.Close()
			status, err := InstanceStatus(context.Background(), aws.Config{
				Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
				BaseEndpoint: aws.String(server.URL), HTTPClient: server.Client(),
			}, "i-test")
			if err != nil {
				t.Fatal(err)
			}
			if status.HibernationConfigured != (value == "true") {
				t.Fatalf("hibernation configured = %v for %q", status.HibernationConfigured, value)
			}
		})
	}
}
