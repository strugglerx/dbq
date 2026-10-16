.PHONY: all build install test clean cross-build package release help

BINARY_NAME=dbq
DIST_DIR=dist
VERSION?=$(shell git describe --tags --always 2>/dev/null || echo "v0.1.0")

# 默认构建当前平台
all: build

help:
	@echo "dbq Makefile 常用指令:"
	@echo "  make build               - 编译当前平台二进制"
	@echo "  make install             - 安装二进制到 \$$GOPATH/bin"
	@echo "  make test                - 运行单元测试"
	@echo "  make cross-build         - 交叉编译 5 大主流平台二进制到 dist/"
	@echo "  make package             - 交叉编译并打包 tar.gz / zip 与 sha256 校验和"
	@echo "  make release VERSION=vX  - 一键打包并发布到 GitHub Releases (依赖 gh CLI)"
	@echo "  make clean               - 清理本地二进制与 dist 目录"

build:
	go build -ldflags="-s -w" -o $(BINARY_NAME) .

install:
	go install -ldflags="-s -w" .

test:
	go test -v ./...

clean:
	rm -rf $(BINARY_NAME) $(DIST_DIR) test.db

# 交叉编译全平台二进制
cross-build:
	@echo "==> 正在跨平台交叉编译 (CGO_ENABLED=0)..."
	@mkdir -p $(DIST_DIR)
	CGO_ENABLED=0 GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 .
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 .
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 .
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 .
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(DIST_DIR)/$(BINARY_NAME)-windows-amd64.exe .
	@echo "==> 编译完成，产物位于 $(DIST_DIR)/"

# 打包归档与校验和
package: clean cross-build
	@echo "==> 正在打包归档 ($(VERSION))..."
	@cd $(DIST_DIR) && \
	tar -czf $(BINARY_NAME)_$(VERSION)_darwin_arm64.tar.gz $(BINARY_NAME)-darwin-arm64 && \
	tar -czf $(BINARY_NAME)_$(VERSION)_darwin_amd64.tar.gz $(BINARY_NAME)-darwin-amd64 && \
	tar -czf $(BINARY_NAME)_$(VERSION)_linux_amd64.tar.gz $(BINARY_NAME)-linux-amd64 && \
	tar -czf $(BINARY_NAME)_$(VERSION)_linux_arm64.tar.gz $(BINARY_NAME)-linux-arm64 && \
	zip -q $(BINARY_NAME)_$(VERSION)_windows_amd64.zip $(BINARY_NAME)-windows-amd64.exe && \
	shasum -a 256 $(BINARY_NAME)* > checksums.txt
	@echo "==> 打包完成，校验和已生成: $(DIST_DIR)/checksums.txt"

# 一键发布到 GitHub Release (例如: make release VERSION=v0.2.0)
release: package
	@which gh >/dev/null 2>&1 || (echo "错误: 未检测到 GitHub CLI (gh)，请先安装: brew install gh" && exit 1)
	@echo "==> 正在发布 $(VERSION) 到 GitHub Releases..."
	gh release create $(VERSION) \
		$(DIST_DIR)/*.tar.gz \
		$(DIST_DIR)/*.zip \
		$(DIST_DIR)/$(BINARY_NAME)-darwin-* \
		$(DIST_DIR)/$(BINARY_NAME)-linux-* \
		$(DIST_DIR)/$(BINARY_NAME)-windows-* \
		$(DIST_DIR)/checksums.txt \
		--title "$(BINARY_NAME) $(VERSION)" \
		--generate-notes
	@echo "🎉 发布成功: $$(gh release view $(VERSION) --json url -q .url)"
