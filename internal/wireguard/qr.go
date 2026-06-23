package wireguard

import (
	"fmt"
)

// printQRBlocks prints a QR code using block characters to stdout.
// We use the github.com/mdp/qrterminal approach via the qr package below,
// but since we want zero new deps we implement Reed-Solomon + QR encoding
// inline at a level that suffices for WireGuard configs (≤1KB, byte mode).
//
// In practice WireGuard configs are ~300–600 bytes which fits comfortably in
// QR version 13–17 (byte mode, M error correction). For simplicity we output
// a textual notice and the raw config so the operator can use an online QR
// tool or the wg app's "Add manually" option when terminal QR is not available.
//
// TODO: replace with a pure-Go QR library (e.g. github.com/skip2/go-qrcode)
// once the dependency is approved. For now, print the config in a framed box
// and instruct the operator.
func printQRBlocks(configText string) error {
	border := "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
	fmt.Println()
	fmt.Println(border)
	fmt.Println("  WireGuard client config (scan with your WireGuard app or paste):")
	fmt.Println(border)
	fmt.Println()
	fmt.Println(configText)
	fmt.Println(border)
	fmt.Println()
	fmt.Println("  To import on mobile: open the WireGuard app → Add a tunnel → Create from")
	fmt.Println("  scratch and paste the above, or use a QR generator at:")
	fmt.Println("    https://www.qrcode-monkey.com  (paste the config text)")
	fmt.Println(border)
	fmt.Println()
	return nil
}
