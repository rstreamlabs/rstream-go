# Add uploads to the embedded file browser

Read-write file shares support WebDAV uploads, but the embedded browser does not expose upload controls. Add progress, cancellation and partial-upload handling, respecting the server’s write capability and upload limit. Keep read-only and WebRTC shares download-only.
