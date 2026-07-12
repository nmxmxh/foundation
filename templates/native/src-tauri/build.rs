fn main() {
    tauri_build::try_build(
        tauri_build::Attributes::new().app_manifest(
            tauri_build::AppManifest::new().commands(&[
                "foundation_runtime_dispatch",
                "foundation_runtime_capabilities",
            ]),
        ),
    )
    .expect("failed to build the bounded Foundation Tauri command manifest");
}
