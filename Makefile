#!make

-include .env.makefile
ifeq ($(wildcard .env.makefile),.env.makefile)
export $(shell sed 's/=.*//' .env.makefile)
endif

.DEFAULT_GOAL := all

# Binaries
BINARIES := $(shell find cmd -mindepth 1 -maxdepth 1 -type d -exec basename {} \;)

# Examples
EXAMPLES := $(shell find examples -mindepth 2 -maxdepth 2 -name '*.go' -print 2>/dev/null | awk -F/ '{print $$(NF-1)}' | sort -u)

# Detect git tag and branch
GIT_TAG := $(shell git describe --tags --exact-match 2>/dev/null | sed 's/^v//')
GIT_BRANCH := $(shell git symbolic-ref --quiet --short HEAD 2>/dev/null || printf dev)
CHANNEL ?= $(if $(GIT_TAG),stable,dev)
VERSION ?= $(if $(GIT_TAG),$(GIT_TAG),$(GIT_BRANCH))

# Output directory
OUT_DIR := out

# ARM 32 bits architectures
ARM_ARCHS := v6 v7

# ARM 32 bits floating point
ARM_FLOATING_POINTS := softfloat hardfloat

# ARM 32 bits floating point
X86_FLOATING_POINTS := 387 sse2

# ARM 64 bits architectures
X86_64_VARIANTS := v1 v2 v3 v4

# Go Module
GO_MODULE := $(shell go list -m)

# Path to goimports
GOIMPORTS ?= $(shell go env GOPATH)/bin/goimports

# All platforms
ALL_PLATFORMS := $(foreach dist,$(shell go tool dist list),\
$(eval os=$(firstword $(subst /, ,$(dist))))\
$(eval arch=$(lastword $(subst /, ,$(dist))))\
$(eval platform=$(if $(filter darwin,$(os)),$(subst darwin,macos,$(dist)),$(dist)))\
$(if $(filter arm,$(arch)),$(foreach arch,$(ARM_ARCHS),$(foreach fp,$(ARM_FLOATING_POINTS),$(platform)$(arch)$(if $(filter hardfloat,$(fp)),hf,))),$(if $(filter amd64,$(arch)),$(foreach x86_64_variant,$(X86_64_VARIANTS),$(subst x86_64_v1,x86_64,$(subst amd64,x86_64_$(x86_64_variant),$(platform)))),$(if $(filter 386,$(arch)),$(foreach fp,$(X86_FLOATING_POINTS),$(if $(filter 387,$(fp)),$(subst 386,x86_i386,$(platform)),$(subst 386,x86_i686,$(platform)))),$(platform)))))

# Current platform
CURRENT_OS := $(subst darwin,macos,$(shell go env GOOS))
CURRENT_ARCH := $(subst amd64,x86_64_v2,$(shell go env GOARCH))
CURRENT_PLATFORM := $(CURRENT_OS)/$(CURRENT_ARCH)

# Supported operating systems
OS_WHITELIST := linux macos netbsd openbsd freebsd windows

# Supported architectures
ARCH_WHITELIST := x86_i386 x86_i686 x86_64 x86_64_v2 x86_64_v3 x86_64_v4 armv6 armv6hf armv7 armv7hf arm64 mips mipsle mips64 mips64le ppc64 ppc64le riscv64

# Supported platforms
PLATFORMS := $(foreach platform,$(ALL_PLATFORMS),\
$(eval os=$(firstword $(subst /, ,$(platform))))\
$(eval arch=$(lastword $(subst /, ,$(platform))))\
$(if $(filter $(os),$(OS_WHITELIST)),$(if $(filter $(arch),$(ARCH_WHITELIST)),$(platform))))

# Linux platforms
LINUX_PLATFORMS := $(filter linux/%,$(PLATFORMS))

# MacOS platforms
MACOS_CODESIGN_MODE ?= certificate
MACOS_PLATFORMS := $(filter macos/%,$(PLATFORMS))

# Windows platforms
WINDOWS_PLATFORMS := $(filter windows/%,$(PLATFORMS))

# Debian ports
DEBIAN_ARCHS := x86_i686 x86_64 armv6 armv7hf arm64 mips mipsle mips64le ppc64 ppc64le riscv64

# Debian platforms
DEBIAN_PLATFORMS := $(foreach platform,$(LINUX_PLATFORMS),\
$(eval arch=$(lastword $(subst /, ,$(platform))))\
$(if $(filter $(arch),$(DEBIAN_ARCHS)),$(platform)))

# Nuget platforms
NUGET_PLATFORMS := $(filter $(WINDOWS_PLATFORMS),windows/x86_i686 windows/x86_64)

# Debian maintainer
MAINTAINER := support@rstream.io

# Debian description
DESCRIPTION := Go SDK for rstream - serverless networking

# rstream repository
RSTREAM_URL ?= https://rstream.io

# rstream storage type
RSTREAM_STORAGE_TYPE ?= s3

# aptly repository
APTLY_URL ?= https://aptly.rstream.io

# Nuget source
NUGET_SOURCE ?= https://nexus.rstream.io/repository/windows/

# Docker repository
DOCKER_REPO ?= rstream

# List of docker platforms
DOCKER_PLATFORMS := $(if $(filter $(CHANNEL),stable),$(filter $(PLATFORMS),linux/arm64 linux/x86_64 linux/x86_64_v2 linux/ppc64le linux/x86_i686 linux/armv7hf linux/armv6hf),linux/$(CURRENT_ARCH))

comma:= ,

empty:=

space:= $(empty) $(empty)

switch = $(word $(findstring $1,$2),$(subst ,, $2))

define debian_arch
$(strip \
$(if $(filter $1,arm64),arm64, \
$(if $(filter $1,armv6),armel, \
$(if $(filter $1,armv7hf),armhf, \
$(if $(filter $1,mips),mips, \
$(if $(filter $1,mips64le),mips64el, \
$(if $(filter $1,mipsle),mipsel, \
$(if $(filter $1,ppc64),ppc64, \
$(if $(filter $1,ppc64le),ppc64el, \
$(if $(filter $1,riscv64),riscv64, \
$(if $(filter $1,x86_64),amd64, \
$(if $(filter $1,x86_i686),i386, \
$1))))))))))))
endef

define sources_proto
$(shell find . -name '*.proto' ! -path './cmd/*' ! -path './examples/*')
endef

define sources_pb_go
$(foreach proto,$(call sources_proto),$(subst .proto,.pb.go,$(proto)))
endef

define sources
$(shell find . -name '*.go' ! -path './cmd/*' ! -path './examples/*' ! -path '*.pb.go' -o -path "./$1/$2/*") $(call sources_pb_go)
endef

define base_dir_cmd
$(OUT_DIR)/cmd/$1/$(CHANNEL)/$(VERSION)
endef

define base_dir_examples
$(OUT_DIR)/examples
endef

define output_dir
$(call base_dir_cmd,$1)/$2/$3
endef

define release_dir
$(call output_dir,$1,$2,$3)/release
endef

define binary_path
$(call release_dir,$1,$2,$3)/bin/$1$(if $(filter $2,windows),.exe)
endef

define archive_basename
$1-$(VERSION)-$2-$3
endef

define pkg_path
$(call output_dir,$1,$2,$3)/$(call archive_basename,$1,$2,$3)$(if $(filter windows,$2),.zip,.tar.gz)
endef

define deb_path
$(call output_dir,$1,$2,$3)/$1-$(VERSION)-$2-$(call debian_arch,$3).deb
endef

define nupkg_path
$(call base_dir_cmd,$1)/windows/$1.$(VERSION).nupkg
endef

define cmd_tags
$(strip $(shell if [ -f cmd/$1/tags ]; then awk '!/^[[:space:]]*(#|$$)/{print}' cmd/$1/tags | paste -sd, -; fi))
endef

$(foreach bin,$(BINARIES),$(eval CMD_TAGS_$(bin):=$(call cmd_tags,$(bin))))

define go_build_tags
$(if $(CMD_TAGS_$1),-tags=$(CMD_TAGS_$1))
endef

define build
set -e ;\
echo "Building $1/$2 for $3/$4" ;\
$(eval GOARCH=$(if $(findstring armv,$(word 1,$(subst /, ,$4))),arm,$(if $(findstring x86_i,$4),386,$(if $(findstring x86_64,$4),amd64,$(word 1,$(subst /, ,$4)))))) \
$(eval GOAMD64=$(if $(findstring x86_64,$4),$(if $(findstring _v,$4),$(lastword $(subst _, ,$4)),v1),)) \
CGO_ENABLED=0 GOPRIVATE=github.com/rstreamlabs GOOS=$(subst macos,darwin,$3) GOARCH=$(GOARCH) $(if $(filter $(GOARCH),arm),GOARM=$(word 1,$(subst armv, ,$(word 1,$(subst hf, ,$4))))$(shell echo ,)$(if $(findstring hf,$4),hardfloat,softfloat),) $(if $(filter amd64,$(GOARCH)),GOAMD64=$(GOAMD64),) $(if $(findstring x86_i386,$4),GO386=softfloat,) go build -buildvcs=false -v $(if $(filter cmd,$1),$(call go_build_tags,$2),) -ldflags="-X '$(GO_MODULE).Agent=$(patsubst %-go,%,$(notdir $(shell printf '%s\n' "$(GO_MODULE)" | sed -E 's|/v[0-9]+$$||')))' -X '$(GO_MODULE).Channel=$(CHANNEL)' -X '$(GO_MODULE).Version=$(VERSION)' -X '$(GO_MODULE).OS=$3' -X '$(GO_MODULE).Arch=$4'" -o $$@ ./$1/$2
endef

define build_pkg
set -e ;\
echo "Creating $$@" ;\
mkdir -p $(call output_dir,$1,$2,$3) ;\
releasedir="$(call release_dir,$1,$2,$3)"; \
case "$$@" in \
*.zip) \
zippath="$$$$(realpath $$$$releasedir)"; \
dstdir="$$$$(dirname $$$$zippath)"; \
(cd "$$$$releasedir"; zip -r "$$$$(dirname $$$$zippath)/$$$$(basename $$@)" .) ;; \
*.tar.gz|*.tgz) \
tar -czf $$@ -C "$$$$releasedir" . ;; \
*) \
echo "Error: Unknown file format for $$@" >&2 ;\
exit 1 ;; \
esac
endef

define deploy_pkg
set -e ;\
$(eval FILENAME=$(shell basename $(call pkg_path,$1,$2,$3))) \
CHECKSUM=$$$$(shasum -a 256 $(call pkg_path,$1,$2,$3) | awk '{print $$$$1}') ;\
echo "Deploying $(call pkg_path,$1,$2,$3)" ;\
RESPONSE=$$$$(curl \
--fail \
-H "Authorization: Bearer $(RSTREAM_TOKEN)" \
-i \
-s \
-S \
-X PUT \
"$(RSTREAM_URL)/api/packages?name=$1&version=$(VERSION)&channel=$(CHANNEL)&os=$2&arch=$3&filename=${FILENAME}&checksum=$$$$CHECKSUM&storageType=$(RSTREAM_STORAGE_TYPE)") ;\
PACKAGE_ID=$$$$(echo "$$$$RESPONSE" | grep 'x-package-id' | cut -d ' ' -f2 | tr -d '\r') ;\
SIGNED_URL=$$$$(echo "$$$$RESPONSE" | grep 'location:' | cut -d ' ' -f2 | tr -d '\r') ;\
curl \
--progress-bar \
--upload-file "$(call pkg_path,$1,$2,$3)" \
-fail \
-H "Content-Type: application/octet-stream" \
-X PUT \
"$$$$SIGNED_URL" \
| cat ;\
printf "name:$1\nid:$$$$PACKAGE_ID\nversion:$(VERSION)\nchannel:$(CHANNEL)\nos:$2\narch:$3\nfilename:${FILENAME}\nchecksum:$$$$CHECKSUM\n" > $(call pkg_path,$1,$2,$3).info
endef

define build_deb
set -e ;\
$(eval DEBIAN_ARCH=$(call debian_arch,$3)) \
TMP_DIR=$$$$(mktemp -d) ;\
echo "Creating $$@" ;\
mkdir -p $$$$TMP_DIR/usr/ $$$$TMP_DIR/DEBIAN ;\
rsync -a $(call release_dir,$1,$2,$3)/ $$$$TMP_DIR/usr/ ;\
echo "Package: $1\nVersion: $(VERSION)\nArchitecture: ${DEBIAN_ARCH}\nMaintainer: $(MAINTAINER)\nDescription: $(DESCRIPTION)" > $$$$TMP_DIR/DEBIAN/control ;\
dpkg-deb --build --root-owner-group -Z gzip $$$$TMP_DIR $$@ ;\
rm -rf $$$$TMP_DIR
endef

define deploy_deb
set -e ;\
echo "Uploading files ..." ;\
curl -s --fail -w "\n" --digest -u "$(APTLY_DIGEST)" -X POST $(foreach platform,$(DEBIAN_PLATFORMS),-F file=@$(call deb_path,$1,$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform)))) $(APTLY_URL)/api/files/$1-$(CHANNEL)-$(VERSION) ;\
echo "Adding files to debian repository ..." ;\
curl -s --fail -w "\n" --digest -u "$(APTLY_DIGEST)" -X POST $(APTLY_URL)/api/repos/linux-$(CHANNEL)/file/$1-$(CHANNEL)-$(VERSION)?forceReplace=1 ;\
echo "Publishing debian repository ..." ;\
curl -s --fail -w "\n" --digest -u "$(APTLY_DIGEST)" -H "Content-Type: application/json" -X PUT --data '{"ForceOverwrite": true}' $(APTLY_URL)/api/publish/filesystem:public-repo:linux/linux ;\
echo "Cleaning up ..." ;\
curl -s --fail -w "\n" --digest -u "$(APTLY_DIGEST)" -X DELETE $(APTLY_URL)/api/files/$1-$(CHANNEL)-$(VERSION)
endef

define build_docker
echo $(DOCKER_PLATFORMS) ;\
$(eval IMAGE=$(shell echo $(DOCKER_REPO)/$1)) \
$(if $(findstring linux/x86_i686,$(DOCKER_PLATFORMS)),\
  mkdir -p $(call base_dir_cmd,$1)/linux/386/release/bin ;\
  ln -sf ../../../x86_i686/release/bin/$1 $(call base_dir_cmd,$1)/linux/386/release/bin/$1 ;\
)\
$(if $(findstring linux/x86_64,$(DOCKER_PLATFORMS)),\
  mkdir -p $(call base_dir_cmd,$1)/linux/amd64/release/bin ;\
  ln -sf ../../../x86_64/release/bin/$1 $(call base_dir_cmd,$1)/linux/amd64/release/bin/$1 ;\
)\
$(if $(findstring linux/x86_64_v2,$(DOCKER_PLATFORMS)),\
  mkdir -p $(call base_dir_cmd,$1)/linux/amd64/v2/release/bin ;\
  ln -sf ../../../../x86_64_v2/release/bin/$1 $(call base_dir_cmd,$1)/linux/amd64/v2/release/bin/$1 ;\
)\
$(if $(findstring linux/armv6hf,$(DOCKER_PLATFORMS)),\
  mkdir -p $(call base_dir_cmd,$1)/linux/arm/v6/release/bin ;\
  ln -sf ../../../../armv6hf/release/bin/$1 $(call base_dir_cmd,$1)/linux/arm/v6/release/bin/$1 ;\
)\
$(if $(findstring linux/armv7hf,$(DOCKER_PLATFORMS)),\
  mkdir -p $(call base_dir_cmd,$1)/linux/arm/v7/release/bin ;\
  ln -sf ../../../../armv7hf/release/bin/$1 $(call base_dir_cmd,$1)/linux/arm/v7/release/bin/$1 ;\
)\
docker buildx build \
--build-arg BINARY=$1 \
--file Dockerfile \
--platform $(subst hf,,$(subst v6,/v6,$(subst v7,/v7,$(subst x86_64_v,x86_64/v,$(subst x86_i686,386,$(subst $(space),$(comma),$(DOCKER_PLATFORMS))))))) \
--pull \
--tag ${IMAGE}:$(VERSION)$(if $(filter-out stable,$(CHANNEL)),-$(CHANNEL)) \
$(if $(filter stable,$(CHANNEL)),--tag ${IMAGE}:latest) \
$(if $(filter stable,$(CHANNEL)),--output=type=registry,--output=type=docker) \
$(call base_dir_cmd,$1)
endef

define macos_apple_codesign
set -e ;\
case "$(MACOS_CODESIGN_MODE)" in \
adhoc|certificate) ;; \
*) \
echo "Error: MACOS_CODESIGN_MODE must be one of: adhoc, certificate" >&2 ;\
exit 1 ;; \
esac ;\
if [ "$(MACOS_CODESIGN_MODE)" = "adhoc" ]; then \
echo "Signing $(call binary_path,$1,$2,$3) ..." ;\
codesign \
-f \
-i "io.rstream" \
-s "-" \
-v \
$(call binary_path,$1,$2,$3) ;\
else \
[ -n "$(MACOS_CERTIFICATE_NAME)" ] || { echo "Error: MACOS_CERTIFICATE_NAME is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
[ -n "$(MACOS_NOTARIZATION_APPLE_ID)" ] || { echo "Error: MACOS_NOTARIZATION_APPLE_ID is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
[ -n "$(MACOS_NOTARIZATION_TEAM_ID)" ] || { echo "Error: MACOS_NOTARIZATION_TEAM_ID is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
[ -n "$(MACOS_NOTARIZATION_PWD)" ] || { echo "Error: MACOS_NOTARIZATION_PWD is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
TMP_FILE=$$$$(mktemp).zip ;\
echo "Signing $(call binary_path,$1,$2,$3) ..." ;\
codesign \
--options=runtime \
--timestamp \
-f \
-i "io.rstream" \
-s "$(MACOS_CERTIFICATE_NAME)" \
-v \
$(call binary_path,$1,$2,$3) ;\
echo "Notarizing $(call binary_path,$1,$2,$3) ..." ;\
ditto -c -k --keepParent $(call binary_path,$1,$2,$3) $$$$TMP_FILE ;\
xcrun notarytool submit --apple-id "$(MACOS_NOTARIZATION_APPLE_ID)" --team-id "$(MACOS_NOTARIZATION_TEAM_ID)" --password "$(MACOS_NOTARIZATION_PWD)" --wait $$$$TMP_FILE ;\
rm -rf $$$$TMP_FILE ;\
fi
endef

define macos_rcodesign
set -e ;\
case "$(MACOS_CODESIGN_MODE)" in \
adhoc|certificate) ;; \
*) \
echo "Error: MACOS_CODESIGN_MODE must be one of: adhoc, certificate" >&2 ;\
exit 1 ;; \
esac ;\
if [ "$(MACOS_CODESIGN_MODE)" = "adhoc" ]; then \
echo "Signing $(call binary_path,$1,$2,$3) ..." ;\
rcodesign sign $(call binary_path,$1,$2,$3) ;\
else \
[ -n "$(MACOS_CERTIFICATE)" ] || { echo "Error: MACOS_CERTIFICATE is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
[ -n "$(MACOS_CERTIFICATE_PWD)" ] || { echo "Error: MACOS_CERTIFICATE_PWD is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
[ -n "$(MACOS_APP_STORE_API_KEY)" ] || { echo "Error: MACOS_APP_STORE_API_KEY is required when MACOS_CODESIGN_MODE=certificate" >&2 ; exit 1 ; } ;\
BINARY_ARCHIVE=$$$$(mktemp).zip ;\
MACOS_CERTIFICATE_FILE=$$$$(mktemp).p12 ;\
MACOS_APP_STORE_API_KEY_FILE=$$$$(mktemp).json ;\
echo "Signing $(call binary_path,$1,$2,$3) ..." ;\
echo ${MACOS_CERTIFICATE} | base64 --decode > $$$$MACOS_CERTIFICATE_FILE ;\
rcodesign sign --p12-file "$$$$MACOS_CERTIFICATE_FILE" --p12-password "$(MACOS_CERTIFICATE_PWD)" --code-signature-flags runtime $(call binary_path,$1,$2,$3) ;\
echo "Notarizing $(call binary_path,$1,$2,$3) ..." ;\
zip -j -q $$$$BINARY_ARCHIVE $(call binary_path,$1,$2,$3) ;\
echo ${MACOS_APP_STORE_API_KEY} | base64 --decode > $$$$MACOS_APP_STORE_API_KEY_FILE ;\
rcodesign notary-submit -v --api-key-file $$$$MACOS_APP_STORE_API_KEY_FILE --wait $$$$BINARY_ARCHIVE ;\
rm -rf $$$$BINARY_ARCHIVE ;\
rm -rf $$$$MACOS_CERTIFICATE_FILE ;\
rm -rf $$$$MACOS_APP_STORE_API_KEY_FILE ;\
fi
endef

define build_nupkg
set -e ;\
TMP_DIR=$$$$(mktemp -d) ;\
mkdir -p $$$$TMP_DIR $$$$TMP_DIR/tools/x86 $$$$TMP_DIR/tools/x64 ;\
cp README.md $$$$TMP_DIR/ ;\
$(foreach platform,$(NUGET_PLATFORMS),\
cp $(call binary_path,$1,$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))) $$$$TMP_DIR/tools/$(subst x86_i686,x86,$(subst x86_64,x64,$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))/$1.exe ;\
) \
echo '<?xml version="1.0" encoding="utf-8"?><package xmlns="http://schemas.microsoft.com/packaging/2010/07/nuspec.xsd"><metadata><id>$1</id><version>$(VERSION)</version><title>$1</title><authors>$(MAINTAINER)</authors><description>$(DESCRIPTION)</description><readme>docs/README.md</readme></metadata><files><file src="README.md" target="docs/" /><file src="tools/**" target="tools" /></files></package>' > $$$$TMP_DIR/$1.nuspec ;\
nuget pack $$$$TMP_DIR/$1.nuspec -OutputDirectory $(call base_dir_cmd,$1)/windows ;\
rm -rf $$$$TMP_DIR
endef

define deploy_nupkg
set -e ;\
echo "Deploying $(call nupkg_path,$1)" ;\
nuget push "$(call nupkg_path,$1)" "$(NUGET_API_KEY)" -Source "$(NUGET_SOURCE)" -NonInteractive -SkipDuplicate -Verbosity detailed
endef

.PHONY: all

all: $(BINARIES)

.PHONY: examples

examples: $(EXAMPLES)

# Integration test binaries — one binary per test/<suite>/<role>/ directory.
# New test suites are picked up automatically; no Makefile change required.
TEST_ROLES := $(shell find test -mindepth 2 -maxdepth 2 -name '*.go' -print 2>/dev/null \
	| awk -F/ '{print $$(NF-2)"/"$$(NF-1)}' | sort -u)
TEST_OUT := $(OUT_DIR)/test

.PHONY: test-bins

test-bins: $(foreach r,$(TEST_ROLES),$(TEST_OUT)/$(r))

define template_test_bin
$(TEST_OUT)/$1: $$(shell find test/$1 -name '*.go' 2>/dev/null)
	@set -e; echo "Building test/$1 for $(CURRENT_OS)/$(CURRENT_ARCH)"; \
	CGO_ENABLED=0 GOPRIVATE=github.com/rstreamlabs \
	GOOS=$(subst macos,darwin,$(CURRENT_OS)) GOARCH=$(CURRENT_ARCH) \
	go build -buildvcs=false \
	  -ldflags="-X '$(GO_MODULE).Agent=$(patsubst %-go,%,$(notdir $(shell printf '%s\n' '$(GO_MODULE)' | sed -E 's|/v[0-9]+$$||')))' -X '$(GO_MODULE).Channel=$(CHANNEL)' -X '$(GO_MODULE).Version=$(VERSION)' -X '$(GO_MODULE).OS=$(CURRENT_OS)' -X '$(GO_MODULE).Arch=$(CURRENT_ARCH)'" \
	  -o $(TEST_OUT)/$1 ./test/$1
endef

$(foreach r,$(TEST_ROLES),$(eval $(call template_test_bin,$(r))))

.PHONY: clean

clean:
	@rm -rf $(OUT_DIR)

.PHONY: tests

tests:
	@echo "==> Running tests..."
	go test -v ./...

$(GOIMPORTS):
	@go install golang.org/x/tools/cmd/goimports@latest

.PHONY: format

format: $(GOIMPORTS)
	@find . -type f -name '*.go' ! -name '*.pb.go' -print0 | xargs -0 $(GOIMPORTS) -w

.SECONDARY: $(call sources_proto)

$(call sources_pb_go): $(call sources_proto)
	@echo "==> Generating protobuf code..."
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go generate ./...

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)))

$(foreach bin,$(BINARIES),$(eval $(bin): $(call binary_path,$(bin),$(CURRENT_OS),$(CURRENT_ARCH))))

define template_target_build_cmd
$(call binary_path,$1,$2,$3): $(call sources,cmd,$1)
	@$(call build,cmd,$1,$2,$3)
endef

$(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(eval $(call template_target_build_cmd,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-cross))

$(foreach bin,$(BINARIES),$(eval $(bin)-cross: $(foreach platform,$(PLATFORMS),$(call binary_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

.PHONY: cross

cross: $(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(call binary_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform)))))

$(foreach platform,$(PLATFORMS),$(eval .PHONY: $(word 1,$(subst /, ,$(platform)))-cross))

$(foreach platform,$(PLATFORMS),$(eval $(word 1,$(subst /, ,$(platform)))-cross: $(foreach bin,$(BINARIES),$(call binary_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

define template_target_pkg
$(call pkg_path,$1,$2,$3): $(call binary_path,$1,$2,$3)
	@$(call build_pkg,$1,$2,$3)
endef

$(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(eval $(call template_target_pkg,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

.PHONY: pkg

pkg: $(foreach bin,$(BINARIES),$(call pkg_path,$(bin),$(CURRENT_OS),$(CURRENT_ARCH)))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-pkg))

$(foreach bin,$(BINARIES),$(eval $(bin)-pkg: $(call pkg_path,$(bin),$(CURRENT_OS),$(CURRENT_ARCH))))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-pkg-cross))

$(foreach bin,$(BINARIES),$(eval $(bin)-pkg-cross: $(foreach platform,$(PLATFORMS),$(call pkg_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

.PHONY: pkg-cross

pkg-cross: $(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(call pkg_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform)))))

define template_target_deploy_pkg
$1-$2-$3-deploy-pkg: $(call pkg_path,$1,$2,$3)
	@$(call deploy_pkg,$1,$2,$3)
endef

$(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(eval .PHONY: $(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-deploy-pkg)))

$(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(eval $(call template_target_deploy_pkg,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

.PHONY: deploy-pkg

deploy-pkg: $(foreach bin,$(BINARIES),$(bin)-$(CURRENT_OS)-$(CURRENT_ARCH)-deploy-pkg)

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-deploy-pkg))

$(foreach bin,$(BINARIES),$(eval $(bin)-deploy-pkg: $(bin)-$(CURRENT_OS)-$(CURRENT_ARCH)-deploy-pkg))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-deploy-pkg-cross))

$(foreach bin,$(BINARIES),$(eval $(bin)-deploy-pkg-cross: $(foreach platform,$(PLATFORMS),$(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-deploy-pkg)))

.PHONY: deploy-pkg-cross

deploy-pkg-cross: $(foreach bin,$(BINARIES),$(foreach platform,$(PLATFORMS),$(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-deploy-pkg))

define template_target_deb
$(call deb_path,$1,$2,$3): $(call binary_path,$1,$2,$3)
	@$(call build_deb,$1,$2,$3)
endef

$(foreach bin,$(BINARIES),$(foreach platform,$(DEBIAN_PLATFORMS),$(eval $(call template_target_deb,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

.PHONY: deb

deb: $(foreach bin,$(BINARIES),$(call deb_path,$(bin),$(CURRENT_OS),$(CURRENT_ARCH)))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-deb))

$(foreach bin,$(BINARIES),$(eval $(bin)-deb: $(call deb_path,$(bin),$(CURRENT_OS),$(CURRENT_ARCH))))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-deb-cross))

$(foreach bin,$(BINARIES),$(eval $(bin)-deb-cross: $(foreach platform,$(DEBIAN_PLATFORMS),$(call deb_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

.PHONY: deb-cross

deb-cross: $(foreach bin,$(BINARIES),$(foreach platform,$(DEBIAN_PLATFORMS),$(call deb_path,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform)))))

.PHONY: deploy-deb

deploy-deb: $(foreach bin,$(BINARIES),$(bin)-deploy-deb)

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-deploy-deb))

define template_target_deploy_deb
$1-deploy-deb: $(foreach platform,$(DEBIAN_PLATFORMS),$(call deb_path,$1,$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))
	@$(call deploy_deb,$1)
endef

$(foreach bin,$(BINARIES),$(eval $(call template_target_deploy_deb,$(bin))))

.PHONY: docker

docker: $(foreach bin,$(BINARIES),$(bin)-docker)

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-docker))

define template_target_docker
$1-docker: $(foreach platform,$(DOCKER_PLATFORMS),$(call binary_path,$1,$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))
	@$(call build_docker,$1)
endef

$(foreach bin,$(BINARIES),$(eval $(call template_target_docker,$(bin))))

define template_target_macos_apple_codesign
$1-$2-$3-macos-apple-codesign: $(call binary_path,$1,$2,$3)
	@$(call macos_apple_codesign,$1,$2,$3)
endef

$(foreach bin,$(BINARIES),$(foreach platform,$(MACOS_PLATFORMS),$(eval .PHONY: $(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-macos-apple-codesign)))

$(foreach bin,$(BINARIES),$(foreach platform,$(MACOS_PLATFORMS),$(eval $(call template_target_macos_apple_codesign,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-macos-apple-codesign))

$(foreach bin,$(BINARIES),$(eval $(bin)-macos-apple-codesign: $(foreach platform,$(MACOS_PLATFORMS),$(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-macos-apple-codesign)))

.PHONY: macos-apple-codesign

macos-apple-codesign: $(foreach bin,$(BINARIES),$(bin)-macos-apple-codesign)

.PHONY: macos-apple-codesign-cross

macos-apple-codesign-cross: $(foreach bin,$(BINARIES),$(foreach platform,$(MACOS_PLATFORMS),$(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-macos-apple-codesign))

define template_target_macos_rcodesign
$1-$2-$3-macos-rcodesign: $(call binary_path,$1,$2,$3)
	@$(call macos_rcodesign,$1,$2,$3)
endef

$(foreach bin,$(BINARIES),$(foreach platform,$(MACOS_PLATFORMS),$(eval .PHONY: $(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-macos-rcodesign)))

$(foreach bin,$(BINARIES),$(foreach platform,$(MACOS_PLATFORMS),$(eval $(call template_target_macos_rcodesign,$(bin),$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))))

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-macos-rcodesign))

$(foreach bin,$(BINARIES),$(eval $(bin)-macos-rcodesign: $(foreach platform,$(MACOS_PLATFORMS),$(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-macos-rcodesign)))

.PHONY: macos-rcodesign

macos-rcodesign: $(foreach bin,$(BINARIES),$(bin)-macos-rcodesign)

.PHONY: macos-rcodesign-cross

macos-rcodesign-cross: $(foreach bin,$(BINARIES),$(foreach platform,$(MACOS_PLATFORMS),$(bin)-$(word 1,$(subst /, ,$(platform)))-$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))-macos-rcodesign))

.PHONY: nupkg

nupkg: $(foreach bin,$(BINARIES),$(bin)-nupkg)

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-nupkg))

$(foreach bin,$(BINARIES),$(eval $(bin)-nupkg: $(call nupkg_path,$(bin))))

define template_target_nupkg
$(call nupkg_path,$1): $(foreach platform,$(NUGET_PLATFORMS),$(call binary_path,$1,$(word 1,$(subst /, ,$(platform))),$(subst $(firstword $(subst /, ,$(platform)))/,,$(platform))))
	@$(call build_nupkg,$1)
endef

$(foreach bin,$(BINARIES),$(eval $(call template_target_nupkg,$(bin))))

.PHONY: deploy-nupkg

deploy-nupkg: $(foreach bin,$(BINARIES),$(bin)-deploy-nupkg)

$(foreach bin,$(BINARIES),$(eval .PHONY: $(bin)-deploy-nupkg))

define template_target_deploy_nupkg
$1-deploy-nupkg: $1-nupkg
	@$(call deploy_nupkg,$1)
endef

$(foreach bin,$(BINARIES),$(eval $(call template_target_deploy_nupkg,$(bin))))

$(foreach bin,$(EXAMPLES),$(eval .PHONY: $(bin)))

$(foreach bin,$(EXAMPLES),$(eval $(bin): $(call base_dir_examples)/$(bin)))

define template_target_build_examples
$(call base_dir_examples)/$1: $(call sources,examples,$1)
	@$(call build,examples,$1,$(CURRENT_OS),$(CURRENT_ARCH))
endef

$(foreach bin,$(EXAMPLES),$(eval $(call template_target_build_examples,$(bin))))
