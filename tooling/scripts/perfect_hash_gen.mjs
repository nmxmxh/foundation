#!/usr/bin/env node
/**
 * perfect_hash_gen.mjs
 *
 * Near-minimal perfect hash generator for small, fixed Go key sets.
 * Association-table algorithm (gperf-style):
 *
 *   slot = (byteLen(key) + asso[byteAt(key, p0)] + asso[byteAt(key, p1)] + ...) & mask
 *
 * Rules that the emitted Go code also follows:
 * - Keys are UTF-8 byte strings. Length and positions count bytes, as Go does.
 * - byteAt returns 0 for a position outside the key, so short keys are found.
 * - The lookup always compares the full key, so a miss can never alias a hit.
 * - The search is bounded (maxSteps per attempt). It fails loudly, never hangs.
 *
 * It emits Go only.
 *
 * CLI:
 *   node tooling/scripts/perfect_hash_gen.mjs <spec.json> [--out file.go] [--check]
 *
 * spec.json:
 *   { "package": "vectorstr", "function": "lookupX", "valueType": "MyID",
 *     "zeroValue": "MyUnknown", "entries": { "key": "MyValueExpr", ... } }
 *
 * --check exits 1 when the output file differs from a fresh generation (CI drift gate).
 */

import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const DEFAULT_MAX_STEPS = 2_000_000;

const POSITION_SETS = [[0], [0, -1], [0, 1], [0, -1, 1], [0, 1, 2], [0, 1, -1, 2]];

const toBytes = (key) => {
  if (typeof key !== "string" || key === "") {
    throw new Error(`key must be a non-empty string, got ${JSON.stringify(key)}`);
  }
  return Buffer.from(key, "utf8");
};

const byteAt = (bytes, pos) => {
  const index = pos >= 0 ? pos : bytes.length + pos;
  return index >= 0 && index < bytes.length ? bytes[index] : 0;
};

const slotOf = (bytes, positions, asso, mask) => {
  let sum = bytes.length;
  for (const pos of positions) {
    sum += asso[byteAt(bytes, pos)];
  }
  return sum & mask;
};

export function findPerfectHash(keys, options = {}) {
  if (!Array.isArray(keys) || keys.length === 0) {
    throw new Error("keys must be a non-empty array of strings");
  }
  const maxSteps = options.maxSteps ?? DEFAULT_MAX_STEPS;
  const uniqueKeys = Array.from(new Set(keys)).sort();
  const encoded = uniqueKeys.map((key) => ({ key, bytes: toBytes(key) }));

  let tableSize = 1;
  while (tableSize < uniqueKeys.length) {
    tableSize <<= 1;
  }
  const maxTableSize = tableSize << 2;

  for (let size = tableSize; size <= maxTableSize; size <<= 1) {
    for (const positions of POSITION_SETS) {
      const solution = solveAssociation(encoded, positions, size, maxSteps);
      if (solution) {
        return { keys: uniqueKeys, tableSize: size, mask: size - 1, positions, ...solution };
      }
    }
  }
  throw new Error(`no perfect hash for ${uniqueKeys.length} keys up to table size ${maxTableSize}`);
}

function solveAssociation(encoded, positions, tableSize, maxSteps) {
  const mask = tableSize - 1;
  const frequency = new Map();
  for (const { bytes } of encoded) {
    for (const pos of positions) {
      const b = byteAt(bytes, pos);
      frequency.set(b, (frequency.get(b) ?? 0) + 1);
    }
  }
  const order = Array.from(frequency.keys()).sort((a, b) => frequency.get(b) - frequency.get(a));
  const rank = new Map(order.map((b, i) => [b, i]));

  // A key becomes checkable once its last-ranked byte is assigned.
  const keysReadyAt = order.map(() => []);
  for (const entry of encoded) {
    let last = 0;
    for (const pos of positions) {
      last = Math.max(last, rank.get(byteAt(entry.bytes, pos)));
    }
    keysReadyAt[last].push(entry);
  }

  const asso = new Array(256).fill(0);
  const maxValue = Math.min(tableSize * 2, 255);
  let steps = 0;

  const place = (index, occupied) => {
    if (index === order.length) {
      return true;
    }
    const b = order[index];
    for (let value = 0; value <= maxValue; value++) {
      if (++steps > maxSteps) {
        return false;
      }
      asso[b] = value;
      const added = [];
      let ok = true;
      for (const { bytes } of keysReadyAt[index]) {
        const slot = slotOf(bytes, positions, asso, mask);
        if (occupied.has(slot)) {
          ok = false;
          break;
        }
        occupied.add(slot);
        added.push(slot);
      }
      if (ok && place(index + 1, occupied)) {
        return true;
      }
      for (const slot of added) {
        occupied.delete(slot);
      }
    }
    asso[b] = 0;
    return false;
  };

  if (!place(0, new Set())) {
    return null;
  }
  const slots = new Array(tableSize).fill(null);
  for (const { key, bytes } of encoded) {
    slots[slotOf(bytes, positions, asso, mask)] = key;
  }
  return { asso, slots };
}

const goIdent = (name, what) => {
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name ?? "")) {
    throw new Error(`${what} must be a Go identifier, got ${JSON.stringify(name)}`);
  }
  return name;
};

// JSON string escapes are valid Go interpreted-string escapes.
const goString = (value) => JSON.stringify(value);

export function generateGoPerfectMap({ packageName, functionName, valueType, zeroValue = "0", entries }) {
  goIdent(packageName, "package");
  goIdent(functionName, "function");
  const solution = findPerfectHash(Object.keys(entries));
  const { tableSize, mask, positions, asso, slots } = solution;

  const assoRows = [];
  for (let i = 0; i < 256; i += 16) {
    assoRows.push("\t\t" + asso.slice(i, i + 16).join(", ") + ",");
  }
  const slotKeys = slots.map((k) => (k === null ? `""` : goString(k))).join(", ");
  const slotValues = slots.map((k) => (k === null ? zeroValue : String(entries[k]))).join(", ");
  const terms = positions.map((pos) => `int(${functionName}Asso[${functionName}ByteAt(key, ${pos})])`);

  return `// Code generated by tooling/scripts/perfect_hash_gen.mjs. DO NOT EDIT.

package ${packageName}

var (
	${functionName}Asso = [256]uint8{
${assoRows.join("\n")}
	}

	${functionName}Keys = [${tableSize}]string{
		${slotKeys},
	}

	${functionName}Values = [${tableSize}]${valueType}{
		${slotValues},
	}
)

// ${functionName}ByteAt returns the byte at pos (negative counts from the end), or 0 outside the key.
func ${functionName}ByteAt(key string, pos int) byte {
	if pos < 0 {
		pos += len(key)
	}
	if pos < 0 || pos >= len(key) {
		return 0
	}
	return key[pos]
}

// ${functionName} is a perfect-hash lookup. It always compares the full key.
func ${functionName}(key string) (${valueType}, bool) {
	slot := (len(key) + ${terms.join(" + ")}) & ${mask}
	if ${functionName}Keys[slot] == key && key != "" {
		return ${functionName}Values[slot], true
	}
	return ${zeroValue}, false
}
`;
}

function main(argv) {
  const args = argv.slice(2);
  const specPath = args.find((a) => !a.startsWith("--"));
  const outIndex = args.indexOf("--out");
  const outPath = outIndex >= 0 ? args[outIndex + 1] : null;
  const check = args.includes("--check");
  if (!specPath) {
    console.error("usage: perfect_hash_gen.mjs <spec.json> [--out file.go] [--check]");
    return 2;
  }
  const spec = JSON.parse(readFileSync(specPath, "utf8"));
  const code = generateGoPerfectMap({
    packageName: spec.package,
    functionName: spec.function,
    valueType: spec.valueType,
    zeroValue: spec.zeroValue,
    entries: spec.entries,
  });
  if (check) {
    if (!outPath) {
      console.error("--check needs --out");
      return 2;
    }
    const current = readFileSync(outPath, "utf8");
    if (current !== code) {
      console.error(`[FAIL] ${outPath} is stale; regenerate from ${specPath}`);
      return 1;
    }
    console.log(`[OK] ${outPath} matches ${specPath}`);
    return 0;
  }
  if (outPath) {
    writeFileSync(outPath, code);
  } else {
    process.stdout.write(code);
  }
  return 0;
}

if (process.argv[1] && fileURLToPath(import.meta.url) === process.argv[1]) {
  process.exit(main(process.argv));
}
