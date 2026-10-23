#!/usr/bin/env bash

set -euo pipefail

temporary=$(mktemp -d)
trap 'rm -rf "$temporary"' EXIT
mkdir "$temporary/packages" "$temporary/feed"
printf 'rstream release packaging fixture\n' > "$temporary/payload.txt"
cat > "$temporary/fixture.nuspec" <<'XML'
<?xml version="1.0"?>
<package>
  <metadata>
    <id>rstream-packaging-fixture</id>
    <version>1.0.0</version>
    <authors>rstream</authors>
    <description>Local release packaging verification.</description>
  </metadata>
  <files><file src="payload.txt" target="tools/payload.txt" /></files>
</package>
XML
nuget pack "$temporary/fixture.nuspec" -OutputDirectory "$temporary/packages"
package="$temporary/packages/rstream-packaging-fixture.1.0.0.nupkg"
nuget push "$package" -Source "$temporary/feed" -NonInteractive -SkipDuplicate
python3 - "$temporary" <<'PY'
import sys
from pathlib import Path
from zipfile import ZipFile

root = Path(sys.argv[1])
packages = list((root / "feed").rglob("*.nupkg"))
assert len(packages) == 1, packages
original = root / "packages/rstream-packaging-fixture.1.0.0.nupkg"
assert packages[0].read_bytes() == original.read_bytes()
with ZipFile(packages[0]) as archive:
    assert archive.read("tools/payload.txt") == (root / "payload.txt").read_bytes()
print("NuGet pack and local promotion preserve the exact package and payload bytes")
PY
