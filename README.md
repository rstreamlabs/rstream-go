# rstream-go

Transform localhost into global reach through secure tunneling.

`rstream-go` is the Go SDK for **rstream**, a secure connectivity platform built around a globally distributed edge network and lightweight agents. Agents maintain outbound-only tunnels from local and private environments, while the edge network authenticates traffic, enforces access policy, and routes requests to upstream services. rstream supports HTTP and non-HTTP workloads and provides end-to-end visibility through connection logs and metrics.

The Go SDK is the **reference implementation**. It covers the broadest rstream API surface and is the most complete SDK in terms of protocol support and tunnel lifecycle features. The rstream CLI is implemented in Go and lives in this repository, so the SDK and CLI share the same configuration model and operational behavior.

Looking for native integration? The C++ SDK is available at https://github.com/rstreamlabs/rstream-cpp.

## What is a tunnel?

A tunnel is a secure way to expose services without requiring inbound ports, public IPs, or NAT changes. In rstream, tunnels are established **outbound** to the edge network, reducing exposure while keeping access controllable and observable.

When you create a **published** tunnel with rstream, you get a forwarding address that routes inbound traffic to a local service. For example, a tunnel for `localhost:8080` provides a forwarding address like `https://abc123.rstream.io` that forwards HTTP requests to local port 8080.

## How rstream works

rstream establishes outbound tunnels between environments running services or devices and the rstream edge network. Clients connect to the edge using a forwarding address for published tunnels or a tunnel identifier for private tunnels. The edge authenticates the connection, applies policy, and forwards traffic through the existing tunnel path to the upstream service.

Tunnel transports are encrypted, and edge enforcement decisions are surfaced through logs and metrics.

## Tunnel types

rstream supports two fundamental tunnel types:

**Bytestream tunnels** (TCP-like) provide reliable, ordered transmission for protocols such as HTTP and TLS, as well as custom bytestream services.

**Datagram tunnels** (UDP-like) provide low-latency, message-oriented communication for protocols such as QUIC and DTLS, as well as custom datagram services.

## Published vs private tunnels

**Published tunnels** are accessible via standard clients (browsers, curl, etc.) through forwarding addresses. Published tunnels can be configured with edge authentication and access policies depending on protocol and deployment.

**Private tunnels** require an rstream client to connect. Private tunnels are accessed by name (if specified) or by ID through the rstream dialer instead of a public forwarding address.

## Use cases

**Local development**: Expose a local service for testing, demos, and collaboration without changing network configuration.

**Fleet operations**: Provide controlled access to devices and machines across environments with consistent identity, policy, and observability.

**Infrastructure and platforms**: Use rstream as a connectivity layer for internal tools, CI workflows, and production access paths.

**Generative AI workflows**: Distribute work across fleets of runners or machines while keeping access scoped and auditable.

**Real-time systems**: Support low-latency traffic patterns for telemetry, streaming, and datagram workloads.

## Supported features

**Core tunneling**: Create tunnels for TCP-like and UDP-like workloads with outbound-only connectivity.

**Multi-protocol support**: HTTP (1.1, 2, 3), TLS, DTLS, QUIC, plus WebSocket and WebTransport in HTTP tunnels.

**Access control**: IP restrictions, GeoIP policies, mutual TLS, token-based access, and account-based access depending on tunnel configuration.

**Operational visibility**: Connection logs and metrics for traffic, enforcement decisions, and performance signals.

**Transport configuration**: IPv4/IPv6 selection, DNS override, interface binding, and proxy support.

**Resilience**: Long-lived agents, reconnect behavior, and transport-level multiplexing for stable connectivity.

## Supported protocols

**HTTP protocols**: HTTP/1.1, HTTP/2 (H2C), HTTP/3 with WebSocket and WebTransport support.

**Secure transports**: TLS- and QUIC-based transports for agent-to-edge connectivity, plus DTLS and QUIC as published tunnel protocols when enabled by the deployment.

**Network options**: IPv4/IPv6, MPTCP, proxy support, and custom DNS resolution.

## Compatibility

rstream is compatible with Linux, macOS and Windows. Additionally, rstream supports other UNIX systems such as FreeBSD, OpenBSD and NetBSD through manual installation.

## Installation

### Local build
For a local source build on the current platform, run:
```bash
make
```

### Debian/Ubuntu
For Debian-based distributions, the installation script installs packaged binaries and dependencies:
```bash
sudo /bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install-debian.sh)"
```

### macOS
On macOS, the Homebrew tap provides the standard installation path:
```bash
brew tap rstreamlabs/rstream && brew install rstream
```

### Manual installation
For generic environments, use the manual installer script:
```bash
/bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install.sh)"
```

### Docker
If you run rstream in containers, pull the public image:
```bash
docker pull rstream/rstream:latest
```

## Authentication

To function properly, rstream requires an authentication token. Create an account on https://rstream.io, generate a token, then register it on your machine:

```bash
rstream login ${AUTHENTICATION_TOKEN}
```

## Environment variables

These variables are shared across CLI and SDK configuration resolution. Prefer configuration contexts for regular usage, and use overrides for automation or constrained environments.

- `RSTREAM_CONFIG`: Override the CLI config file path.
- `RSTREAM_CONTEXT`: Select the context by name.
- `RSTREAM_ENGINE`: Override the engine URL (data plane).
- `RSTREAM_AUTHENTICATION_TOKEN`: Override the authentication token.
- `RSTREAM_API_URL`: Override the control-plane API URL (mostly for internal or advanced usage).

Resolution behavior follows the same model used by `config.NewClientFromEnv()`: explicit SDK options are evaluated first, then environment overrides, then context/environment values from the config file. `RSTREAM_CONFIG` selects the config file path before fallback to the default config location.

## Usage

### Basic HTTP tunnel
Use this command to publish a local HTTP service with default protocol and publication settings.

```bash
# Create an HTTP tunnel for local port 8080 (default: HTTP protocol, published)
rstream forward 8080
```

This command creates a public HTTP tunnel and displays the forwarding address (e.g., `https://abc123.rstream.io`). Any HTTP request sent to this URL will be redirected to `localhost:8080`. The tunnel remains active until you stop the command.

### TLS tunnel
Use `--tls` when the exposed tunnel endpoint must terminate TLS.

```bash
# Create a secure TLS tunnel for local port 8080
rstream forward 8080 --tls
```

Creates a secure TLS-encrypted tunnel accessible through the rstream network. Standard TLS clients can connect to the tunnel's forwarding address.

### Private tunnels
Use `--no-publish` to create private tunnels that are reachable only through rstream clients.

```bash
# Create a private tunnel (not publicly accessible)
rstream forward 22 --tls --no-publish --name ssh-tunnel
```

Private tunnels require rstream clients to connect and are identified by name or ID rather than public URLs.

### UDP/datagram tunnels
For datagram transport, choose DTLS mode when you need encrypted UDP-style traffic.

```bash
# Create a DTLS tunnel for UDP traffic on port 5000
rstream forward 5000 --dtls
```

DTLS tunnels automatically handle datagram traffic. The `--datagram` flag is implied with `--dtls`.

### Run (declarative tunnels)

`rstream run` keeps tunnels in sync from a YAML file or Docker labels, with optional watch/reconcile.

- `forward`: creates a single tunnel from CLI args and forwards immediately (single tunnel, interactive).
- `run --apply`: declarative list of tunnels from YAML, supports watch/reconcile.
- `run --docker`: discovers tunnels from Docker labels, supports watch/reconcile.

```bash
# Apply a YAML spec once
rstream -v run --apply examples/run-yaml/tunnels.yaml

# Watch the YAML for changes and reconcile
rstream -v run --apply examples/run-yaml/tunnels.yaml --watch

# Discover tunnels from Docker labels
rstream -v run --docker --watch
```

See `docs/CMD_RUN.md` for the full YAML schema, Docker label reference, and reconciliation details.

## Build and compilation

rstream includes a comprehensive Makefile supporting multiple platforms and packaging formats.

### Development
For daily development loops, these targets build, test, and clean local artifacts.
```bash
make          # Build for current platform
make clean    # Clean build artifacts
make tests    # Run test suite
make examples # Build example applications
```

### Cross-platform compilation
When producing binaries for multiple targets, use:
```bash
make cross    # Build for all supported platforms
```

Supports Linux, macOS, Windows, and BSD systems across multiple architectures including x86, ARM, MIPS, PowerPC, and RISC-V.

### Packaging
For release packaging workflows, these targets build archives and platform packages:
```bash
make pkg         # Create packages for current platform
make pkg-cross   # Create packages for all platforms
make deb         # Create Debian/Ubuntu packages
make docker      # Build Docker images
make nupkg       # Create Windows NuGet packages
```

## Code examples

The Go SDK enables applications to create and manage tunnels programmatically. The examples below use `config.NewClientFromEnv()` to read the same config and environment settings as the CLI. Ensure a default context (or `RSTREAM_ENGINE`) is set, and provide `RSTREAM_AUTHENTICATION_TOKEN` if the selected engine requires authentication.

### Manual client options (exception)

Use this only when bypassing config and environment resolution is required.

```go
client, err := rstream.NewClient(rstream.ClientOptions{
	Engine: "engine.example:443",
	Token:  "authentication_token",
})
if err != nil {
	panic(err)
}
```

### HTTP server (published tunnel)

This example creates a published HTTP tunnel and serves requests through the tunnel listener. The forwarding address printed by `ForwardingAddress()` is the public URL that can be used from a browser or `curl`.

```go
package main

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Protocol:    rstream.ProtocolPtr(rstream.ProtocolHTTP),
		HTTPVersion: rstream.HTTPVersionPtr(rstream.HTTP1_1),
		Publish:     rstream.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	addr, _ := tunnel.ForwardingAddress()
	fmt.Printf("Server accessible at: %s\n", addr)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "Hello from rstream!")
	})
	http.Serve(tunnel.(net.Listener), nil)
}
```

### TLS echo (private tunnel)

This example shows a private tunnel workflow. The server creates a non-published tunnel named `echo` and accepts inbound tunnel connections. The client dials the private tunnel by name and exchanges data over the resulting stream.

**Server code:**
```go
package main

import (
	"context"
	"io"
	"log"
	"net"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Name:    rstream.StringPtr("echo"),
		Publish: rstream.BoolPtr(false),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	props, err := tunnel.Properties()
	if err != nil {
		panic(err)
	}
	log.Printf("Echo server running as private tunnel: %s", *props.Name)
	listener := tunnel.(net.Listener)
	for {
		conn, err := listener.Accept()
		if err != nil {
			break
		}
		go func(c net.Conn) {
			defer c.Close()
			log.Printf("New connection from %s", c.RemoteAddr())
			io.Copy(c, c)
		}(conn)
	}
}
```

**Client code:**
```go
package main

import (
	"context"
	"fmt"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	conn, err := client.Dial(context.Background(), rstream.Addr{
		IdOrName: "echo",
	})
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	message := "Hello from private tunnel!"
	conn.Write([]byte(message))
	buffer := make([]byte, len(message))
	conn.Read(buffer)
	fmt.Printf("Sent: %s\nReceived: %s\n", message, string(buffer))
}
```

### DTLS datagram server

This example creates a published DTLS datagram tunnel. The forwarding address is where datagram clients connect. The server uses the packet listener API to accept datagram sessions and echo packets.

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Name:     rstream.StringPtr("dtls"),
		Type:     rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Protocol: rstream.ProtocolPtr(rstream.ProtocolDTLS),
		Publish:  rstream.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	addr, _ := tunnel.ForwardingAddress()
	fmt.Printf("DTLS server accessible at: %s\n", addr)
	packetListener := tunnel.(rstream.PacketListener)
	for {
		conn, _, err := packetListener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go func() {
			defer conn.Close()
			buffer := make([]byte, 1024)
			for {
				n, addr, err := conn.ReadFrom(buffer)
				if err != nil {
					break
				}
				log.Printf("Received %d bytes from %s", n, addr)
				n, err = conn.WriteTo(buffer[:n], addr)
				if err != nil {
					break
				}
			}
		}()
	}
}
```

### QUIC server

This example creates a published QUIC datagram tunnel. QUIC is a modern transport designed for low latency and resilience to network changes. The sample generates a local TLS configuration and serves QUIC streams over the tunnel packet listener.

```go
package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"

	"github.com/quic-go/quic-go"
	"github.com/rstreamlabs/rstream-go"
	"github.com/rstreamlabs/rstream-go/config"
)

func generateTLSConfig() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{SerialNumber: big.NewInt(1)}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
	}, nil
}

func main() {
	client, err := config.NewClientFromEnv()
	if err != nil {
		panic(err)
	}
	ctrl, err := client.Connect(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	defer ctrl.Close()
	tunnel, err := ctrl.CreateTunnel(context.Background(), rstream.TunnelProperties{
		Name:     rstream.StringPtr("quic"),
		Type:     rstream.TunnelTypePtr(rstream.TunnelTypeDatagram),
		Protocol: rstream.ProtocolPtr(rstream.ProtocolQUIC),
		Publish:  rstream.BoolPtr(true),
	})
	if err != nil {
		panic(err)
	}
	defer tunnel.Close()
	addr, _ := tunnel.ForwardingAddress()
	fmt.Printf("QUIC server accessible at: %s\n", addr)
	tlsCfg, err := generateTLSConfig()
	if err != nil {
		panic(err)
	}
	transport := quic.Transport{
		Conn: rstream.PacketConnFromPacketListener(tunnel.(rstream.PacketListener)),
	}
	listener, err := transport.Listen(tlsCfg, nil)
	defer listener.Close()
	for {
		conn, err := listener.Accept(context.Background())
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		go func() {
			defer conn.CloseWithError(0, "server done")
			stream, err := conn.AcceptStream(context.Background())
			if err != nil {
				return
			}
			defer stream.Close()
			buffer := make([]byte, 1024)
			for {
				n, err := stream.Read(buffer)
				if err != nil {
					break
				}
				log.Printf("Received %d bytes from %s", n, conn.RemoteAddr())
				n, err = stream.Write(buffer[:n])
				if err != nil {
					break
				}
			}
		}()
	}
}
```

## References

- Documentation: https://rstream.io/docs
- Go SDK (reference implementation): https://github.com/rstreamlabs/rstream-go
- C++ SDK: https://github.com/rstreamlabs/rstream-cpp

## Contributing

Pull requests are encouraged and appreciated. Whether you're fixing bugs, adding features, improving documentation, or suggesting enhancements, your contributions help make rstream better for everyone. Build locally, run checks, and submit focused pull requests with clear validation notes.

## Support

**Get help:**  
support@rstream.io

**Report security concerns:**  
reports@rstream.io

## License

See `LICENSE` in the repository root.
