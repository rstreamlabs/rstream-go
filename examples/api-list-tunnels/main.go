// See LICENSE file in the project root for license information.

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
