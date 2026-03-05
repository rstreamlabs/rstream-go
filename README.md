# rstream

Transform localhost into global reach through secure tunneling.

rstream tackles the complexities of crafting and overseeing secure, scalable network infrastructures. Our tunneling approach simplifies infrastructure-as-a-service deployment, enabling the exposure of previously unreachable resources.

## What is a tunnel?

A tunnel is a secure method for connecting network resources without needing a publicly routable IP address. It uses outbound-only connections to an edge network, enhancing security and minimizing exposure to external threats. Tunnels transmit encrypted data securely, prevent DDoS attacks, conceal IP addresses, and integrate legacy protocols.

When you create a tunnel with rstream, you get a public URL (the forwarding address) that automatically redirects all incoming traffic to your local service. For example, a tunnel for `localhost:8080` provides a public URL like `https://abc123.rstream.io` that forwards all requests to your local port 8080.

## How rstream works

rstream establishes secure outbound connections (tunnels) between your resources and rstream's edge network. When a client wants to access your resources, they establish an incoming connection to rstream's servers. The connection is authenticated and is then redirected to your resources using the previously established secure tunnel as a signaling channel.

All data streams are encrypted with rstream, ensuring complete security for your communications.

## Tunnel Types

rstream supports two fundamental tunnel types:

**Bytestream Tunnels** (TCP-like): Reliable, ordered data transmission for protocols like HTTP, TLS, SSH, and database connections.

**Datagram Tunnels** (UDP-like): Low-latency, connectionless communication for protocols like DTLS, QUIC, DNS, and real-time applications.

## Published vs Private Tunnels

**Published Tunnels**: Accessible via standard clients (browsers, curl, etc.) through public URLs. All protocols (HTTP, TLS, DTLS, QUIC) can be published and support various authentication mechanisms including token-based auth, rstream account authentication, and challenge/captcha systems.

**Private Tunnels**: Require an rstream client to connect. These provide enhanced security for internal services and are accessed by name (if specified) or by ID using the rstream dialer instead of public URLs.

## Use Cases

**Local Development**: With rstream, transition from localhost to globally reachable setups effortlessly, simplifying development workflows and deployment processes.

**Network Protection**: rstream boosts network security by managing access, preventing DDoS, hiding IPs, and encapsulating legacy protocols within a secure environment.

**Fleet Management**: rstream enhances fleet management by controlling resource access and acting as a signaling layer through its API, enabling advanced middleware solutions.

**Generative AI Solutions**: Utilize rstream for generative AI, distributing workloads across GPU instances and authenticating users for secure, scalable AI deployments.

**Real-Time Communications**: Enable low-latency streaming and real-time communications. Ideal for telemetry, metrics, and video streaming, ensuring swift data transfer and immediate response.

## Supported Features

**Core Tunneling**: Create secure tunnels for any TCP or UDP application with zero network configuration. Supports both published (public URL) and private (client-only) tunnel modes.

**Multi-Protocol Support**: HTTP (1.1, 2, 3), TLS, DTLS, QUIC, WebSockets, and WebTransport protocols with automatic protocol detection and optimization.

**Enterprise Security**: Geographic restrictions, IP whitelisting, mutual TLS authentication, token-based auth, rstream account authentication, and challenge/captcha protection.

**Zero Configuration**: No firewall rules, port forwarding, or NAT configuration required. Works behind corporate firewalls and restrictive network environments.

**High Availability**: Automatic reconnection, connection multiplexing, and load balancing across multiple edge nodes.

**Developer Experience**: Native Go SDK, comprehensive CLI, real-time connection monitoring, and multiple output formats (JSON, interactive UI).

## Supported Protocols

**HTTP Protocols**: HTTP/1.1, HTTP/2 (H2C), HTTP/3 with WebSocket and WebTransport support. Includes automatic HTTPS termination.

**Secure Transport**: TLS 1.2/1.3 with custom cipher suites, DTLS for UDP traffic, QUIC with 0-RTT support, and mutual TLS (mTLS) client authentication.

**Network Protocols**: IPv4/IPv6 dual-stack, MPTCP for enhanced reliability, HTTP/SOCKS proxy support, and custom DNS resolution.

## Compatibility

rstream is compatible with Linux, macOS and Windows. Additionally, rstream supports other UNIX systems such as FreeBSD, OpenBSD and NetBSD through manual installation.

## Installation

### Local Build
```bash
make
```

### Debian/Ubuntu
```bash
sudo /bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install-debian.sh)"
```

### macOS
```bash
brew tap rstreamlabs/rstream && brew install rstream
```

### Manual Installation
```bash
/bin/bash -i -c "$(curl -fsSL https://rstream.io/scripts/install.sh)"
```

### Docker
```bash
docker pull rstream/rstream:latest
```

## Authentication

To function properly, rstream requires an authentication token. Create an account on https://rstream.io, generate a token, then register it on your machine:

```bash
rstream login ${AUTHENTICATION_TOKEN}
```

## Environment Variables

- `RSTREAM_CONFIG`: Override the CLI config file path.
- `RSTREAM_CONTEXT`: Select the context by name.
- `RSTREAM_ENGINE`: Override the engine URL (data plane).
- `RSTREAM_AUTHENTICATION_TOKEN`: Override the authentication token.
- `RSTREAM_API_URL`: Override the control-plane API URL (mostly for internal or advanced usage).

## Usage

### Basic HTTP Tunnel

```bash
# Create an HTTP tunnel for local port 8080 (default: HTTP protocol, published)
rstream forward 8080
```

This command creates a public HTTP tunnel and displays the forwarding address (e.g., `https://abc123.rstream.io`). Any HTTP request sent to this URL will be redirected to `localhost:8080`. The tunnel remains active until you stop the command.

### TLS Tunnel

```bash
# Create a secure TLS tunnel for local port 8080
rstream forward 8080 --tls
```

Creates a secure TLS-encrypted tunnel accessible through the rstream network. Standard TLS clients can connect to the tunnel's forwarding address.

### Private Tunnels

```bash
# Create a private tunnel (not publicly accessible)
rstream forward 22 --tls --no-publish --name ssh-tunnel
```

Private tunnels require rstream clients to connect and are identified by name or ID rather than public URLs.

### UDP/Datagram Tunnels

```bash
# Create a DTLS tunnel for UDP traffic on port 5000
rstream forward 5000 --dtls
```

DTLS tunnels automatically handle datagram traffic. The `--datagram` flag is implied with `--dtls`.

### Run (Declarative Tunnels)

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

## Build and Compilation

rstream includes a comprehensive Makefile supporting multiple platforms and packaging formats:

### Development
```bash
make          # Build for current platform
make clean    # Clean build artifacts
make tests    # Run test suite
make examples # Build example applications
```

### Cross-Platform Compilation
```bash
make cross    # Build for all supported platforms
```

Supports Linux, macOS, Windows, and BSD systems across multiple architectures including x86, ARM, MIPS, PowerPC, and RISC-V.

### Packaging
```bash
make pkg         # Create packages for current platform
make pkg-cross   # Create packages for all platforms
make deb         # Create Debian/Ubuntu packages
make docker      # Build Docker images
make nupkg       # Create Windows NuGet packages
```

## Code Examples

rstream includes a comprehensive Go SDK enabling developers to create secure tunnels programmatically. The client-side code is fully open source and inspectable, ensuring transparency and security. Additional examples are available in the `examples/` directory of this repository.

Examples use `config.NewClientFromEnv()` and read the same config/env settings as the CLI. Ensure a default context (or `RSTREAM_ENGINE`) is set, and provide `RSTREAM_AUTHENTICATION_TOKEN` if your engine requires authentication. Manually specifying engine/token is the exception, not the default.

### Manual Client Options (Exception)

Use this only when you need to bypass config/env resolution.

```go
client, err := rstream.NewClient(rstream.ClientOptions{
	Engine: "engine.example:443",
	Token:  "authentication_token",
})
if err != nil {
	panic(err)
}
```

### HTTP Server (Published Tunnel)

This example demonstrates how to create a public HTTP server accessible via standard web browsers. The server will be available through a public rstream URL.

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

### TLS Echo (Private Tunnel)

This example shows how to create a private TLS tunnel for secure internal communications. It includes both server and client code to demonstrate the complete workflow.

**Server Code:**
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
		Name:     rstream.StringPtr("echo"),
		Publish:  rstream.BoolPtr(false),
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

**Client Code:**
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

### DTLS Datagram Server

This example demonstrates creating a DTLS tunnel for secure UDP communication, ideal for real-time applications requiring both security and low latency.

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

### QUIC Server

This example demonstrates creating a QUIC tunnel for low-latency datagram communication with modern transport features.

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

## Contributing

We welcome contributions to rstream! Pull requests are encouraged and appreciated. Whether you're fixing bugs, adding features, improving documentation, or suggesting enhancements, your contributions help make rstream better for everyone.

## Support

**Get help:**  
support@rstream.io

**Report security concerns:**  
reports@rstream.io
