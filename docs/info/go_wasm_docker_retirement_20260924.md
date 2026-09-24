# Go WASM Retirement in Docker Builds

Date: 2026-09-24
Scope: Foundation managed patches and project Docker builds.

## Deployment failure

Ovasabi retained a custom `go-wasm-builder` stage after the updater removed the retired `wasm/` directory.
The frontend stage still copied `main.wasm` and `wasm_exec.js` from that stage.
BuildKit followed these dependencies and rejected the missing `COPY wasm/` input before compilation.
The earlier scaffold checks did not detect this dependency.

## Correction

The managed retirement patch now recognizes the recorded Go stage by its SHA-256 digest.
It removes that stage and its two known artifact copies.
It preserves the Rust stage, Rust artifacts, frontend stages, and project configuration.
The repair also applies when the source directory has already been removed.

Custom stages, unknown consumers, and numeric stage references stop retirement before file changes.
Numeric references require review because removing a stage changes their indexes.
The managed wrapper now reports these cases as update failures.

Scaffold validation rejects retired Go inputs in Dockerfiles under the project root, `frontend/`, and `docker/`.
The check supports `Dockerfile` and `Dockerfile.*` names.
Its command entry resolves filesystem aliases, including the macOS `/tmp` alias.

## Evidence ledger

- Contract: No runtime ABI or request contract changed. Docker builds exclude the retired Go browser shim.
- Invariant: Rust WASM and project stages remain unchanged. Unknown Docker customizations require review.
- Evidence: Retirement tests, updater integration, scaffold validation, and managed patch hygiene.
- Fallback: Unsupported changes stop with a review message. The patch does not restore the retired shim.
- Ownership: Core owns the patch and checks. The create-mode Dockerfile remains project-owned.
- Regression guards: Already-retired sources, repeated updates, custom stages, numeric references, aliases, and stale context inputs.
- Documentation: This report records the missed dependency and correction.

Execution logs reside in `test-results/go-wasm-docker-retirement-20260924/`.

## Verification results

Nine retirement tests passed, including refusal of custom stages and a failing managed wrapper.
Updater integration, scaffold manifest, managed patch hygiene, shell syntax, and documentation checks passed.
All twelve inspected projects passed the Docker reference audit.
The normal fleet updater completed for all eleven indexed projects, with scaffold validation and idempotence checks.
Metered received an audit; it is absent from the fleet index.

The real Ovasabi build exposed two additional TypeScript blockers after Rust compilation succeeded.
Two unused `Style` objects were removed from project content components.
The frontend build stage now copies its existing protobuf fixture before type checking.
No TypeScript checks were disabled. The fixture remains outside the final image.
The changed components passed ESLint. Existing content and protobuf tests passed all 25 cases.
Vitest reported existing evaluation warnings and a delayed server shutdown, then exited successfully.

The complete local `frontend` image built successfully with tag `ovasabi-frontend:wasm-retirement-check`.
The context contained working copies of tracked deployment files, excluding local dependency and compiler caches.
The first attempt encountered a transient Rust download failure. The retry reached compilation successfully.
This verification built a local image. It did not deploy a production release.
