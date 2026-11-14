module github.com/rstreamlabs/rstream-go

go 1.27.4

require (
	github.com/charmbracelet/ultraviolet v0.0.0-20261023060817-a45b7c86333f
	github.com/charmbracelet/x/vt v0.1.0
	github.com/creack/pty v1.1.24
	github.com/eclipse-keypont/crypto11 v1.6.8
	github.com/fsnotify/fsnotify v1.10.1
	github.com/gdamore/tcell/v2 v2.13.10
	github.com/gorilla/websocket v1.5.3
	github.com/johnstarich/go/dns v0.2.5
	github.com/lmittmann/tint v1.2.1
	github.com/miekg/dns v1.1.73
	github.com/moby/moby/api v1.57.0
	github.com/moby/moby/client v0.6.3
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/pion/dtls/v3 v3.1.15
	github.com/pion/dtls/v4 v4.0.2
	github.com/pion/ice/v4 v4.4.9
	github.com/pion/sctp v1.12.1
	github.com/pion/transport/v4 v4.1.3
	github.com/pion/transport/v5 v5.1.5
	github.com/pion/turn/v5 v5.1.3
	github.com/pion/webrtc/v4 v4.2.26
	github.com/quic-go/connect-ip-go v0.6.0
	github.com/quic-go/masque-go v0.6.0
	github.com/quic-go/quic-go v0.64.1
	github.com/quic-go/webtransport-go v0.13.0
	github.com/rivo/tview v0.42.0
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.1.0
	github.com/yosida95/uritemplate/v3 v3.0.2
	golang.org/x/crypto v0.59.0
	golang.org/x/net v0.62.0
	golang.org/x/sys v0.50.0
	golang.org/x/term v0.48.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/Microsoft/go-winio v0.6.4 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/x/ansi v0.11.9 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/go-connections v0.8.2 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dunglas/httpsfv v1.1.2 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.31 // indirect
	github.com/miekg/pkcs11 v1.1.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pion/datachannel v1.6.4 // indirect
	github.com/pion/interceptor v0.1.51 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/mdns/v2 v2.2.2 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/rtcp v1.2.20 // indirect
	github.com/pion/rtp v1.10.6 // indirect
	github.com/pion/sdp/v3 v3.0.20 // indirect
	github.com/pion/srtp/v3 v3.1.3 // indirect
	github.com/pion/stun/v4 v4.0.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/go-internal v1.16.0 // indirect
	github.com/thales-e-security/pool v0.0.2 // indirect
	github.com/wlynxg/anet v0.0.5 // indirect
	github.com/xo/terminfo v1.3.1 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.72.0 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.uber.org/mock v0.6.0 // indirect
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/sync v0.25.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	golang.org/x/time v0.16.0 // indirect
)

retract (
	v1.26.3 // Superseded after a documentation correction.
	v1.26.2 // Superseded after a documentation correction.
)
