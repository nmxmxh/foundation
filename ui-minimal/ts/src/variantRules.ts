/**
 * Static style variants, selected by a data attribute instead of a prop.
 *
 * A prop function that returns a whole block — `${({ $tone }) => ...}` — has
 * no single declaration a build-time extractor can turn into a CSS variable.
 * This enumerates the values once, when the styles are built, as plain rules:
 *
 * ```ts
 * ${variantRules("tone", minimalTones, (tone) => `color: ${toneColor(tone)};`)}
 * // &:where([data-minimal-tone="brand"]) { color: ... }   one rule per value
 * ```
 *
 * and the element carries `data-minimal-tone={tone}`.
 *
 * Returns a string: under Linaria (research doc sections 8 and 14.7) it is
 * evaluated at build time and inlined into the extracted stylesheet, and a
 * styled-components template accepts the same string unchanged.
 *
 * `:where()` contributes no specificity, so `.c:where([...])` weighs exactly
 * what `.c` did, and every hover, focus and disabled rule wins or loses just
 * as it did when the block was inlined. The one thing that moves is position:
 * the rule is emitted after its parent's own declarations instead of among
 * them. A variant must therefore not set a property, or a shorthand covering
 * one, that a later declaration of the same rule also sets.
 *
 * Every value of the union is enumerated, so the element must always carry
 * the attribute; booleans are rendered by React as "true"/"false".
 */
export const variantRules = <V extends string | boolean>(
  name: string,
  values: readonly V[],
  rule: (value: V) => string,
): string =>
  values
    .map(
      (value) => `
      &:where([data-minimal-${name}="${String(value)}"]) {
        ${rule(value)}
      }
    `,
    )
    .join("\n");
