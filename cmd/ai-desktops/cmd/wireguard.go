package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var wireguardCmd = &cobra.Command{
	Use:   "wireguard",
	Short: "Manage WireGuard VPN peers for a desktop",
	Long: `wireguard provides subcommands for managing WireGuard VPN peer configuration
on an ai-desktops desktop.  Use the subcommands below to add, list, remove, or
inspect WireGuard peers.`,
}

var wireguardAddPeerCmd = &cobra.Command{
	Use:   "add-peer <desktop-id>",
	Short: "Add a WireGuard peer to the desktop",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("wireguard add-peer is not yet implemented")
	},
}

var wireguardListPeersCmd = &cobra.Command{
	Use:   "list-peers <desktop-id>",
	Short: "List WireGuard peers on the desktop",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("wireguard list-peers is not yet implemented")
	},
}

var wireguardRemovePeerCmd = &cobra.Command{
	Use:   "remove-peer <desktop-id> <peer-public-key>",
	Short: "Remove a WireGuard peer from the desktop",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("wireguard remove-peer is not yet implemented")
	},
}

var wireguardShowConfigCmd = &cobra.Command{
	Use:   "show-config <desktop-id>",
	Short: "Show the WireGuard interface configuration on the desktop",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("wireguard show-config is not yet implemented")
	},
}

func init() {
	wireguardCmd.AddCommand(wireguardAddPeerCmd)
	wireguardCmd.AddCommand(wireguardListPeersCmd)
	wireguardCmd.AddCommand(wireguardRemovePeerCmd)
	wireguardCmd.AddCommand(wireguardShowConfigCmd)
	rootCmd.AddCommand(wireguardCmd)
}
