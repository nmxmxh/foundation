import { test } from "node:test";
import assert from "node:assert/strict";
import { findPerfectHash, generateGoPerfectMap } from "../tooling/scripts/perfect_hash_gen.mjs";

const byteAt = (bytes, pos) => {
  const i = pos >= 0 ? pos : bytes.length + pos;
  return i >= 0 && i < bytes.length ? bytes[i] : 0;
};

const lookup = (solution, key) => {
  const bytes = Buffer.from(key, "utf8");
  let sum = bytes.length;
  for (const pos of solution.positions) sum += solution.asso[byteAt(bytes, pos)];
  return solution.slots[sum & solution.mask] === key;
};

test("every key resolves, including keys shorter than the probe positions", () => {
  const keys = ["a", "ab", "abc", "accept", "x", "authorization", "zz"];
  const solution = findPerfectHash(keys);
  for (const key of keys) assert.ok(lookup(solution, key), `key ${key} must resolve`);
  assert.ok(!lookup(solution, "abd"));
});

test("non-ASCII keys hash by UTF-8 bytes, as Go does", () => {
  const keys = ["café", "cafe", "naïve", "über", "日本"];
  const solution = findPerfectHash(keys);
  for (const key of keys) assert.ok(lookup(solution, key), `key ${key} must resolve`);
  for (const value of solution.asso) assert.ok(value >= 0 && value <= 255);
});

test("keys are emitted as escaped Go string literals", () => {
  const code = generateGoPerfectMap({
    packageName: "p",
    functionName: "lookupX",
    valueType: "int",
    entries: { 'quo"te': "1", "back\\slash": "2", "new\nline": "3" },
  });
  assert.ok(code.includes('"quo\\"te"'));
  assert.ok(code.includes('"back\\\\slash"'));
  assert.ok(code.includes('"new\\nline"'));
  assert.ok(!code.includes('"quo"te"'), "raw quote would inject Go code");
});

test("rejects empty keys and non-identifier names", () => {
  assert.throws(() => findPerfectHash(["", "a"]), /non-empty/);
  assert.throws(
    () => generateGoPerfectMap({ packageName: "p; os.Exit(1)", functionName: "f", valueType: "int", entries: { a: "1" } }),
    /Go identifier/,
  );
});

test("search is bounded and fails loudly", () => {
  assert.throws(() => findPerfectHash(["aa", "ab", "ba", "bb", "abc", "acb"], { maxSteps: 1 }), /no perfect hash/);
});
