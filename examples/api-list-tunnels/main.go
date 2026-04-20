// See LICENSE file in the project root for license information.

// api-list-tunnels lists all active tunnels registered with the rstream engine
// and prints them as JSON. It uses only the rstream config API — no server-side
// tunnel is created — making it ideal for service discovery and health checks.
//
// Run: go run .

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	flag.Parse()
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	tunnels, err := client.ListTunnels(context.Background(), nil)
	if err != nil {
		log.Fatalf("failed to list tunnels: %v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(tunnels); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode tunnels: %v\n", err)
		os.Exit(1)
	}
}
