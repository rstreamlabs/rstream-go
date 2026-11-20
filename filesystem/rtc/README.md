# WebRTC filesystem interoperability

The WebRTC backend carries read-only filesystem responses on one ordered data channel per request. Signaling uses authenticated HTTP with the same access policy as the export. The Go and browser implementations agree on the version, peer lease, renewal and restart, chunk credit, completion marker and content length. Changing those fields requires coordinating the Go and JavaScript clients.

The source owns the protocol constants and limits. Preserve bounded pending peers and buffered writes. Authorization loss, lease expiry and cancellation must close the peer and unblock the reader. A WebRTC write request must fail without retrying through WebDAV.

The integration fixture in [testdata/server](testdata/server) starts a real Go endpoint for the JavaScript filesystem tests. Use a browser or a native WebRTC provider to verify the corresponding runtime as well as the source tests.
