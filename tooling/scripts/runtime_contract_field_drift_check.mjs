#!/usr/bin/env node
// Field-level drift check for runtime Cap'n Proto contracts.
//
// The .capnp schemas in runtime-sdk/protocols/system/v1 (plus the transport
// envelope) are the source of truth for runtime contract layout. capnpc is not
// run in this repo; the schemas are read by generators for constants and by
// hand-written host structs for control-plane descriptors. That leaves struct
// FIELDS unverified. This script closes that gap.
//
// Two categories, two policies:
//
//   A. Control-plane descriptors (held as native values host-side): every
//      language mirror must match the schema field-for-field (name, order,
//      @N-compatible type). Strict parity. Drift is a failure.
//
//   B. FFI capsules (passed as encoded bytes across the WASM boundary to
//      scaffolded Rust units): no host mirror by design. Reported as
//      informational; field-match is deferred to scaffold-time capnpc.
//
// A struct is classified B automatically when it has zero host mirrors, A
// otherwise. Add a name to CAPSULE_ALLOWLIST to assert "intentionally B" even
// if a stray mirror appears.

import { readFileSync, readdirSync, statSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const REPO_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");

const SCHEMA_DIRS = [
  "runtime-sdk/protocols/system/v1",
  "runtime-transport/schemas/foundation/v1",
];

// Where hand-written host mirrors live, per language.
const MIRROR_DIRS = {
  rust: ["runtime-sdk/rust/crates/ovrt-core/src", "runtime-native/rust/src"],
  go: ["runtime-sdk/go/runtimehost"],
  ts: ["runtime-sdk/ts/browser-host/src", "runtime-native/ts/src"],
};

// Structs known to be FFI capsules (category B) even if a mirror shows up.
const CAPSULE_ALLOWLIST = new Set([
  "runtimepayloadref",
  "runtimechunkdescriptor",
  "runtimesyscallrequest",
  "runtimesyscallresponse",
  "runtimecomputepayload",
  "runtimecomputelimits",
  "runtimecomputecapsule",
  "runtimecomputereceipt",
]);

// capnp primitive -> acceptable native types per language (strict).
// Struct/enum-typed fields (non-primitive) are matched by presence+order only,
// since each language names its own enum/struct type.
const TYPE_TABLE = {
  Text: { rust: ["String", "Option<String>", "&str"], go: ["string"], ts: ["string", "string | null"] },
  Data: { rust: ["Vec<u8>", "Option<Vec<u8>>"], go: ["[]byte"], ts: ["Uint8Array"] },
  Bool: { rust: ["bool"], go: ["bool"], ts: ["boolean"] },
  UInt32: { rust: ["u32", "Option<u32>"], go: ["uint32"], ts: ["number"] },
  UInt64: { rust: ["u64", "Option<u64>"], go: ["uint64"], ts: ["number", "bigint"] },
  Int32: { rust: ["i32"], go: ["int32"], ts: ["number"] },
  Int64: { rust: ["i64"], go: ["int64"], ts: ["number", "bigint"] },
  Float32: { rust: ["f32"], go: ["float32"], ts: ["number"] },
  Float64: { rust: ["f64"], go: ["float64"], ts: ["number"] },
};

const PRIMITIVES = new Set(Object.keys(TYPE_TABLE));

// Every native type that maps to a schema primitive. Used to tell a domain
// newtype (RuntimeRole) apart from a recognized primitive (String, uint32).
const RECOGNIZED_TYPES = new Set(
  Object.values(TYPE_TABLE).flatMap((byLang) => Object.values(byLang).flat()),
);

// A PascalCase type that isn't a recognized primitive mapping is a domain
// newtype/enum over the wire primitive — the intended host-side representation.
function isDomainNewtype(type) {
  const base = type.replace(/Option<|>|\s*\|\s*null|\?/g, "").trim();
  return /^[A-Z]/.test(base) && !RECOGNIZED_TYPES.has(base);
}

function main() {
  const strict = !process.argv.includes("--report");
  const schemaStructs = collectSchemaStructs();
  const mirrors = {
    rust: collectMirrors("rust", parseRustStructs),
    go: collectMirrors("go", parseGoStructs),
    ts: collectMirrors("ts", parseTsStructs),
  };

  const findings = [];
  const capsules = [];
  let checked = 0;

  for (const s of schemaStructs) {
    const key = norm(s.name);
    const present = { rust: mirrors.rust.get(key), go: mirrors.go.get(key), ts: mirrors.ts.get(key) };
    const anyMirror = present.rust || present.go || present.ts;
    const isCapsule = CAPSULE_ALLOWLIST.has(key) || !anyMirror;

    if (isCapsule) {
      capsules.push({ name: s.name, fields: s.fields.length, mirrors: langsPresent(present) });
      continue;
    }

    checked += 1;
    for (const lang of ["rust", "go", "ts"]) {
      const mirror = present[lang];
      if (!mirror) {
        findings.push(`[${s.name}] missing ${lang} mirror (schema declares it as a control-plane descriptor)`);
        continue;
      }
      diffFields(s, mirror, lang, findings);
    }
  }

  report(schemaStructs, checked, capsules, findings, mirrors);

  if (findings.length > 0 && strict) process.exit(1);
}

function diffFields(schemaStruct, mirror, lang, findings) {
  const schemaFields = schemaStruct.fields;
  const mirrorFields = mirror.fields;
  const label = `[${schemaStruct.name}/${lang}]`;

  // Presence + order: walk schema order, expect mirror to match position-for-position.
  const schemaNames = schemaFields.map((f) => norm(f.name));
  const mirrorNames = mirrorFields.map((f) => norm(f.name));

  const missing = schemaNames.filter((n) => !mirrorNames.includes(n));
  const extra = mirrorNames.filter((n) => !schemaNames.includes(n));
  for (const n of missing) findings.push(`${label} drops schema field '${n}'`);
  for (const n of extra) findings.push(`${label} has extra field '${n}' not in schema`);

  // Order: compare the common subsequence.
  const common = schemaNames.filter((n) => mirrorNames.includes(n));
  const mirrorCommon = mirrorNames.filter((n) => common.includes(n));
  if (common.join(",") !== mirrorCommon.join(",")) {
    findings.push(`${label} field order differs: schema[${common.join(",")}] vs mirror[${mirrorCommon.join(",")}]`);
  }

  // Types: only primitive schema fields are strictly typed. A field may be the
  // mapped primitive OR a domain newtype/enum over it (the deliberate
  // "category as UInt32/Text on the wire, strong enum in the host" pattern). A
  // *different builtin* (e.g. Go int where the schema says UInt32) is drift.
  for (const sf of schemaFields) {
    const mf = mirrorFields.find((f) => norm(f.name) === norm(sf.name));
    if (!mf) continue;
    if (!PRIMITIVES.has(sf.type)) continue; // enum/struct-typed schema field: skip
    const accepted = TYPE_TABLE[sf.type][lang];
    if (accepted.includes(mf.type)) continue;
    if (isDomainNewtype(mf.type)) continue; // strong enum/newtype over the primitive
    findings.push(`${label} field '${norm(sf.name)}': schema ${sf.type} but mirror is '${mf.type}' (want ${accepted.join("|")} or a domain newtype)`);
  }
}

// ---- schema parsing ------------------------------------------------------

function collectSchemaStructs() {
  const out = [];
  for (const dir of SCHEMA_DIRS) {
    for (const file of listFiles(path.join(REPO_ROOT, dir), ".capnp")) {
      out.push(...parseCapnpStructs(readFileSync(file, "utf8")));
    }
  }
  return out;
}

function parseCapnpStructs(text) {
  const structs = [];
  const structRe = /struct\s+([A-Za-z0-9_]+)\s*\{([^}]*)\}/g;
  let m;
  while ((m = structRe.exec(text)) !== null) {
    const name = m[1];
    const body = m[2];
    const fields = [];
    const fieldRe = /^\s*([A-Za-z0-9_]+)\s*@(\d+)\s*:\s*([A-Za-z0-9_().]+)\s*;/gm;
    let fm;
    while ((fm = fieldRe.exec(body)) !== null) {
      fields.push({ name: fm[1], ordinal: Number(fm[2]), type: fm[3] });
    }
    fields.sort((a, b) => a.ordinal - b.ordinal);
    structs.push({ name, fields });
  }
  return structs;
}

// ---- mirror parsing ------------------------------------------------------

function collectMirrors(lang, parser) {
  const map = new Map();
  for (const dir of MIRROR_DIRS[lang]) {
    const abs = path.join(REPO_ROOT, dir);
    for (const file of listFiles(abs, extFor(lang))) {
      for (const s of parser(readFileSync(file, "utf8"))) {
        if (!map.has(norm(s.name))) map.set(norm(s.name), s);
      }
    }
  }
  return map;
}

function parseRustStructs(text) {
  const out = [];
  const re = /pub struct\s+([A-Za-z0-9_]+)\s*\{([^}]*)\}/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    const fields = [];
    const fre = /^\s*pub\s+([A-Za-z0-9_]+)\s*:\s*([^,]+),/gm;
    let fm;
    while ((fm = fre.exec(m[2])) !== null) fields.push({ name: fm[1], type: fm[2].trim() });
    out.push({ name: m[1], fields });
  }
  return out;
}

function parseGoStructs(text) {
  const out = [];
  const re = /type\s+([A-Za-z0-9_]+)\s+struct\s*\{([^}]*)\}/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    const fields = [];
    const fre = /^\s*([A-Za-z0-9_]+)\s+([A-Za-z0-9_.\[\]*]+)(?:\s+`[^`]*`)?/gm;
    let fm;
    while ((fm = fre.exec(m[2])) !== null) {
      if (fm[1] === "struct") continue;
      fields.push({ name: fm[1], type: fm[2].trim() });
    }
    out.push({ name: m[1], fields });
  }
  return out;
}

function parseTsStructs(text) {
  const out = [];
  const re = /export type\s+([A-Za-z0-9_]+)\s*=\s*\{([^}]*)\}/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    const fields = [];
    const fre = /^\s*([A-Za-z0-9_]+)\??\s*:\s*([^;]+);/gm;
    let fm;
    while ((fm = fre.exec(m[2])) !== null) fields.push({ name: fm[1], type: fm[2].trim() });
    out.push({ name: m[1], fields });
  }
  return out;
}

// ---- helpers -------------------------------------------------------------

function norm(name) {
  return name.replace(/[_\s]/g, "").toLowerCase();
}

function langsPresent(present) {
  return ["rust", "go", "ts"].filter((l) => present[l]).join(",") || "none";
}

function extFor(lang) {
  return lang === "rust" ? ".rs" : lang === "go" ? ".go" : ".ts";
}

function listFiles(dir, ext) {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return [];
  }
  const out = [];
  for (const e of entries) {
    const p = path.join(dir, e);
    const st = statSync(p);
    if (st.isDirectory()) out.push(...listFiles(p, ext));
    else if (p.endsWith(ext) && !p.endsWith(".test.ts") && !p.endsWith(".bench.ts")) out.push(p);
  }
  return out;
}

function report(schemaStructs, checked, capsules, findings, mirrors) {
  console.log(`runtime contract field drift check`);
  console.log(`  schema structs: ${schemaStructs.length}`);
  console.log(`  control-plane (strict parity): ${checked}`);
  console.log(`  ffi capsules (informational):  ${capsules.length}`);
  console.log("");
  console.log("FFI capsules — no host mirror by design (field-match deferred to scaffold capnpc):");
  for (const c of capsules) console.log(`  - ${c.name} (${c.fields} fields, mirrors: ${c.mirrors})`);
  console.log("");
  if (findings.length === 0) {
    console.log("[OK] all control-plane descriptors match the schema field-for-field");
    return;
  }
  console.log(`[FAIL] ${findings.length} field drift(s) in control-plane descriptors:`);
  for (const f of findings) console.log(`  - ${f}`);
}

main();
