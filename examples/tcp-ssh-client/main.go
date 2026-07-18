// See LICENSE file in the project root for license information.

// tcp-ssh-client connects to tcp-ssh-server through its published TCP address.
// It uses a standard SSH client and verifies the ephemeral host key fingerprint
// printed by the server.

package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	passwordEnvironmentVariable = "RSTREAM_SSH_PASSWORD"
	sshCommand                  = "rstream-demo"
)

func hostKeyCallback(expectedFingerprint string) ssh.HostKeyCallback {
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actualFingerprint := ssh.FingerprintSHA256(key)
		if subtle.ConstantTimeCompare([]byte(actualFingerprint), []byte(expectedFingerprint)) != 1 {
			return fmt.Errorf("unexpected SSH host key fingerprint %q", actualFingerprint)
		}
		return nil
	}
}

func normalizeAddress(value string) (string, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, " (tcp)")
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("parse published TCP address: %w", err)
		}
		if parsed.Scheme != "tcp" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("invalid published TCP address %q", value)
		}
		value = parsed.Host
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return "", fmt.Errorf("invalid published TCP address %q", value)
	}
	parsedPort, err := strconv.ParseUint(port, 10, 16)
	if err != nil || parsedPort == 0 {
		return "", fmt.Errorf("invalid published TCP port %q", port)
	}
	return net.JoinHostPort(host, strconv.FormatUint(parsedPort, 10)), nil
}

func run(address, username, password, fingerprint string) error {
	address, err := normalizeAddress(address)
	if err != nil {
		return err
	}
	client, err := ssh.Dial("tcp", address, &ssh.ClientConfig{User: username, Auth: []ssh.AuthMethod{ssh.Password(password)}, HostKeyCallback: hostKeyCallback(fingerprint), Timeout: 10 * time.Second})
	if err != nil {
		return fmt.Errorf("connect to published SSH service: %w", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("open SSH session: %w", err)
	}
	defer session.Close()
	output, err := session.Output(sshCommand)
	if err != nil {
		return fmt.Errorf("run SSH command: %w", err)
	}
	fmt.Print(string(output))
	return nil
}

func main() {
	address := flag.String("address", "", "published TCP address or URL")
	fingerprint := flag.String("fingerprint", "", "expected SHA256 SSH host key fingerprint")
	username := flag.String("user", "rstream", "SSH username")
	flag.Parse()
	if strings.TrimSpace(*address) == "" {
		log.Fatal("-address is required")
	}
	if strings.TrimSpace(*fingerprint) == "" {
		log.Fatal("-fingerprint is required")
	}
	password := os.Getenv(passwordEnvironmentVariable)
	if password == "" {
		log.Fatalf("%s is required", passwordEnvironmentVariable)
	}
	if err := run(*address, *username, password, *fingerprint); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
