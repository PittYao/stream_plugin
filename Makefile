# 变量
BINARY_NAME=stream_plugin
VERSION=1.0.0
BUILD_TIME=$(shell date -u '+%Y-%m-%d_%H:%M:%S')
GIT_HASH=$(shell git rev-parse --short HEAD 2>/dev/null || echo "none")

# 默认目标：Windows
.PHONY: all
all: windows

# Windows
.PHONY: windows
windows:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o bin/$(BINARY_NAME)_windows_amd64.exe .

# Linux
.PHONY: linux
linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/$(BINARY_NAME)_linux_amd64 .

# macOS
.PHONY: darwin
darwin:
	GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o bin/$(BINARY_NAME)_darwin_amd64 .

# 清理
.PHONY: clean
clean:
	rm -rf bin/

# 运行
.PHONY: run
run:
	go run .

# 依赖下载
.PHONY: deps
deps:
	go mod tidy
	go mod download
