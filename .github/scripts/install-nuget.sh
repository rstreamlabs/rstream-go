#!/usr/bin/env bash

set -euo pipefail

sudo apt-get update
sudo apt-get install -y ca-certificates gnupg
keyring=/usr/share/keyrings/mono-official-archive-keyring.gpg
temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
gpg --homedir "$temporary" --batch --keyserver hkp://keyserver.ubuntu.com:80 \
  --recv-keys 3FA7E0328081BFF6A14DA29AA6A19B38D3D831EF
gpg --homedir "$temporary" --batch --export 3FA7E0328081BFF6A14DA29AA6A19B38D3D831EF \
  > "$temporary/mono.gpg"
sudo install -m 0644 "$temporary/mono.gpg" "$keyring"
printf '%s\n' "deb [signed-by=$keyring] https://download.mono-project.com/repo/ubuntu stable-focal main" \
  | sudo tee /etc/apt/sources.list.d/mono-official-stable.list >/dev/null
sudo apt-get update
sudo apt-get install -y nuget

# Both commands are required by candidate creation and immutable promotion.
nuget help pack > "$temporary/pack-help"
nuget help push > "$temporary/push-help"
grep -q OutputDirectory "$temporary/pack-help"
grep -q SkipDuplicate "$temporary/push-help"
