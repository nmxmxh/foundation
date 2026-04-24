# Post-Quantum Security Posture

Date: 2026-04-24

Foundation protects for post-quantum risk through crypto agility and edge/runtime policy, not by adding expensive cryptography to render or service hot paths.

## Baseline

1. Treat post-quantum readiness as a migration program: inventory cryptography, classify long-lived data, keep algorithms configurable, and test replacements before forcing them.
2. Use standardized algorithms only. The current NIST standards are ML-KEM for key establishment, ML-DSA for signatures, and SLH-DSA for stateless hash-based signatures.
3. Prefer hybrid TLS key exchange where the platform provides it. In Go services, stay on a Go release that enables hybrid post-quantum TLS by default or configure the same through the edge proxy when termination happens outside Go.
4. Keep app payload protocols algorithm-agile: envelopes must carry versioned metadata and must not bake one signature, hash, or KEM into domain code.
5. Do not run post-quantum signatures or KEMs inside React render, animation, or worker frame loops. Use TLS/session establishment, artifact signing, key rotation, and background verification boundaries instead.

## Required Project Defaults

1. `config-contracts` exposes `security.postQuantum` with `tlsHybridKEM`, `signatureAlgorithm`, `cryptoInventoryRequired`, and `longLivedArtifactSigning`.
2. New projects default to `tlsHybridKEM=auto`, `signatureAlgorithm=classical`, `cryptoInventoryRequired=true`, and `longLivedArtifactSigning=false`.
3. `required` hybrid KEM mode is reserved for environments where the serving edge and client fleet are known to support it. Public consumer apps should start with `auto`.
4. `ml-dsa` or `slh-dsa` signatures should be used only for long-lived artifacts, release attestations, or compliance-driven signing after benchmark review.
5. Short-lived API authentication remains classical JWT/session auth unless a concrete threat model requires artifact-level signatures.
6. Go services that terminate TLS directly can use `server-kit/go/security.ApplyPostQuantumTLS`; services behind nginx, Traefik, or a cloud edge must configure the equivalent policy at that TLS terminator.

## Performance Rules

1. Quantum-safe negotiation belongs at TLS or durable artifact boundaries, not per-command domain handlers.
2. Long-lived blobs can be signed or re-signed asynchronously during upload/finalization workers.
3. Request/response hot paths must measure p95/p99 latency before enabling stronger signature modes.
4. Compression must stay disabled when reflected attacker-controlled data is mixed with secrets or one-time tokens.
5. WebAssembly and SharedArrayBuffer work should focus on deterministic compute and memory movement; crypto upgrades should not compete with UI/render work.

## References

1. NIST FIPS 203: ML-KEM, Module-Lattice-Based Key-Encapsulation Mechanism.
2. NIST FIPS 204: ML-DSA, Module-Lattice-Based Digital Signature Standard.
3. NIST FIPS 205: SLH-DSA, Stateless Hash-Based Digital Signature Standard.
4. CISA and NIST post-quantum migration guidance: inventory, prioritize, and migrate cryptographic systems.
