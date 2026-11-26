# Syncme build entry point.
#
#   make            Linux binaries for this machine -> dist/
#   make linux      Linux binaries for x86_64 and arm64 -> dist/
#   make macos      macOS binaries (Apple Silicon + Intel) -> dist/
#   make windows    Windows .exe (x64 + ARM) -> dist/
#   make deb        Ubuntu/Debian packages (needs dpkg-deb) -> dist/
#   make dist       everything above
#   make android    Android APK -> dist/syncme.apk (downloads the Android SDK if needed)
#   make all        both of the above
#   make arch       Arch Linux package (run on Arch) -> dist/*.pkg.tar.zst
#   make install    install syncme for the current user (~/.local/bin) and start it
#   make test       unit + end-to-end tests

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null | sed "s/^v//" | grep -E "^[0-9]" || echo 0.0.0-dev)
GOFLAGS  = -trimpath -ldflags "-s -w -X main.version=$(VERSION)"
PREFIX  ?= $(HOME)/.local

.PHONY: native linux macos windows deb dist android all arch install test clean

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

macos:
	@mkdir -p dist
	for arch in arm64 amd64; do \
	  CGO_ENABLED=0 GOOS=darwin GOARCH=$$arch go build $(GOFLAGS) -o dist/syncme-macos-$$arch ./cmd/syncme || exit 1; \
	  CGO_ENABLED=0 GOOS=darwin GOARCH=$$arch go build $(GOFLAGS) -o dist/syncme-relay-macos-$$arch ./cmd/syncme-relay || exit 1; \
	done

windows:
	@mkdir -p dist
	for arch in amd64 arm64; do \
	  CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build $(GOFLAGS) -o dist/syncme-windows-$$arch.exe ./cmd/syncme || exit 1; \
	  CGO_ENABLED=0 GOOS=windows GOARCH=$$arch go build $(GOFLAGS) -o dist/syncme-relay-windows-$$arch.exe ./cmd/syncme-relay || exit 1; \
	done

deb:
	for arch in amd64 arm64; do packaging/deb/build-deb.sh $$arch $(VERSION) dist || exit 1; done

dist: linux macos windows deb
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
