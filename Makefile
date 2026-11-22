# Syncme build entry point.
#
#   make            Linux binaries for this machine -> dist/
#   make linux      Linux binaries for x86_64 and arm64 -> dist/
#   make android    Android APK -> dist/syncme.apk (downloads the Android SDK if needed)
#   make all        both of the above
#   make arch       Arch Linux package (run on Arch) -> dist/*.pkg.tar.zst
#   make install    install syncme for the current user (~/.local/bin) and start it
#   make test       unit + end-to-end tests

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS  = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
PREFIX  ?= $(HOME)/.local

.PHONY: native linux android all arch install test clean

native:
	@mkdir -p dist
	CGO_ENABLED=0 go build $(GOFLAGS) -o dist/syncme ./cmd/syncme
	CGO_ENABLED=0 go build $(GOFLAGS) -o dist/syncme-relay ./cmd/syncme-relay
	@echo "Built dist/syncme  (run it: ./dist/syncme)"

linux:
	@mkdir -p dist
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GOFLAGS) -o dist/syncme-linux-$$arch ./cmd/syncme || exit 1; \
	  CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build $(GOFLAGS) -o dist/syncme-relay-linux-$$arch ./cmd/syncme-relay || exit 1; \
	done
	@ls -l dist/

android:
	scripts/build-android.sh

all: linux android

arch:
	cd packaging/arch && SYNCME_SRC="$(CURDIR)" makepkg -f --cleanbuild
	@mkdir -p dist && mv packaging/arch/*.pkg.tar.zst dist/
	@echo "Install with: sudo pacman -U dist/*.pkg.tar.zst"

install: native
	install -Dm755 dist/syncme $(PREFIX)/bin/syncme
	install -Dm755 dist/syncme-relay $(PREFIX)/bin/syncme-relay
	$(PREFIX)/bin/syncme install

test:
	go test ./...
	scripts/e2e-test.sh

clean:
	rm -rf dist packaging/arch/src packaging/arch/pkg packaging/arch/syncme android/app/build android/core-test/build
