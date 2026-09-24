build-wasm:
	@if [ -d wasm ]; then \
		echo "Building WASM..."; \
		mkdir -p "$(WASM_PUBLIC_DIR)"; \
		GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" GOOS=js GOARCH=wasm $(GOBUILD) -ldflags="-s -w" -o "$(WASM_PUBLIC_DIR)/main.wasm" ./wasm; \
		goroot=$$(go env GOROOT); \
		if [ -f "$$goroot/lib/wasm/wasm_exec.js" ]; then cp "$$goroot/lib/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; \
		elif [ -f "$$goroot/misc/wasm/wasm_exec.js" ]; then cp "$$goroot/misc/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; fi; \
		if command -v "$(WASM_OPT)" >/dev/null 2>&1; then tmp="$(WASM_PUBLIC_DIR)/main.wasm.opt"; if "$(WASM_OPT)" $(WASM_OPT_FLAGS) -o "$$tmp" "$(WASM_PUBLIC_DIR)/main.wasm"; then mv "$$tmp" "$(WASM_PUBLIC_DIR)/main.wasm"; else rm -f "$$tmp"; echo "wasm-opt failed; keeping unoptimized main.wasm"; fi; fi; \
		if command -v "$(BROTLI)" >/dev/null 2>&1; then "$(BROTLI)" -f "$(WASM_PUBLIC_DIR)/main.wasm"; fi; \
	fi
	@$(MAKE) wasm-manifest

build-wasm-dev:
	@if [ -d wasm ]; then \
		echo "Building WASM (development)..."; \
		mkdir -p "$(WASM_PUBLIC_DIR)"; \
		GOCACHE="$(GO_CACHE_DIR)" GOMODCACHE="$(GO_MOD_CACHE_DIR)" GOOS=js GOARCH=wasm $(GOBUILD) -o "$(WASM_PUBLIC_DIR)/main.wasm" ./wasm; \
		goroot=$$(go env GOROOT); \
		if [ -f "$$goroot/lib/wasm/wasm_exec.js" ]; then cp "$$goroot/lib/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; \
		elif [ -f "$$goroot/misc/wasm/wasm_exec.js" ]; then cp "$$goroot/misc/wasm/wasm_exec.js" "$(WASM_PUBLIC_DIR)/wasm_exec.js"; fi; \
	fi
	@$(MAKE) wasm-manifest

