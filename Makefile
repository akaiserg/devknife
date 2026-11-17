# devknife — offline developer toolbox (desktop app + CLI + MCP server)
VERSION ?= 0.1.0
LDFLAGS  = -s -w -X devknife/internal/cli.Version=$(VERSION)
export GOTOOLCHAIN ?= local
# silence deprecation warnings from the vendored webview C++ header
export CGO_CXXFLAGS ?= -Wno-deprecated-literal-operator

.PHONY: all build app run test vet vuln icon cli-all windows linux install clean

all: test build

## build: native binary with desktop window + CLI → bin/devknife
build:
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/devknife ./cmd/devknife

## app: macOS double-clickable app → dist/devknife.app (ARCHS="arm64 amd64" for universal)
app:
	VERSION=$(VERSION) ./scripts/package-macos.sh

## run: open the desktop app from source
run: build
	./bin/devknife app

## windows: run on Windows (needs a cgo toolchain, e.g. MSYS2 mingw-w64; WebView2 ships with Windows 10/11)
windows:
	set CGO_ENABLED=1&& go build -trimpath -ldflags "$(LDFLAGS) -H windowsgui -X devknife/internal/cli.AppMode=1" -o dist/devknife-app.exe ./cmd/devknife
	set CGO_ENABLED=1&& go build -trimpath -ldflags "$(LDFLAGS)" -o dist/devknife.exe ./cmd/devknife

## linux: run on Linux (needs libgtk-3-dev libwebkit2gtk-4.0-dev)
linux:
	CGO_ENABLED=1 go build -trimpath -ldflags "$(LDFLAGS)" -o dist/devknife ./cmd/devknife

## cli-all: cross-compiled CLI-only binaries (no native window; `devknife app` opens the browser UI)
cli-all:
	@for t in darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64; do \
		os=$${t%/*}; arch=$${t#*/}; ext=$$( [ $$os = windows ] && echo .exe ); \
		echo "→ $$t"; CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/cli/devknife-$$os-$$arch$$ext ./cmd/devknife || exit 1; \
	done

## install: copy the app to /Applications and put the CLI on PATH (macOS)
install: app
	rm -rf /Applications/devknife.app && cp -R dist/devknife.app /Applications/
	ln -sf /Applications/devknife.app/Contents/MacOS/devknife /usr/local/bin/devknife
	@echo "✓ installed: open devknife from Launchpad, or run 'devknife' in a terminal"

icon:
	go run ./scripts/mkicon -out build/icon

test:
	go test ./...

vet:
	go vet ./...

## vuln: check dependencies and the standard library for known vulnerabilities (needs network)
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.1.4 ./...

clean:
	rm -rf bin dist build
