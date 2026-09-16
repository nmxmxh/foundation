/**
 * Lab surfaces on the real GPU — the worker half of the `gpu` lane.
 *
 * Built from the SDK exactly as a project builds its own: one
 * `createRenderSurfaceWorker` negotiates one device for every WebGPU surface in
 * the worker, and a WebGL2 surface is served beside it with
 * `serveRenderSurface`. The passes are deliberately synthetic: a full-screen
 * fragment loop whose cost is `iterations × pixels`, so a test can dial GPU load
 * up and down and watch what the loop and the ladder do about it.
 *
 * Everything the lab needs to see goes out on `LAB_*` messages under the
 * surface name `__lab`, which no host listens for.
 */
import {
  createRenderSurfaceWorker,
  serveRenderSurface,
  type RenderSurfaceFrame,
  type RenderSurfacePass,
} from "@ovasabi/runtime-browser";

/* Minimal structural WebGPU types: the lab has no @webgpu/types dependency. */
type Gpu = any;

export type LoadState = {
  /** Multiplies the rung's `detail` into fragment iterations per pixel. */
  load?: number;
  /** Time each frame's GPU work with `onSubmittedWorkDone`. */
  timeGpu?: boolean;
  /** Give the loop `settled`, so it keeps one frame in flight. Default on. */
  backpressure?: boolean;
};

type SurfaceStats = {
  lane: string;
  builtAtMs: number | null;
  firstFrameAtMs: number | null;
  draws: number;
  /** Achieved gap between frames, as the loop's ladder sees it. */
  gaps: number[];
  /** CPU time inside `draw`. */
  drawMs: number[];
  /** Submit to queue-drained, per frame, when `timeGpu` is set. */
  gpuMs: number[];
  /** Frames submitted whose GPU work had not finished when the next was drawn. */
  inFlightAtDraw: number[];
  tiers: number[];
  pixels: number[];
  disposes: number;
};

const now = () => performance.timeOrigin + performance.now();
const counters = { acquires: 0, releases: 0, lost: [] as string[] };
const stats = new Map<string, SurfaceStats>();

const statsFor = (surface: string, lane: string): SurfaceStats => {
  let entry = stats.get(surface);
  if (!entry) {
    entry = { lane, builtAtMs: null, firstFrameAtMs: null, draws: 0, gaps: [], drawMs: [], gpuMs: [], inFlightAtDraw: [], tiers: [], pixels: [], disposes: 0 };
    stats.set(surface, entry);
  }
  return entry;
};

const record = (entry: SurfaceStats, frame: RenderSurfaceFrame, drawMs: number) => {
  if (entry.firstFrameAtMs === null) entry.firstFrameAtMs = now();
  entry.draws += 1;
  // Bounded: a long run must not become the allocation it is measuring.
  if (entry.gaps.length < 4000) {
    entry.gaps.push(frame.delta);
    entry.drawMs.push(drawMs);
    entry.tiers.push(frame.tier);
    entry.pixels.push(frame.width * frame.height);
  }
};

/* ── WebGPU ─────────────────────────────────────────────────────────── */

const WGSL = /* wgsl */ `
struct U { size: vec2f, time: f32, iterations: f32 };
@group(0) @binding(0) var<uniform> u: U;
@vertex fn vs(@builtin(vertex_index) i: u32) -> @builtin(position) vec4f {
  var p = array<vec2f, 3>(vec2f(-1.0, -3.0), vec2f(3.0, 1.0), vec2f(-1.0, 1.0));
  return vec4f(p[i], 0.0, 1.0);
}
@fragment fn fs(@builtin(position) pos: vec4f) -> @location(0) vec4f {
  let c = (pos.xy / u.size) * 3.0 - vec2f(2.0, 1.5) + vec2f(sin(u.time * 0.001) * 0.02, 0.0);
  var z = c;
  var acc = 0.0;
  // Fixed cost: no early exit, so load is exactly iterations × pixels.
  for (var k = 0; k < i32(u.iterations); k++) {
    z = vec2f(z.x * z.x - z.y * z.y, 2.0 * z.x * z.y) + c;
    z = select(z, vec2f(0.0), dot(z, z) > 64.0);
    acc += dot(z, z);
  }
  return vec4f(fract(acc * 0.001), fract(u.time * 0.0001), 0.5, 1.0);
}`;

const buildWebGpuPass = async (canvas: OffscreenCanvas, device: Gpu, surface: string): Promise<RenderSurfacePass<LoadState>> => {
  const entry = statsFor(surface, "webgpu");
  const context = (canvas as Gpu).getContext("webgpu");
  const format = (navigator as Gpu).gpu.getPreferredCanvasFormat();
  context.configure({ device, format, alphaMode: "opaque" });
  const module = device.createShaderModule({ code: WGSL });
  const pipeline = await device.createRenderPipelineAsync({
    layout: "auto",
    vertex: { module, entryPoint: "vs" },
    fragment: { module, entryPoint: "fs", targets: [{ format }] },
    primitive: { topology: "triangle-list" },
  });
  const uniforms = new Float32Array(4);
  const uniformBuffer = device.createBuffer({ size: 16, usage: 0x40 | 0x08 /* UNIFORM | COPY_DST */ });
  const bindGroup = device.createBindGroup({ layout: pipeline.getBindGroupLayout(0), entries: [{ binding: 0, resource: { buffer: uniformBuffer } }] });
  const attachment = { view: null as Gpu, loadOp: "clear", storeOp: "store", clearValue: { r: 0, g: 0, b: 0, a: 1 } };
  const passDescriptor = { colorAttachments: [attachment] };
  let inFlight = 0;
  let backpressure = true;
  const settledNow = Promise.resolve();
  entry.builtAtMs = now();

  return {
    lane: "webgpu",
    // Off is the control: the loop is told every frame is done at once.
    settled: () => (backpressure ? device.queue.onSubmittedWorkDone() : settledNow),
    resize(width, height) {
      canvas.width = width;
      canvas.height = height;
    },
    draw(state, frame) {
      const started = performance.now();
      backpressure = state?.backpressure ?? true;
      uniforms[0] = frame.width;
      uniforms[1] = frame.height;
      uniforms[2] = frame.elapsed;
      uniforms[3] = Math.max(1, Math.round(frame.detail * (state?.load ?? 1)));
      device.queue.writeBuffer(uniformBuffer, 0, uniforms);
      attachment.view = context.getCurrentTexture().createView();
      const encoder = device.createCommandEncoder();
      const pass = encoder.beginRenderPass(passDescriptor);
      pass.setPipeline(pipeline);
      pass.setBindGroup(0, bindGroup);
      pass.draw(3);
      pass.end();
      device.queue.submit([encoder.finish()]);
      attachment.view = null;
      if (entry.inFlightAtDraw.length < 4000) entry.inFlightAtDraw.push(inFlight);
      if (state?.timeGpu) {
        inFlight += 1;
        const submitted = performance.now();
        void device.queue.onSubmittedWorkDone().then(() => {
          inFlight -= 1;
          if (entry.gpuMs.length < 4000) entry.gpuMs.push(performance.now() - submitted);
        });
      }
      record(entry, frame, performance.now() - started);
    },
    dispose() {
      entry.disposes += 1;
      uniformBuffer.destroy();
      context.unconfigure();
    },
  };
};

const gpuWorker = createRenderSurfaceWorker<Gpu>({
  async acquire() {
    counters.acquires += 1;
    const adapter = await (navigator as Gpu).gpu?.requestAdapter({ powerPreference: "low-power" });
    if (!adapter) throw new Error("no WebGPU adapter");
    const device = await adapter.requestDevice();
    void device.lost.then((info: Gpu) => counters.lost.push(String(info.reason)));
    return device;
  },
  release(device) {
    counters.releases += 1;
    device.destroy();
  },
  // Shortened from the 10 s default so a test can watch the release happen.
  releaseWhenIdleMs: 1000,
});

for (const surface of ["gpu-a", "gpu-b", "gpu-c"]) {
  gpuWorker.serve<LoadState>(surface, {
    // Per-surface canvas-independent work on the shared device: the pipeline.
    warm: async (device) => {
      const module = device.createShaderModule({ code: WGSL });
      await device.createRenderPipelineAsync({
        layout: "auto",
        vertex: { module, entryPoint: "vs" },
        fragment: { module, entryPoint: "fs", targets: [{ format: (navigator as Gpu).gpu.getPreferredCanvasFormat() }] },
      });
    },
    build: (canvas, device) => buildWebGpuPass(canvas, device, surface),
  });
}

/* ── WebGL2 ─────────────────────────────────────────────────────────── */

const GLSL_VS = `#version 300 es
void main() {
  vec2 p[3] = vec2[3](vec2(-1.0, -3.0), vec2(3.0, 1.0), vec2(-1.0, 1.0));
  gl_Position = vec4(p[gl_VertexID], 0.0, 1.0);
}`;
const GLSL_FS = `#version 300 es
precision highp float;
uniform vec4 u; // size.xy, time, iterations
out vec4 color;
void main() {
  vec2 c = (gl_FragCoord.xy / u.xy) * 3.0 - vec2(2.0, 1.5);
  vec2 z = c;
  float acc = 0.0;
  for (int k = 0; k < 100000; k++) {
    if (float(k) >= u.w) break;
    z = vec2(z.x * z.x - z.y * z.y, 2.0 * z.x * z.y) + c;
    z = dot(z, z) > 64.0 ? vec2(0.0) : z;
    acc += dot(z, z);
  }
  color = vec4(fract(acc * 0.001), fract(u.z * 0.0001), 0.5, 1.0);
}`;

serveRenderSurface<LoadState>("gl-a", {
  build(canvas) {
    const gl = canvas.getContext("webgl2", { antialias: false, alpha: false }) as WebGL2RenderingContext | null;
    if (!gl) return null;
    const entry = statsFor("gl-a", "webgl2");
    const compile = (type: number, source: string) => {
      const shader = gl.createShader(type)!;
      gl.shaderSource(shader, source);
      gl.compileShader(shader);
      return shader;
    };
    const program = gl.createProgram()!;
    const vs = compile(gl.VERTEX_SHADER, GLSL_VS);
    const fs = compile(gl.FRAGMENT_SHADER, GLSL_FS);
    gl.attachShader(program, vs);
    gl.attachShader(program, fs);
    gl.linkProgram(program);
    if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(program) ?? "link failed");
    if (!gl.getShaderParameter(fs, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(fs) ?? "fragment compile failed");
    const location = gl.getUniformLocation(program, "u");
    const vao = gl.createVertexArray();
    let pendingSync: WebGLSync | null = null;
    let syncStartedAt = 0;
    entry.builtAtMs = now();
    return {
      lane: "webgl2",
      resize(width, height) {
        canvas.width = width;
        canvas.height = height;
        gl.viewport(0, 0, width, height);
      },
      draw(state, frame) {
        const started = performance.now();
        gl.useProgram(program);
        gl.bindVertexArray(vao);
        gl.uniform4f(location, frame.width, frame.height, frame.elapsed, Math.max(1, Math.round(frame.detail * (state?.load ?? 1))));
        gl.drawArrays(gl.TRIANGLES, 0, 3);
        if (state?.timeGpu && pendingSync === null) {
          /*
           * A fence, polled without blocking. `gl.finish()` read ~0 ms at 40×
           * load on ANGLE/Metal (2026-09-15), so it is not a GPU barrier there;
           * a sync object reports when the driver actually got to this point.
           */
          pendingSync = gl.fenceSync(gl.SYNC_OBJECT_TYPE, 0);
          syncStartedAt = performance.now();
          const poll = () => {
            if (!pendingSync) return;
            if (gl.getSyncParameter(pendingSync, gl.SYNC_STATUS) === gl.SIGNALED) {
              if (entry.gpuMs.length < 4000) entry.gpuMs.push(performance.now() - syncStartedAt);
              gl.deleteSync(pendingSync);
              pendingSync = null;
            } else setTimeout(poll, 1);
          };
          setTimeout(poll, 0);
        }
        record(entry, frame, performance.now() - started);
      },
      dispose() {
        entry.disposes += 1;
        gl.deleteVertexArray(vao);
        gl.deleteProgram(program);
        gl.deleteShader(vs);
        gl.deleteShader(fs);
        gl.getExtension("WEBGL_lose_context")?.loseContext();
      },
    };
  },
});

/* ── lab control ────────────────────────────────────────────────────── */

self.addEventListener("message", (event: MessageEvent) => {
  const message = event.data as { kind?: string } | undefined;
  if (message?.kind === "LAB_QUERY") {
    (self as unknown as Worker).postMessage({
      kind: "LAB_STATS",
      surface: "__lab",
      counters: { ...counters, lost: [...counters.lost], served: gpuWorker.size, acquired: gpuWorker.acquired },
      stats: Object.fromEntries(stats),
    });
  }
  if (message?.kind === "LAB_RESET") stats.clear();
});
