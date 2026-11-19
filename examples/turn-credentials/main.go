// See LICENSE file in the project root for license information.

// turn-credentials fetches short-lived TURN credentials from the rstream API
// and prints them as JSON. These credentials can be fed directly into a WebRTC
// ICE configuration so media traffic is relayed through rstream's TURN
// infrastructure without managing a separate TURN server.
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
	flag.Usage = func() {
		fmt.Fprintln(flag.CommandLine.Output(), "Request short-lived TURN credentials for the selected rstream context.")
	}
	flag.Parse()
	if flag.NArg() != 0 {
		flag.Usage()
		os.Exit(2)
	}

	turn, err := config.CreateTURNCredentialsFromEnv(
		context.Background(),
	)
	if err != nil {
		log.Fatalf("failed to create TURN credentials: %v", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(turn); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode TURN credentials: %v\n", err)
		os.Exit(1)
	}
}
