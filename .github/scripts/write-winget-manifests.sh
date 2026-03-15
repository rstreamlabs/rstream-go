#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 6 ]]; then
  echo "usage: $0 <repo-root> <version> <x64-url> <x64-sha256> <arm64-url> <arm64-sha256>" >&2
  exit 1
fi

repo_root=$1
version=$2
x64_url=$3
x64_sha256=$4
arm64_url=$5
arm64_sha256=$6

package_identifier="rstream.rstream"
manifest_version="1.10.0"
manifest_dir="${repo_root}/manifests/r/rstream/rstream/${version}"

mkdir -p "${manifest_dir}"

cat >"${manifest_dir}/${package_identifier}.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.version.${manifest_version}.schema.json

PackageIdentifier: ${package_identifier}
PackageVersion: ${version}
DefaultLocale: en-US
ManifestType: version
ManifestVersion: ${manifest_version}
EOF

cat >"${manifest_dir}/${package_identifier}.installer.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.installer.${manifest_version}.schema.json

PackageIdentifier: ${package_identifier}
PackageVersion: ${version}
InstallerType: zip
NestedInstallerType: portable
NestedInstallerFiles:
  - RelativeFilePath: bin/rstream.exe
    PortableCommandAlias: rstream
Installers:
  - Architecture: x64
    InstallerUrl: ${x64_url}
    InstallerSha256: ${x64_sha256}
  - Architecture: arm64
    InstallerUrl: ${arm64_url}
    InstallerSha256: ${arm64_sha256}
ManifestType: installer
ManifestVersion: ${manifest_version}
EOF

cat >"${manifest_dir}/${package_identifier}.locale.en-US.yaml" <<EOF
# yaml-language-server: \$schema=https://aka.ms/winget-manifest.defaultLocale.${manifest_version}.schema.json

PackageIdentifier: ${package_identifier}
PackageVersion: ${version}
PackageLocale: en-US
Publisher: rstream
PublisherUrl: https://rstream.io
PublisherSupportUrl: https://rstream.io/docs
Author: rstream
PackageName: rstream
PackageUrl: https://rstream.io
License: Apache-2.0
LicenseUrl: https://github.com/uartnet/rstream-go-v2/blob/main/LICENSE
ShortDescription: Command-line client for rstream.
Description: rstream is a developer-first platform for zero-trust networking.
Moniker: rstream
ManifestType: defaultLocale
ManifestVersion: ${manifest_version}
EOF
