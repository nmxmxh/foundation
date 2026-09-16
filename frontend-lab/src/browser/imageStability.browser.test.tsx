import { cleanup, render } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { MinimalGlobalStyles, MinimalImage, MinimalThemeProvider } from "@ovasabi/ui-minimal";

/*
 * P6 stability gate: an image must not move what is below it when its pixels
 * arrive. Measured as geometry, in Chromium: the top of the paragraph under
 * the image before the image has loaded, and after it has.
 */

const COLUMN = 400;

/** A fresh 800×400 image each call, so nothing is cached and load is always asynchronous. */
const imageUrl = async () => {
  const canvas = new OffscreenCanvas(800, 400);
  const context = canvas.getContext("2d")!;
  context.fillStyle = "#2563eb";
  context.fillRect(0, 0, 800, 400);
  return URL.createObjectURL(await canvas.convertToBlob({ type: "image/png" }));
};

const loaded = (image: HTMLImageElement) =>
  image.complete && image.naturalWidth > 0
    ? Promise.resolve()
    : new Promise<void>((resolve, reject) => {
        image.addEventListener("load", () => resolve(), { once: true });
        image.addEventListener("error", () => reject(new Error("image failed")), { once: true });
      });

const frames = (count = 2) =>
  new Promise<void>((resolve) => {
    const tick = (left: number) => (left === 0 ? resolve() : requestAnimationFrame(() => tick(left - 1)));
    tick(count);
  });

const shiftOf = async (image: (src: string) => ReactNode) => {
  const src = await imageUrl();
  const { container, getByText } = render(
    <MinimalThemeProvider>
      <MinimalGlobalStyles />
      <div style={{ width: COLUMN }}>
        {image(src)}
        <p style={{ margin: 0 }}>below</p>
      </div>
    </MinimalThemeProvider>,
  );
  const below = getByText("below");
  const img = container.querySelector("img")!;
  const before = below.getBoundingClientRect().top;
  const wasLoaded = img.complete && img.naturalWidth > 0;
  await loaded(img);
  await frames();
  const after = below.getBoundingClientRect().top;
  URL.revokeObjectURL(src);
  return { shift: Math.round(after - before), wasLoaded, img };
};

describe("image layout stability (chromium)", () => {
  afterEach(cleanup);

  it("control: an unsized <img> moves the content below it by the image's height", async () => {
    const { shift, wasLoaded } = await shiftOf((src) => <img src={src} alt="" style={{ display: "block", maxWidth: "100%" }} />);
    expect(wasLoaded, "the image was already loaded at first measurement; the control proves nothing").toBe(false);
    expect(shift).toBe(COLUMN / 2);
  });

  it("MinimalImage with intrinsic width and height reserves the scaled box: zero shift", async () => {
    const { shift, wasLoaded, img } = await shiftOf((src) => <MinimalImage src={src} alt="" width={800} height={400} />);
    expect(wasLoaded).toBe(false);
    expect(shift).toBe(0);
    expect(img.getBoundingClientRect().height).toBe(COLUMN / 2);
  });

  it("MinimalImage with an aspect ratio fills its column and reserves the box: zero shift", async () => {
    const { shift, img } = await shiftOf((src) => <MinimalImage src={src} alt="" aspectRatio="16 / 9" fit="cover" />);
    expect(shift).toBe(0);
    expect(Math.round(img.getBoundingClientRect().height)).toBe(Math.round((COLUMN * 9) / 16));
  });

  it("defaults to lazy, async decode; priority loads eagerly at high fetch priority", async () => {
    const src = await imageUrl();
    const { container } = render(
      <>
        <MinimalImage src={src} alt="" width={8} height={4} />
        <MinimalImage src={src} alt="" width={8} height={4} priority />
      </>,
    );
    const [plain, hero] = Array.from(container.querySelectorAll("img"));
    expect([plain.loading, plain.decoding, plain.getAttribute("fetchpriority")]).toEqual(["lazy", "async", null]);
    expect([hero.loading, hero.getAttribute("fetchpriority")]).toEqual(["eager", "high"]);
    URL.revokeObjectURL(src);
  });

  it("reveal holds the image transparent until it has decoded", async () => {
    const src = await imageUrl();
    const { container } = render(
      <MinimalThemeProvider>
        <MinimalGlobalStyles />
        <MinimalImage src={src} alt="" width={800} height={400} reveal />
      </MinimalThemeProvider>,
    );
    const img = container.querySelector("img")!;
    expect(img.getAttribute("data-minimal-reveal")).toBe("pending");
    expect(getComputedStyle(img).opacity).toBe("0");
    await loaded(img);
    await img.decode();
    await frames(3);
    expect(img.getAttribute("data-minimal-reveal")).toBe("done");
    // The fade is a CSS transition on opacity (composited); let it finish.
    await Promise.all(img.getAnimations().map((animation) => animation.finished));
    expect(getComputedStyle(img).opacity).toBe("1");
    URL.revokeObjectURL(src);
  });
});
