# Build and packaging for applemusic-rp.
#
# The interesting targets are `mac-app` and `windows`; both produce something a
# user can actually launch, rather than a bare binary.

BINARY      := applemusic-rp
APP_NAME    := AppleMusicRP
DISPLAY_NAME := Apple Music Rich Presence
PUBLISHER   := Layttos
SUMMARY     := Shows the track playing in Apple Music on your Discord profile
BUNDLE_ID   := dev.layttos.applemusic-rp
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DIST        := dist

# Stripping the symbol table and DWARF data roughly halves the binary; nothing
# in the daemon needs them at runtime.
LDFLAGS     := -s -w -X main.version=$(VERSION)

.PHONY: all build test vet fmt check clean mac-app windows windows-installer syso headless install-mac uninstall-mac icons

all: check build

## build: compile for the host platform
build:
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) .

## headless: compile without the tray, for a pure background daemon
headless:
	go build -tags notray -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY)-headless .

## test: run the test suite
test:
	go test ./...

## vet: static analysis for every supported platform
vet:
	go vet ./...
	GOOS=windows go vet ./...

fmt:
	gofmt -l -w .

check: fmt vet test

## icons: rasterise internal/ui/icon.svg into the PNG and ICO the program embeds
icons:
	go run ./tools/genicons internal/ui

## mac-app: build AppleMusicRP.app, a menu bar application with no Dock icon
mac-app:
	@mkdir -p "$(DIST)/$(APP_NAME).app/Contents/MacOS" "$(DIST)/$(APP_NAME).app/Contents/Resources"
	@echo "building a universal binary…"
	@CGO_ENABLED=1 GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o "$(DIST)/.$(BINARY)-arm64" . 
	@CGO_ENABLED=1 GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o "$(DIST)/.$(BINARY)-amd64" . 2>/dev/null \
		&& lipo -create -output "$(DIST)/$(APP_NAME).app/Contents/MacOS/$(BINARY)" "$(DIST)/.$(BINARY)-arm64" "$(DIST)/.$(BINARY)-amd64" \
		|| { echo "note: only the host architecture could be built"; cp "$(DIST)/.$(BINARY)-arm64" "$(DIST)/$(APP_NAME).app/Contents/MacOS/$(BINARY)"; }
	@rm -f "$(DIST)/.$(BINARY)-arm64" "$(DIST)/.$(BINARY)-amd64"
	@sed -e 's/@VERSION@/$(VERSION)/g' -e 's/@BUNDLE_ID@/$(BUNDLE_ID)/g' -e 's/@APP_NAME@/$(APP_NAME)/g' -e 's/@BINARY@/$(BINARY)/g' \
		packaging/macos/Info.plist > "$(DIST)/$(APP_NAME).app/Contents/Info.plist"
	@$(MAKE) --no-print-directory icns
	@echo "built $(DIST)/$(APP_NAME).app"
	@echo "install it with:  cp -R $(DIST)/$(APP_NAME).app /Applications/"

# icns: turn the PNG into the bundle's icon using the tools that ship with macOS
.PHONY: icns
icns:
	@rm -rf "$(DIST)/AppIcon.iconset" && mkdir -p "$(DIST)/AppIcon.iconset"
	@for size in 16 32 128 256 512; do \
		sips -z $$size $$size internal/ui/icon.png --out "$(DIST)/AppIcon.iconset/icon_$${size}x$${size}.png" >/dev/null 2>&1; \
		double=$$((size * 2)); \
		sips -z $$double $$double internal/ui/icon.png --out "$(DIST)/AppIcon.iconset/icon_$${size}x$${size}@2x.png" >/dev/null 2>&1; \
	done
	@iconutil -c icns "$(DIST)/AppIcon.iconset" -o "$(DIST)/$(APP_NAME).app/Contents/Resources/AppIcon.icns" 2>/dev/null || true
	@rm -rf "$(DIST)/AppIcon.iconset"

## syso: the executable's own icon, manifest and version information
# The Go linker picks up any *.syso sitting in the package directory. The name
# pins this one to Windows on amd64, so every other build ignores it.
syso:
	go run ./tools/gensyso \
		-ico internal/ui/icon.ico \
		-manifest packaging/windows/app.manifest \
		-o resource_windows_amd64.syso \
		-version "$(VERSION)" \
		-company "$(PUBLISHER)" \
		-product "$(DISPLAY_NAME)" \
		-description "$(SUMMARY)" \
		-copyright "MIT licence" \
		-filename "$(BINARY).exe"

## windows: cross-compile the Windows executable, icon and all
# -H windowsgui suppresses the console window, which a tray application must not
# leave sitting behind it.
windows: syso
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS) -H windowsgui" -o $(DIST)/$(BINARY).exe .
	@echo "built $(DIST)/$(BINARY).exe"
	@echo "to start it with Windows, put a shortcut in the folder that opens with:  shell:startup"

## windows-installer: build the installer; needs Windows, PowerShell and Inno Setup
# The wrapper does the same three steps as `make windows` before calling the
# Inno Setup compiler, so it is the single command to run on Windows.
windows-installer:
	powershell -NoProfile -ExecutionPolicy Bypass -File ./build-windows.ps1

## install-mac: install the headless daemon as a per-user LaunchAgent
install-mac: headless
	@mkdir -p "$$HOME/Library/Application Support/$(APP_NAME)" "$$HOME/Library/LaunchAgents" "$$HOME/Library/Logs/$(APP_NAME)"
	install -m 0755 $(DIST)/$(BINARY)-headless "$$HOME/Library/Application Support/$(APP_NAME)/$(BINARY)"
	@sed -e "s|@BINARY_PATH@|$$HOME/Library/Application Support/$(APP_NAME)/$(BINARY)|g" \
		-e "s|@LOG_DIR@|$$HOME/Library/Logs/$(APP_NAME)|g" \
		-e 's|@BUNDLE_ID@|$(BUNDLE_ID)|g' \
		packaging/macos/launchagent.plist > "$$HOME/Library/LaunchAgents/$(BUNDLE_ID).plist"
	-launchctl bootout gui/$$(id -u)/$(BUNDLE_ID) 2>/dev/null
	launchctl bootstrap gui/$$(id -u) "$$HOME/Library/LaunchAgents/$(BUNDLE_ID).plist"
	@echo "installed and started; logs are in ~/Library/Logs/$(APP_NAME)/"

## uninstall-mac: stop and remove the LaunchAgent
uninstall-mac:
	-launchctl bootout gui/$$(id -u)/$(BUNDLE_ID) 2>/dev/null
	rm -f "$$HOME/Library/LaunchAgents/$(BUNDLE_ID).plist"
	rm -rf "$$HOME/Library/Application Support/$(APP_NAME)"
	@echo "removed"

clean:
	rm -rf $(DIST) resource_windows_amd64.syso
