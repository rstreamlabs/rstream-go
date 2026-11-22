# Embedded file browser

The CLI embeds a generated browser bundle; normal Go builds require no JavaScript toolchain. The HTML, gzip and integrity manifest are committed together. The manifest records the contract version, content hash and inline CSP hashes; `fileserver/ui` verifies them in tests.

Changes to the shared browser client need a regenerated bundle rather than edits to compressed assets. Coordinate that update with the maintainers. The public [`@rstreamlabs/filesystem`](https://github.com/rstreamlabs/rstream-js/tree/main/packages/filesystem) package documents the filesystem API used by browser integrations.
