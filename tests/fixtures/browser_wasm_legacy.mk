build-rust-wasm:
	@if [ -f "$(RUST_WASM_DIR)/Cargo.toml" ]; then \
		echo "Building Rust WASM modules..."; \
		mkdir -p "$(WASM_MODULE_DIR)"; \
		if [ "$(RUST_WASM_PROFILE)" = "release" ]; then \
			cargo build --manifest-path "$(RUST_WASM_DIR)/Cargo.toml" --target "$(RUST_TARGET)" --release; \
			src_dir="$(RUST_WASM_DIR)/target/$(RUST_TARGET)/release"; \
		else \
			cargo build --manifest-path "$(RUST_WASM_DIR)/Cargo.toml" --target "$(RUST_TARGET)"; \
			src_dir="$(RUST_WASM_DIR)/target/$(RUST_TARGET)/debug"; \
		fi; \
		if ls "$$src_dir"/*.wasm >/dev/null 2>&1; then \
			cp "$$src_dir"/*.wasm "$(WASM_MODULE_DIR)/"; \
			if command -v "$(WASM_OPT)" >/dev/null 2>&1; then for wasm in "$(WASM_MODULE_DIR)"/*.wasm; do tmp="$$wasm.opt"; if "$(WASM_OPT)" $(WASM_OPT_FLAGS) -o "$$tmp" "$$wasm"; then mv "$$tmp" "$$wasm"; else rm -f "$$tmp"; echo "wasm-opt failed; keeping unoptimized $$wasm"; fi; done; fi; \
			if command -v "$(BROTLI)" >/dev/null 2>&1; then for wasm in "$(WASM_MODULE_DIR)"/*.wasm; do "$(BROTLI)" -f "$$wasm"; done; fi; \
		fi; \
	else \
		echo "No $(RUST_WASM_DIR)/Cargo.toml; skipping Rust WASM modules"; \
	fi
	@$(MAKE) wasm-manifest

wasm-manifest:
	@echo manifest
