// See LICENSE file in the project root for license information.

// tcp-ssh-server publishes a minimal SSH service through a raw TCP tunnel.
// SSH provides downstream encryption and authentication; rstream forwards the
// TCP connection without terminating or modifying the SSH protocol.

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
	"golang.org/x/crypto/ssh"
)

const (
	passwordEnvironmentVariable = "RSTREAM_SSH_PASSWORD"
	sshCommand                  = "rstream-demo"
)

type execRequest struct {
	Command string
}

type exitStatus struct {
	Status uint32
}

func newSSHServerConfig(username, password string) (*ssh.ServerConfig, ssh.PublicKey, error) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate SSH host key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create SSH host signer: %w", err)
	}
	serverConfig := &ssh.ServerConfig{PasswordCallback: func(metadata ssh.ConnMetadata, provided []byte) (*ssh.Permissions, error) {
		validUser := subtle.ConstantTimeCompare([]byte(metadata.User()), []byte(username))
		validPassword := subtle.ConstantTimeCompare(provided, []byte(password))
		if validUser != 1 || validPassword != 1 {
			return nil, errors.New("SSH authentication failed")
		}
		return nil, nil
	}}
	serverConfig.AddHostKey(signer)
	return serverConfig, signer.PublicKey(), nil
}

func handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			if err := request.Reply(false, nil); err != nil {
				log.Printf("Reject SSH request: %v", err)
			}
			continue
		}
		payload := execRequest{}
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Command != sshCommand {
			if err := request.Reply(false, nil); err != nil {
				log.Printf("Reject SSH command: %v", err)
			}
			continue
		}
		if err := request.Reply(true, nil); err != nil {
			log.Printf("Accept SSH command: %v", err)
			return
		}
		if _, err := io.WriteString(channel, "SSH over an rstream TCP tunnel\n"); err != nil {
			log.Printf("Write SSH response: %v", err)
			return
		}
		if _, err := channel.SendRequest("exit-status", false, ssh.Marshal(exitStatus{Status: 0})); err != nil {
			log.Printf("Send SSH exit status: %v", err)
		}
		return
	}
}

func handleSSHConnection(conn net.Conn, serverConfig *ssh.ServerConfig) {
	defer conn.Close()
	serverConnection, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
	if err != nil {
		log.Printf("SSH handshake from %s failed: %v", conn.RemoteAddr(), err)
		return
	}
	defer serverConnection.Close()
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			if err := channelRequest.Reject(ssh.UnknownChannelType, "only SSH sessions are supported"); err != nil {
				log.Printf("Reject SSH channel: %v", err)
			}
			continue
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			log.Printf("Accept SSH session: %v", err)
			continue
		}
		go handleSession(channel, channelRequests)
	}
}

func run(ctx context.Context, client *rstream.Client, name, username, password string, reservedPort uint32) error {
	serverConfig, publicKey, err := newSSHServerConfig(username, password)
	if err != nil {
		return err
	}
	control, err := client.Connect(ctx, nil)
	if err != nil {
		return fmt.Errorf("connect to rstream engine: %w", err)
	}
	defer control.Close()
	properties := rstream.TunnelProperties{Name: rstream.StringPtr(name), Protocol: rstream.ProtocolPtr(rstream.ProtocolTCP)}
	if reservedPort != 0 {
		properties.Port = rstream.Uint32Ptr(reservedPort)
	}
	tunnel, err := control.CreateTunnel(ctx, properties)
	if err != nil {
		return fmt.Errorf("create published TCP tunnel: %w", err)
	}
	defer tunnel.Close()
	listener, ok := tunnel.(net.Listener)
	if !ok {
		return errors.New("published TCP tunnel does not implement net.Listener")
	}
	address, err := tunnel.ForwardingAddress()
	if err != nil {
		return fmt.Errorf("read published TCP address: %w", err)
	}
	fmt.Printf("SSH address: %s\n", address)
	fmt.Printf("SSH host key fingerprint: %s\n", ssh.FingerprintSHA256(publicKey))
	go func() {
		<-ctx.Done()
		if err := tunnel.Close(); err != nil {
			log.Printf("Close TCP tunnel: %v", err)
		}
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept published TCP connection: %w", err)
		}
		go handleSSHConnection(conn, serverConfig)
	}
}

func main() {
	name := flag.String("name", "tcp-ssh", "tunnel name")
	username := flag.String("user", "rstream", "SSH username")
	reservedPort := flag.Uint("tcp-port", 0, "reserved published TCP port")
	flag.Parse()
	if *reservedPort > 65535 {
		log.Fatal("TCP port must be between 1 and 65535")
	}
	password := os.Getenv(passwordEnvironmentVariable)
	if password == "" {
		log.Fatalf("%s is required", passwordEnvironmentVariable)
	}
	client, err := config.NewClientFromEnv()
	if err != nil {
		log.Fatalf("Configuration error: %v", err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, client, *name, *username, password, uint32(*reservedPort)); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
