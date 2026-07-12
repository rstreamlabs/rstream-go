# Changelog

## [1.24.2](https://github.com/rstreamlabs/rstream-go/compare/v1.24.1...v1.24.2) (2026-07-12)


### Bug Fixes

* close netcat sessions on peer shutdown ([e1088ca](https://github.com/rstreamlabs/rstream-go/commit/e1088ca070a7a61b2e7e9b0d449032b909bd2de1))

## [1.24.1](https://github.com/rstreamlabs/rstream-go/compare/v1.24.0...v1.24.1) (2026-07-11)


### Bug Fixes

* clarify netcat datagram session lifecycle ([c40daff](https://github.com/rstreamlabs/rstream-go/commit/c40daff87728717ef22346f3708930803205aa88))

## [1.24.0](https://github.com/rstreamlabs/rstream-go/compare/v1.23.0...v1.24.0) (2026-07-11)


### Features

* add automatic transport selection and reliable datagrams ([2b6b1bd](https://github.com/rstreamlabs/rstream-go/commit/2b6b1bd8968b3bd47da796a65eaed546753572b8))

## [1.23.0](https://github.com/rstreamlabs/rstream-go/compare/v1.22.1...v1.23.0) (2026-06-25)


### Features

* add Claude Code MCP setup command ([78efdb0](https://github.com/rstreamlabs/rstream-go/commit/78efdb0d7cf288d0bbc54328dbe42e42884fbb29))
* add UDP and datagram netcat commands ([6bd6042](https://github.com/rstreamlabs/rstream-go/commit/6bd60428fce7767ccded12785e86136a98e7879b))

## [1.22.1](https://github.com/rstreamlabs/rstream-go/compare/v1.22.0...v1.22.1) (2026-06-24)


### Bug Fixes

* support WebTTY E2E cross builds on 32-bit targets ([#41](https://github.com/rstreamlabs/rstream-go/issues/41)) ([55be778](https://github.com/rstreamlabs/rstream-go/commit/55be77843d19e8714a4a0c588c6ffaf4a9a77b0a))

## [1.22.0](https://github.com/rstreamlabs/rstream-go/compare/v1.21.0...v1.22.0) (2026-06-24)


### Features

* add managed WebTTY workflows ([#39](https://github.com/rstreamlabs/rstream-go/issues/39)) ([d93b97a](https://github.com/rstreamlabs/rstream-go/commit/d93b97a49e512fc1b1b2e1252535bd19ab6bf378))

## [1.21.0](https://github.com/rstreamlabs/rstream-go/compare/v1.20.1...v1.21.0) (2026-06-02)


### Features

* add webhook-compatible event forwarding ([17f9ed5](https://github.com/rstreamlabs/rstream-go/commit/17f9ed5730a87b67ab1dd666461ca7cf92aef2a6))

## [1.20.1](https://github.com/rstreamlabs/rstream-go/compare/v1.20.0...v1.20.1) (2026-06-02)


### Bug Fixes

* install protoc for macos release builds ([57a60f1](https://github.com/rstreamlabs/rstream-go/commit/57a60f19ba12c5b6d3e0d4ff0fce127193afb653))

## [1.20.0](https://github.com/rstreamlabs/rstream-go/compare/v1.19.0...v1.20.0) (2026-06-02)


### Features

* add project webhook control plane APIs ([10649b5](https://github.com/rstreamlabs/rstream-go/commit/10649b544614a733ec7c5751cb0dad0feeb721e0))

## [1.19.0](https://github.com/rstreamlabs/rstream-go/compare/v1.18.0...v1.19.0) (2026-05-29)


### Features

* improve Codex MCP tunnel workflows ([95c0f12](https://github.com/rstreamlabs/rstream-go/commit/95c0f12aaf9c1ed96d795ff6ab27cd42b2746f47))

## [1.18.0](https://github.com/rstreamlabs/rstream-go/compare/v1.17.1...v1.18.0) (2026-05-26)


### Features

* add agent-native MCP and WebTTY filesystem tools ([fcc3a28](https://github.com/rstreamlabs/rstream-go/commit/fcc3a2865cba1c256df470e34ea2a4036af2ed21))

## [1.17.1](https://github.com/rstreamlabs/rstream-go/compare/v1.17.0...v1.17.1) (2026-05-23)


### Bug Fixes

* align runtime resource diagnostics ([7f9585e](https://github.com/rstreamlabs/rstream-go/commit/7f9585e6ddf72bed04b1ca592bd5418920cf2cb4))

## [1.17.0](https://github.com/rstreamlabs/rstream-go/compare/v1.16.2...v1.17.0) (2026-05-21)


### Features

* support proxy TLS configuration ([9e17de9](https://github.com/rstreamlabs/rstream-go/commit/9e17de9aae9d304cf7631b7dc6f746ac54a6a048))

## [1.16.2](https://github.com/rstreamlabs/rstream-go/compare/v1.16.1...v1.16.2) (2026-05-21)


### Bug Fixes

* make NuGet release deployment idempotent ([1b46702](https://github.com/rstreamlabs/rstream-go/commit/1b467021def675b0bf39b3441c78e5dea0dfac13))

## [1.16.1](https://github.com/rstreamlabs/rstream-go/compare/v1.16.0...v1.16.1) (2026-05-21)


### Bug Fixes

* harden native macOS release artifact installation ([3ddb43d](https://github.com/rstreamlabs/rstream-go/commit/3ddb43dec773a0e073695090825364230d53f722))

## [1.16.0](https://github.com/rstreamlabs/rstream-go/compare/v1.15.1...v1.16.0) (2026-05-21)


### Features

* add CONNECT UDP/IP and proxy transport support ([5fd2411](https://github.com/rstreamlabs/rstream-go/commit/5fd241162af90f4fdb8f645baaa27fb42534af06))
* support hardened credential storage ([6d194d2](https://github.com/rstreamlabs/rstream-go/commit/6d194d2a16e62a2511660076812f4e59e121b99e))


### Bug Fixes

* guard webtty open timer assignment ([101bde4](https://github.com/rstreamlabs/rstream-go/commit/101bde453ce99d641c42d9abf76b5e2cc9e16d22))

## [1.15.1](https://github.com/rstreamlabs/rstream-go/compare/v1.15.0...v1.15.1) (2026-05-16)


### Bug Fixes

* publish CLI release through trusted publishing ([2bca106](https://github.com/rstreamlabs/rstream-go/commit/2bca10601991d52257bbb7f41825c642b3a68587))

## [1.15.0](https://github.com/rstreamlabs/rstream-go/compare/v1.14.0...v1.15.0) (2026-05-14)


### Features

* add SCTP and SSE tunnel coverage ([a0fde77](https://github.com/rstreamlabs/rstream-go/commit/a0fde776383facece29e23109cdb8249d078bfd0))
* support mTLS agent auth and runtime permission suites ([87e3480](https://github.com/rstreamlabs/rstream-go/commit/87e348093a83d48a1e0491822677bc05cb8be8f7))


### Bug Fixes

* clarify mtls agent authentication limits ([229e26d](https://github.com/rstreamlabs/rstream-go/commit/229e26dd5ad9e82e7e6d3da3ff5d053d6afb0d30))
* harden go sdk runtime coverage ([ccef80b](https://github.com/rstreamlabs/rstream-go/commit/ccef80bb1bd259ad29bff31d4f5a1c2a962beeb7))

## [1.14.0](https://github.com/rstreamlabs/rstream-go/compare/v1.13.0...v1.14.0) (2026-05-01)


### Features

* add CLI automation diagnostics ([cb07d0e](https://github.com/rstreamlabs/rstream-go/commit/cb07d0e2f3f5efc0df6e5f9167adc3f3aca5e970))
* add OAuth device login flow ([35ed0a4](https://github.com/rstreamlabs/rstream-go/commit/35ed0a47464f73ffe77e5cd373c3bc4dd7b276a0))

## [1.13.0](https://github.com/rstreamlabs/rstream-go/compare/v1.12.0...v1.13.0) (2026-04-26)


### Features

* add stable domains and upstream TLS ([0caa069](https://github.com/rstreamlabs/rstream-go/commit/0caa06954cd2040ff0e041a374db47af5c28cfa2))

## [1.12.0](https://github.com/rstreamlabs/rstream-go/compare/v1.11.0...v1.12.0) (2026-04-23)


### Features

* add QUIC transport with datagram multiplexing and connection hardening ([af39821](https://github.com/rstreamlabs/rstream-go/commit/af39821d7c83958b15013e725ea4f0594a5fd265))
* support ECH-aware dialing and advanced DNS options ([c95d54c](https://github.com/rstreamlabs/rstream-go/commit/c95d54cf79f29ef9b345b0cfa33d521121afc523))

## [1.11.0](https://github.com/rstreamlabs/rstream-go/compare/v1.10.0...v1.11.0) (2026-04-20)


### Features

* add WebSocket H2C/H3 examples and end-to-end test suites ([8660da1](https://github.com/rstreamlabs/rstream-go/commit/8660da103d88194436219fdfff6d4237131d8dbf))

## [1.10.0](https://github.com/rstreamlabs/rstream-go/compare/v1.9.1...v1.10.0) (2026-04-01)


### Features

* add managed turn credentials support ([3ff3ff6](https://github.com/rstreamlabs/rstream-go/commit/3ff3ff6b4aa47dfe441bd9d44227b60d450c5932))

## [1.9.1](https://github.com/rstreamlabs/rstream-go/compare/v1.9.0...v1.9.1) (2026-03-27)


### Bug Fixes

* polish rstream ui terminal behavior ([3d659c1](https://github.com/rstreamlabs/rstream-go/commit/3d659c1b5a680fa7b88c2c5ca5d903f0d5a324a0))

## [1.9.0](https://github.com/rstreamlabs/rstream-go/compare/v1.8.0...v1.9.0) (2026-03-24)


### Features

* add webtty inventory and interactive ui ([2897ed9](https://github.com/rstreamlabs/rstream-go/commit/2897ed9dc2de26efb29649a9d848785743c48c5a))

## [1.8.0](https://github.com/rstreamlabs/rstream-go/compare/v1.7.2...v1.8.0) (2026-03-22)


### Features

* add filtered inventory and signaling APIs ([1ccf7b3](https://github.com/rstreamlabs/rstream-go/commit/1ccf7b3f2d462d36ae7ed50fd087d465c012f9a9))
* add netcat cli command ([06b32b3](https://github.com/rstreamlabs/rstream-go/commit/06b32b39fa5e94721e9a9c93c3d939fde5e7626b))
* support rstream transport in webtty cli ([f3e1364](https://github.com/rstreamlabs/rstream-go/commit/f3e13642e115e1dab7c8ffb63ea6ef367e97917e))


### Bug Fixes

* fix error parsing in HTTP responses ([7e221f7](https://github.com/rstreamlabs/rstream-go/commit/7e221f794d69dd7feef4b1813f4c48ce625c618a))
* restore windows netcat cross-build ([19b6621](https://github.com/rstreamlabs/rstream-go/commit/19b6621480609f97e2472d9189e32b08a1a55034))

## [1.7.2](https://github.com/rstreamlabs/rstream-go/compare/v1.7.1...v1.7.2) (2026-03-15)


### Bug Fixes

* fix webtty server on windows ([648ad22](https://github.com/rstreamlabs/rstream-go/commit/648ad22ac6d12490f9a5ecf86c0fe2423c9d6d04))

## [1.7.1](https://github.com/rstreamlabs/rstream-go/compare/v1.7.0...v1.7.1) (2026-03-11)


### Bug Fixes

* fix signature of binaries (macos) ([03a643f](https://github.com/rstreamlabs/rstream-go/commit/03a643fb161956ef40e8322b9de1debfb0dee693))

## [1.7.0](https://github.com/rstreamlabs/rstream-go/compare/v1.6.1...v1.7.0) (2026-03-10)


### Features

* add ad hoc signature on binaries (macos) ([d9f2c31](https://github.com/rstreamlabs/rstream-go/commit/d9f2c31d09cad7564bb63f17afb1c134689ce926))

## [1.6.1](https://github.com/rstreamlabs/rstream-go/compare/v1.6.0...v1.6.1) (2026-03-09)


### Bug Fixes

* fix agent name ([b44c771](https://github.com/rstreamlabs/rstream-go/commit/b44c771d4cc6fc0ebd548677411ffe908ecda3d7))

## [1.6.0](https://github.com/rstreamlabs/rstream-go/compare/v1.5.1...v1.6.0) (2026-03-05)


### Features

* add webtty client ([d7a76d3](https://github.com/rstreamlabs/rstream-go/commit/d7a76d3460eec51e5280387829bd561285bd84b9))


### Bug Fixes

* fix declarative tunnels yaml format ([9c328d6](https://github.com/rstreamlabs/rstream-go/commit/9c328d66c89e2732355c4f4c1003446a478f66cb))
* fix trusted-ips and geoip CLI args parsing ([19f0371](https://github.com/rstreamlabs/rstream-go/commit/19f03716921e7b506c2ee9c380434b59f24efcd5))

## [1.5.1](https://github.com/rstreamlabs/rstream-go/compare/v1.5.0...v1.5.1) (2026-02-18)


### Bug Fixes

* control plane api update ([d6febd8](https://github.com/rstreamlabs/rstream-go/commit/d6febd8c1347d917c6f1b39d81378225c7199e6e))

## [1.5.0](https://github.com/rstreamlabs/rstream-go/compare/v1.4.0...v1.5.0) (2026-02-15)


### Features

* browser based login workflow ([bef8933](https://github.com/rstreamlabs/rstream-go/commit/bef89336762330678459ab90855131dd1d00f200))

## [1.4.0](https://github.com/rstreamlabs/rstream-go/compare/v1.3.2...v1.4.0) (2026-02-12)


### Features

* rstrm protocol 1.4 (error codes) ([5e4a243](https://github.com/rstreamlabs/rstream-go/commit/5e4a24349045c9086df36ae12397f5daa11f9220))

## [1.3.2](https://github.com/rstreamlabs/rstream-go/compare/v1.3.1...v1.3.2) (2026-02-11)


### Bug Fixes

* fix client details / labels ([be8de3d](https://github.com/rstreamlabs/rstream-go/commit/be8de3dabe833452cb51f3bb8626074295e03eff))

## [1.3.1](https://github.com/rstreamlabs/rstream-go/compare/v1.3.0...v1.3.1) (2026-02-11)


### Bug Fixes

* fix CI (npm publish) ([9f1738f](https://github.com/rstreamlabs/rstream-go/commit/9f1738f9a7d48a482bf690a4bfa04709c39b37ed))

## [1.3.0](https://github.com/rstreamlabs/rstream-go/compare/v1.2.0...v1.3.0) (2026-02-11)


### Features

* add login, logout, project, context, run cmds ([44d427a](https://github.com/rstreamlabs/rstream-go/commit/44d427a74a311276cdc4a298f78a55c680c9247f))
* rstrm protocol 1.3 ([381a0b6](https://github.com/rstreamlabs/rstream-go/commit/381a0b6ae16410301db3acf42cfa7ad8a0c9f4fe))

## [1.2.0](https://github.com/rstreamlabs/rstream-go/compare/v1.1.0...v1.2.0) (2025-09-12)


### Features

* add events cmd ([71846d7](https://github.com/rstreamlabs/rstream-go/commit/71846d72a5612f477d325dea1bd5a07ff02e54fd))
* add forward cmd ([cfc0511](https://github.com/rstreamlabs/rstream-go/commit/cfc05116250820499c80d3eb06a0a1397eb5caab))
* add login, logout cmds ([eeb5271](https://github.com/rstreamlabs/rstream-go/commit/eeb5271a7736a6b7581b523a2f24c092d4d609b0))
* add tunnels cmd ([227b870](https://github.com/rstreamlabs/rstream-go/commit/227b870dce27633621e6546607097314b41d378a))
* update datagram interface, add dtls compatibility, remove listener ([22ec1ae](https://github.com/rstreamlabs/rstream-go/commit/22ec1ae30203c060b21a6510e3bbd3959f37841e))

## [1.1.0](https://github.com/rstreamlabs/rstream-go/compare/v1.0.0...v1.1.0) (2025-05-30)


### Features

* trigger CI ([01d330e](https://github.com/rstreamlabs/rstream-go/commit/01d330e817afa981bc7f7b7882580839715a5ba6))

## 1.0.0 (2025-05-28)


### ⚠ BREAKING CHANGES

* initial commit

### Features

* initial commit ([719319b](https://github.com/rstreamlabs/rstream-go/commit/719319b45af25c820674a38e6e587f0bf401f9c1))
