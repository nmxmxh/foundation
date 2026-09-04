#!/usr/bin/env node
/**
 * Enforces the render-surface and spacing rules that two source-level audits
 * found had been written down and then not kept.
 *
 * Every rule here exists because the codebase already broke it once. The
 * quality ladder shipped a miss threshold that made demotion unreachable on
 * exactly the devices it protected; `canvasStage` forced a synchronous layout
 * inside the frame its own docstring promised not to; the spacing scale
 * contradicted the practice document that specified it; and nine hand-typed
 * breakpoints disagreed with each other by enough to leave a band of viewport
 * widths rendering a stacked layout inside desktop margins. None of those were
 * catchable by a typechecker or a unit test, and all of them are one grep.
 *
 * Written in Node rather than zsh on purpose: two of the rules need brace
 * matching and one needs arithmetic, and a zsh check that quietly matches
 * nothing is a gate that reports success forever.
 */

import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";

const root = resolve(process.argv[2] ?? ".");
let failed = false;

const ok = (label) => console.log(`[OK] ${label}`);
const fail = (label, ...detail) => {
  console.log(`[FAIL] ${label}`);
  for (const line of detail) if (line) console.log(`  ${line}`);
  failed = true;
};

/** Foundation's own TypeScript, plus the scaffold every generated app starts from. */
const ROOTS = [
  "ui-minimal/ts/src",
  "frontend-kit/ts/src",
  "runtime-sdk/ts/browser-host/src",
  "templates/frontend/src",
];

/**
 * Blank out comments, preserving every byte position and newline.
 *
 * Without this the rules read their own rationale and report it: the paragraph
 * explaining why `gap: 16px` cannot express hierarchy is, to a grep, a raw
 * pixel literal, and the note saying `getBoundingClientRect` no longer runs
 * inside `frame()` names both of the things it is promising not to do.
 */
const stripComments = (text) =>
  text
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, " "))
    .replace(/(^|[^:])\/\/[^\n]*/g, (m, lead) => lead + " ".repeat(m.length - lead.length));

const sources = [];
const walk = (dir) => {
  let entries;
  try {
    entries = readdirSync(dir);
  } catch {
    return;
  }
  for (const entry of entries) {
    if (entry === "node_modules" || entry === "generated") continue;
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) {
      walk(full);
      continue;
    }
    if (!/\.tsx?$/.test(entry)) continue;
    if (/\.(test|bench|profile\.test)\.tsx?$/.test(entry)) continue;
    const text = readFileSync(full, "utf8");
    sources.push({ path: full, rel: relative(root, full), text, code: stripComments(text) });
  }
};
for (const dir of ROOTS) walk(join(root, dir));

if (sources.length === 0) {
  fail("frontend surface sources", "no TypeScript sources found under any of: " + ROOTS.join(", "));
}

const hits = (predicate) => {
  const found = [];
  for (const file of sources) {
    const lines = file.code.split("\n");
    for (let i = 0; i < lines.length; i += 1) {
      if (predicate(lines[i], file)) found.push(`${file.rel}:${i + 1}: ${lines[i].trim()}`);
    }
  }
  return found;
};

const rule = (label, found, remedy) => {
  if (found.length === 0) ok(label);
  else fail(label, ...found.slice(0, 8), found.length > 8 ? `… and ${found.length - 8} more` : "", remedy);
};

/* ── graphics: gpu_practices.md, "Device profile and starting tier" ───── */

/**
 * A layout read inside a frame is a forced synchronous layout, and it is worst
 * during a scroll when layout is already dirty and the browser was about to do
 * it anyway. Scanning the *body* rather than the file, because reading a rect
 * once at construction or from an observer callback is exactly how a stage is
 * supposed to get its position.
 */
const FRAME_BODY = /\b(?:frame|draw|tick|step|render)\s*\(/;
const IN_FRAME_BANNED = /getBoundingClientRect|window\.inner(?:Width|Height)|getComputedStyle|\.offset(?:Top|Left|Width|Height)\b/;

const frameBodyHits = [];
for (const file of sources) {
  const text = file.code;
  for (const match of text.matchAll(new RegExp(FRAME_BODY, "g"))) {
    // Walk from the opening brace of this function to its matching close.
    const open = text.indexOf("{", match.index);
    if (open === -1) continue;
    let depth = 0;
    let end = open;
    for (; end < text.length; end += 1) {
      if (text[end] === "{") depth += 1;
      else if (text[end] === "}") {
        depth -= 1;
        if (depth === 0) break;
      }
    }
    const body = text.slice(open, end);
    // A function that spans most of the file is a module, not a frame.
    if (body.length > 4000) continue;
    const offender = body.match(IN_FRAME_BANNED);
    if (!offender) continue;
    const line = text.slice(0, open + body.indexOf(offender[0])).split("\n").length;
    frameBodyHits.push(`${file.rel}:${line}: ${offender[0]} inside ${match[0]}…)`);
  }
}
rule(
  "no layout read inside a frame",
  frameBodyHits,
  "Serve size from a ResizeObserver, position from the IntersectionObserver entry plus a passive scroll listener, and the viewport from a passive resize listener.",
);

/**
 * `high-performance` requests the discrete adapter — a whole-process GPU switch
 * on macOS, and the driver's highest-power operating point on a phone, which on
 * a thermally constrained device means faster throttling and worse sustained
 * frame times than low-power would have given.
 */
const POWER_ALLOWED = new Set(["runtime-sdk/ts/browser-host/src/webgpuHost.ts"]);
rule(
  "high-performance adapter is reserved for the compute lane",
  hits(
    (line, file) =>
      line.includes('"high-performance"') && !POWER_ALLOWED.has(file.rel.replaceAll("\\", "/")),
  ),
  'A pass that is not the subject of the page requests powerPreference: "low-power" — gpu_practices.md, WebGPU and WGSL rules §7.',
);

/** The ladder's miss threshold must scale with the rung it is judging. */
const ladder = sources.find((file) => file.rel.endsWith("renderSurfaceClient.ts"));
if (!ladder) {
  fail("quality ladder contract", "renderSurfaceClient.ts not found");
} else if (/cadenceMs\s*\+\s*BUDGET_SLACK|BUDGET_SLACK_MS/.test(ladder.code)) {
  fail(
    "quality ladder uses a proportional miss threshold",
    "renderSurfaceClient.ts adds a flat slack to the cadence",
    "A flat slack means a different thing on every rung: cadence + 1000/30 on a 40 Hz rung puts the miss line at 17.1 fps, so a device holding 20 fps records no misses and never demotes.",
  );
} else if (!/\bMISS_FACTOR\b/.test(ladder.code) || !/cadenceMs \* MISS_FACTOR\b/.test(ladder.code)) {
  fail("quality ladder uses a proportional miss threshold", "MISS_FACTOR is not applied to cadenceMs");
} else {
  ok("quality ladder uses a proportional miss threshold");
}

/** A ladder only descends from where it opens, so something must choose. */
const host = sources.find((file) => file.rel.endsWith("browser-host/src/renderSurface.ts"));
if (!host) {
  fail("render surface device prior", "renderSurface.ts not found");
} else {
  /*
   * Scoped to the INIT command specifically.
   *
   * Matching `tier: openingTier` anywhere in the file was not a test: the
   * chosen rung is also named in the READY diagnostics, so deleting it from the
   * command the worker actually reads still left the rule satisfied. A contract
   * check that passes while the contract is broken is worse than none, because
   * it is now the reason nobody looks.
   */
  /*
   * Anchored on `canvas: offscreen`, which appears only in the command that is
   * actually posted. Anchoring on `kind: "INIT"` found the *type declaration*
   * first, whose optional `tier?: number` satisfied the rule while the call
   * site sent nothing.
   */
  const initStart = host.code.indexOf("canvas: offscreen");
  const initBlock = initStart === -1 ? "" : host.code.slice(initStart, host.code.indexOf("},", initStart));
  if (!/\bstartingTierForDevice\b/.test(host.code)) {
    fail(
      "render surface opens on a device prior",
      "renderSurface.ts does not read a device profile",
      "Every device opening at the best rung means every phone discovers it is a phone by janking through the demotion count on first paint.",
    );
  } else if (!/\btier:/.test(initBlock)) {
    fail(
      "render surface opens on a device prior",
      "the INIT command does not carry the rung the host chose",
      "A ladder only descends from where it opens, and the worker cannot read the prior itself: matchMedia does not exist there.",
    );
  } else {
    ok("render surface opens on a device prior");
  }
}

/**
 * The zero-copy state path must stay wired to both halves of the lane.
 *
 * It is opt-in, so nothing fails loudly if a refactor drops it — the surface
 * just goes back to paying a structured clone per frame, which at 16KB is
 * 3 microseconds and 16.3KB of garbage on the main thread, and which no test
 * would notice because the pixels are identical.
 */
const channel = sources.find((file) => file.rel.endsWith("renderStateChannel.ts"));
const client = sources.find((file) => file.rel.endsWith("renderSurfaceClient.ts"));
if (!channel || !host || !client) {
  fail("shared state channel", "renderStateChannel.ts, renderSurface.ts or renderSurfaceClient.ts not found");
} else if (!/\bcreateRenderStateChannel\b/.test(host.code) || !/stateBuffer: stateChannel/.test(host.code)) {
  fail(
    "the host offers a shared state path",
    "renderSurface.ts does not negotiate a state channel or does not send its buffer",
    "Without it every surface pays a structured clone per frame for state that could have crossed as bytes.",
  );
} else if (!/\battachRenderStateReader\b/.test(client.code)) {
  fail(
    "the worker reads the shared state path",
    "renderSurfaceClient.ts does not attach a state reader",
  );
} else if (!/Atomics\.store/.test(channel.code) || !/Atomics\.load/.test(channel.code)) {
  fail(
    "the shared state path is correctly ordered",
    "renderStateChannel.ts publishes or reads without atomics",
    "Plain reads and writes carry no ordering guarantee; the sequence must be stored and loaded atomically or the slot writes can be seen out of order.",
  );
} else {
  ok("shared state channel is wired to both halves of the lane");
}

/**
 * Disposal, which nothing else can catch.
 *
 * GPU resources live outside the JS heap, so a leak is invisible to every
 * measurement in this repository — the heap profile, the allocation profile,
 * the retained-byte figures. A static check that the *contract* still demands
 * release is the only guard available.
 */
if (!client) {
  fail("pass disposal contract", "renderSurfaceClient.ts not found");
} else if (/\bdispose\?:/.test(client.code)) {
  fail(
    "a render pass must declare disposal",
    "RenderSurfacePass.dispose is optional",
    "Optional disposal reads as 'release if convenient', and the leak it permits cannot be measured — GPU memory is not on the JS heap. A pass with nothing to release writes an empty body.",
  );
} else if (!/\bdispose:\s*\(\)\s*=>\s*void/.test(client.code)) {
  fail("a render pass must declare disposal", "RenderSurfacePass has no dispose member");
} else {
  ok("a render pass must declare disposal");
}

/**
 * Prewarming has to stay wired, because losing it is invisible.
 *
 * A surface whose warm path is dropped still draws, still passes every test,
 * and simply takes the canvas-independent half of its build — adapter, device,
 * shader compilation, pipelines — back onto the critical path to first paint.
 * Measured at 91% of the startup chain.
 */
if (!client || !host) {
  fail("prewarm contract", "renderSurfaceClient.ts or renderSurface.ts not found");
} else if (!/\bprewarmRenderSurface\b/.test(host.code) || !/kind: "WARM"/.test(host.code)) {
  fail(
    "the host can prewarm a surface",
    "renderSurface.ts exports no prewarm entry point, or never sends WARM",
  );
} else if (!/case "WARM"/.test(client.code) || !/definition\.warm\b/.test(client.code)) {
  fail(
    "the worker honours a warm request",
    "renderSurfaceClient.ts does not handle WARM or never calls definition.warm",
  );
} else if (!/warming \? await warming : undefined/.test(client.code)) {
  fail(
    "a late INIT waits for an in-flight warm",
    "renderSurfaceClient.ts does not await warming before building",
    "Prewarm-then-mount in the same tick is the ordinary case; restarting the work there would make a fast mount slower than not warming at all.",
  );
} else {
  ok("prewarming is wired and a late INIT waits for it");
}

/**
 * The shared-worker arrangement, which is invisible when it breaks.
 *
 * Three surfaces that each acquire their own device still draw correctly. They
 * just cost three adapters, three pipeline sets and three lots of driver state
 * on a page that asked for one — and nothing in a test or a profile says so.
 */
const sharedWorker = sources.find((file) => file.rel.endsWith("renderSurfaceWorker.ts"));
if (!sharedWorker) {
  fail("shared surface worker", "renderSurfaceWorker.ts not found");
} else if (!/sharing \?\?=/.test(sharedWorker.code)) {
  fail(
    "a shared worker holds one in-flight acquisition",
    "renderSurfaceWorker.ts does not memoise the acquisition promise",
    "Surfaces on one page mount in the same tick; a flag-guarded acquisition is found unset by each of them and requests a device for each of them.",
  );
} else if (!/options\.release\?\.\(/.test(sharedWorker.code) || !/handlers\.size > 0/.test(sharedWorker.code)) {
  fail(
    "a shared worker reference-counts its device",
    "renderSurfaceWorker.ts does not release on the last surface",
    "A shared worker outlives every surface in it, so nothing else will notice when the last one goes.",
  );
} else if (!/pending\.then\(/.test(sharedWorker.code)) {
  fail(
    "a shared worker releases a late acquisition",
    "renderSurfaceWorker.ts drops an in-flight acquisition when the last surface leaves",
    "That loses a device which is about to arrive and which nothing else references — the one path where a shared device leaks unobservably.",
  );
} else {
  ok("a shared worker acquires once, counts references, and releases a late device");
}

const gpuHost = sources.find((file) => file.rel.endsWith("webgpuHost.ts"));
if (!gpuHost) {
  fail("gpu host teardown", "webgpuHost.ts not found");
} else if (!/\n  dispose\(/.test(gpuHost.code)) {
  fail(
    "a gpu host offers a public teardown",
    "webgpuHost.ts has no public dispose()",
    "Releasing on device loss is not teardown: loss is the one case where the driver already took the memory back.",
  );
} else if (!/device\.destroy\?\.\(\)/.test(gpuHost.code) || !/ownsDevice/.test(gpuHost.code)) {
  fail(
    "a gpu host destroys only the device it owns",
    "webgpuHost.ts does not destroy its device, or does not track ownership",
    "A device will routinely be shared with a render surface; destroying one you were handed is worse than leaking one.",
  );
} else {
  ok("a gpu host offers a public teardown and destroys only what it owns");
}

/* ── spacing: styling_design_practices.md §10.3, §10.4, §11 ──────────── */

rule(
  "media queries carry no hand-typed pixel literal",
  hits((line) => /@media\s*\([^)]*(?:max|min)-width:\s*\d/.test(line)),
  "Use from() / until() on a named breakpoint — styling_design_practices.md §10.4.",
);

rule(
  "full-screen surfaces use svh or dvh, never vh",
  hits((line) => /\b\d+vh\b/.test(line)),
  "On mobile 100vh resolves to the *large* viewport — the height with the URL bar retracted — so the surface allocates and renders taller than the screen.",
);

/*
 * Scoped to the two properties the spacing law actually governs: the gap
 * between peers and the space an element claims above itself. Control geometry
 * — `padding: 8px 14px` on a chip — is a different question that the scale does
 * not answer, and `margin: -1px` is a border-overlap trick that no scale should
 * own. Widening this rule to every spacing property would make it a rule about
 * everything, which is a rule nobody can keep.
 */
rule(
  "rhythm values come from the scale",
  hits((line) => /(?:gap|column-gap|row-gap|margin-block-start):\s*-?\d*\.?\d+(?:px|rem|em)/.test(line)),
  "Read theme.space. A layout needing a step that does not exist is a finding about the scale, not a licence for a one-off — styling_design_practices.md §11 rule 6.",
);

/** The package that defines the scale must not depend on its own deprecation. */
rule(
  "foundation sources read theme.space, not the deprecated alias",
  hits(
    (line, file) =>
      file.rel.includes("ui-minimal") &&
      // theme.tsx declares the alias and must name it to emit its variables.
      !file.rel.endsWith("theme.tsx") &&
      /theme\.spacing\b/.test(line),
  ),
  "theme.spacing is the previous scale's names kept for downstream compatibility; ui-minimal itself reads theme.space.",
);

/**
 * The rule that encodes the preference: a document-shaped stack is flow layout
 * with margins, because margins do not collapse in flex and collapsing is what
 * lets a nested claim resolve against its parent instead of adding to it.
 *
 * Allowlisted where the column genuinely needs flex *and* its children are
 * uniform peers. Each entry must carry a comment saying which rule it is under.
 */
const COLUMN_GAP_ALLOWED = new Set(["ui-minimal/ts/src/primitives.tsx:Sidebar"]);
const columnGapHits = [];
for (const file of sources) {
  for (const match of file.code.matchAll(/flex-direction:\s*column\s*;/g)) {
    const after = file.code.slice(match.index);
    const close = after.indexOf("`");
    const rule_ = close === -1 ? after.slice(0, 600) : after.slice(0, close);
    if (!/\n\s*gap:/.test(rule_)) continue;
    const line = file.code.slice(0, match.index).split("\n").length;
    // Take the styled-component name from the nearest declaration above.
    const before = file.code.slice(0, match.index).split("\n").reverse();
    const named = before.find((l) => /^\s*(?:const\s+)?([A-Z][A-Za-z0-9]*)\s*[:=]\s*styled/.test(l));
    const name = named?.match(/([A-Z][A-Za-z0-9]*)\s*[:=]\s*styled/)?.[1] ?? "?";
    const key = `${file.rel.replaceAll("\\", "/")}:${name}`;
    const documented = /styling_design_practices\.md §11/.test(
      file.text.slice(match.index - 500, match.index + 500),
    );
    if (COLUMN_GAP_ALLOWED.has(key) && documented) continue;
    columnGapHits.push(`${file.rel}:${line}: gap on flex column ${name}`);
  }
}
rule(
  "document-shaped stacks use flow layout, not a flex column with a gap",
  columnGapHits,
  "Use MinimalStack, or allowlist the component in this script with a comment citing styling_design_practices.md §11 rule 3.",
);

/** Two steps a reader cannot tell apart are not two steps. */
const theme = sources.find((file) => file.rel.endsWith("ui-minimal/ts/src/theme.tsx"));
const scale = theme?.code.match(/const space: MinimalSpaceTheme = \{([\s\S]*?)\n\};/)?.[1];
if (!scale) {
  fail("spacing scale ratio", "could not read the space scale out of ui-minimal/ts/src/theme.tsx");
} else {
  const steps = [...scale.matchAll(/"?([0-9a-z]+)"?:\s*"(\d+(?:\.\d+)?)px"/g)].map((m) => ({
    name: m[1],
    px: Number(m[2]),
  }));
  const tight = [];
  for (let i = 1; i < steps.length; i += 1) {
    const ratio = steps[i].px / steps[i - 1].px;
    if (ratio < 1.5) tight.push(`${steps[i - 1].name} -> ${steps[i].name} is ${ratio.toFixed(2)}x`);
  }
  if (steps.length < 6) fail("spacing scale ratio", `only parsed ${steps.length} steps`);
  else if (tight.length > 0) {
    fail(
      "every spacing step is distinguishable from its neighbours",
      ...tight,
      "A reader parses a step as deliberate around 1.6x; below about 1.5x it reads as drift, and six tokens producing four distinguishable values is what made every layout look uniform.",
    );
  } else ok(`every spacing step is distinguishable from its neighbours (${steps.length} steps)`);
}

console.log(failed ? "\nfrontend surface practices FAILED" : "\nfrontend surface practices passed");
process.exit(failed ? 1 : 0);
