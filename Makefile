# SSXV_Manager 构建脚本
GO=go
WASM_SRC := ./cmd/ssxv-wasm
WASM_OUT := web/public/wasm/ssxv.wasm
WASM_EXEC_SRC := web/src/wasm_exec.js
WASM_EXEC := $(shell $(GO) env GOROOT)/lib/wasm/wasm_exec.js

.PHONY: wasm web cli all test

wasm: ## 构建 WASM 解码核心（js/wasm）
	GOOS=js GOARCH=wasm $(GO) build -o $(WASM_OUT) $(WASM_SRC)
	cp "$(WASM_EXEC)" $(WASM_EXEC_SRC)
	@ls -la web/public/wasm/

web: wasm ## 构建 Web 前端产物（含 WASM）
	cd web && npm run build

cli: ## 构建 CLI
	$(GO) build -o ssxv-cli ./cmd/ssxv-cli

test: ## 运行 Go 测试
	$(GO) test ./...

all: web
