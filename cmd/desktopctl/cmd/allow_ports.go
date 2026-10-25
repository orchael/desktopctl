package cmd

import (
	"encoding/json"
	"fmt"
)

// encodeAllowedPorts validates the public TCP ports shared by create and
// init-foundation, then writes a JSON array for Pulumi's GetObject.
func encodeAllowedPorts(ports []int) (string, error) {
	unique := make([]int, 0, len(ports))
	seen := make(map[int]bool, len(ports))
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return "", fmt.Errorf("--allow-port must be between 1 and 65535, got %d", port)
		}
		if port == 22 {
			return "", fmt.Errorf("--allow-port 22 is managed by desktop.operator_cidr")
		}
		if !seen[port] {
			unique = append(unique, port)
			seen[port] = true
		}
	}
	encoded, err := json.Marshal(unique)
	if err != nil {
		return "", fmt.Errorf("encode allowed ports: %w", err)
	}
	return string(encoded), nil
}
