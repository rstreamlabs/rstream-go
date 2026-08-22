module github.com/rstreamlabs/rstream-go

go 1.26.5

require (
	github.com/charmbracelet/ultraviolet v0.0.0-20260812204455-68fa937c71be
	github.com/charmbracelet/x/vt v0.0.0-20260816001655-68d539dca504
	github.com/creack/pty v1.1.24
	github.com/eclipse-keypont/crypto11 v1.6.8
	github.com/fsnotify/fsnotify v1.10.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/websocket v1.5.3
	github.com/johnstarich/go/dns v0.2.5
	github.com/lmittmann/tint v1.2.0
	github.com/miekg/dns v1.1.73
	github.com/moby/moby/api v1.55.0
	github.com/moby/moby/client v0.5.1
	github.com/pelletier/go-toml/v2 v2.4.3
	github.com/pion/dtls/v3 v3.1.5
	github.com/pion/sctp v1.11.1
	github.com/quic-go/connect-ip-go v0.1.0
	github.com/quic-go/masque-go v0.4.0
	github.com/rivo/tview v0.42.0
	github.com/spf13/cobra v1.10.2
	github.com/yosida95/uritemplate/v3 v3.0.2
	golang.org/x/crypto v0.55.0
	golang.org/x/net v0.58.0
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/Microsoft/go-winio v0.6.2 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/x/ansi v0.11.8 // indirect
	github.com/charmbracelet/x/exp/ordered v0.1.0 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/containerd/errdefs v1.0.0 // indirect
	github.com/containerd/errdefs/pkg v0.3.0 // indirect
	github.com/distribution/reference v0.6.0 // indirect
	github.com/docker/go-connections v0.8.1 // indirect
	github.com/docker/go-units v0.5.0 // indirect
	github.com/dunglas/httpsfv v1.1.1 // indirect
	github.com/felixge/httpsnoop v1.1.0 // indirect
	github.com/gdamore/encoding v1.0.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.28 // indirect
	github.com/miekg/pkcs11 v1.1.2 // indirect
	github.com/moby/docker-image-spec v1.3.1 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/opencontainers/go-digest v1.0.0 // indirect
	github.com/opencontainers/image-spec v1.1.1 // indirect
	github.com/pion/logging v0.2.4 // indirect
	github.com/pion/randutil v0.1.0 // indirect
	github.com/pion/transport/v4 v4.1.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	github.com/thales-e-security/pool v0.0.2 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.70.0 // indirect
	go.opentelemetry.io/otel v1.45.0 // indirect
	go.opentelemetry.io/otel/metric v1.45.0 // indirect
	go.opentelemetry.io/otel/trace v1.45.0 // indirect
	go.uber.org/mock v0.6.0 // indirect
	golang.org/x/exp v0.0.0-20260718201538-764159d718ef // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	pgregory.net/rapid v1.3.0 // indirect
)

require (
	github.com/gdamore/tcell/v2 v2.13.10
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/quic-go/quic-go v0.60.0
	github.com/quic-go/webtransport-go v0.11.1
	github.com/spf13/pflag v1.0.10 // indirect
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	golang.org/x/text v0.41.0 // indirect
)

retract (
	v1.26.3 // Superseded after a documentation correction.
	v1.26.2 // Superseded after a documentation correction.
)
