package wireguard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/orchael/ai-desktops/internal/config"
)

const peersSSMPrefix = "/ai-desktops/wireguard/peers"

// PeerSSMPath returns the SSM parameter path for a named peer.
func PeerSSMPath(name string) string {
	return peersSSMPrefix + "/" + name
}

type ssmPeerValue struct {
	PublicKey string `json:"public_key"`
	AllowedIP string `json:"allowed_ip"`
}

// PutPeerSSM writes a peer's public key and allowed IP to SSM Parameter Store.
// The parameter is a plain String (public keys are not secret).
func PutPeerSSM(ctx context.Context, awsCfg aws.Config, peer config.WireGuardPeer) error {
	val, err := json.Marshal(ssmPeerValue{
		PublicKey: peer.PublicKey,
		AllowedIP: peer.AllowedIP,
	})
	if err != nil {
		return fmt.Errorf("marshal peer %q: %w", peer.Name, err)
	}
	c := ssm.NewFromConfig(awsCfg)
	overwrite := true
	_, err = c.PutParameter(ctx, &ssm.PutParameterInput{
		Name:      aws.String(PeerSSMPath(peer.Name)),
		Value:     aws.String(string(val)),
		Type:      ssmtypes.ParameterTypeString,
		Overwrite: &overwrite,
	})
	if err != nil {
		return fmt.Errorf("put SSM parameter for peer %q: %w", peer.Name, err)
	}
	return nil
}

// DeletePeerSSM removes a peer's parameter from SSM Parameter Store.
// Returns nil if the parameter does not exist (idempotent).
func DeletePeerSSM(ctx context.Context, awsCfg aws.Config, name string) error {
	c := ssm.NewFromConfig(awsCfg)
	_, err := c.DeleteParameter(ctx, &ssm.DeleteParameterInput{
		Name: aws.String(PeerSSMPath(name)),
	})
	if err != nil {
		var notFound *ssmtypes.ParameterNotFound
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("delete SSM parameter for peer %q: %w", name, err)
	}
	return nil
}

// ListPeersSSM reads all peers from SSM Parameter Store and returns them.
func ListPeersSSM(ctx context.Context, awsCfg aws.Config) ([]config.WireGuardPeer, error) {
	c := ssm.NewFromConfig(awsCfg)
	var peers []config.WireGuardPeer
	var nextToken *string
	for {
		out, err := c.GetParametersByPath(ctx, &ssm.GetParametersByPathInput{
			Path:      aws.String(peersSSMPrefix + "/"),
			Recursive: aws.Bool(false),
			NextToken: nextToken,
		})
		if err != nil {
			return nil, fmt.Errorf("list SSM peers: %w", err)
		}
		for _, p := range out.Parameters {
			name := strings.TrimPrefix(aws.ToString(p.Name), peersSSMPrefix+"/")
			var v ssmPeerValue
			if err := json.Unmarshal([]byte(aws.ToString(p.Value)), &v); err != nil {
				continue
			}
			peers = append(peers, config.WireGuardPeer{
				Name:      name,
				PublicKey: v.PublicKey,
				AllowedIP: v.AllowedIP,
			})
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	return peers, nil
}
