VERSION ?= dev
BIN := dist

WINDOWS_BINS := $(BIN)/woodgit-agent.exe $(BIN)/woodgit-agent-arm64.exe

.PHONY: all windows sign clean

all: windows

$(BIN):
	mkdir -p $(BIN)

$(BIN)/woodgit-agent.exe: $(shell find . -name '*.go') go.mod | $(BIN)
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.Version=$(VERSION)" -o $@ ./cmd/agent

$(BIN)/woodgit-agent-arm64.exe: $(shell find . -name '*.go') go.mod | $(BIN)
	GOOS=windows GOARCH=arm64 go build -ldflags="-s -w -X main.Version=$(VERSION)" -o $@ ./cmd/agent

windows: $(WINDOWS_BINS)

# Requires SIGN_PFX (+SIGN_PASSWORD) or SIGN_PVK+SIGN_SPC; see scripts/sign-windows.sh
sign: windows
	./scripts/sign-windows.sh $(BIN)/woodgit-agent.exe
	./scripts/sign-windows.sh $(BIN)/woodgit-agent-arm64.exe

clean:
	rm -rf $(BIN)
