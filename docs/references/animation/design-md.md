# DESIGN.md Reference

`DESIGN.md` is the persistent visual-identity contract for coding agents. Use it beside `ui-minimal` theme tokens: the YAML front matter gives exact token values, and the markdown body explains the design intent behind those values.

## File Shape

1. YAML front matter appears first and is wrapped in `---` fences.
2. Markdown prose follows with ordered `##` sections.
3. Token values are normative. Prose explains how to apply them.
4. Unknown valid tokens may be preserved, but broken token references should be fixed before implementation.

## Required Agent Behavior

1. Read `DESIGN.md` before creating or changing product UI.
2. Map `colors`, `typography`, `rounded`, and `spacing` into app-owned `MinimalThemeProvider` overrides.
3. Use `components` entries to set wrapper defaults, not to fork shared primitive structure.
4. Keep section prose aligned with implementation. If the design rationale changes, update `DESIGN.md` in the same change.
5. Preserve unknown headings and tokens unless they are invalid or contradict the app theme.

## Component Token Guidance

Component entries should express styling policy, not copy or behavior.

```yaml
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    typography: "{typography.label-caps}"
    rounded: "{rounded.sm}"
    padding: 7px 16px
    height: 34px
  dropdown-panel:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.primary}"
    rounded: "{rounded.md}"
    width: trigger
    height: viewport-clamped
```

For overlays, include width and height intent. A dropdown token without panel sizing is incomplete because anchored surfaces must know whether they match trigger width, use a minimum readable width, and clamp to viewport height.

## Section Order

Use this order when sections are present:

1. `Overview`
2. `Colors`
3. `Typography`
4. `Layout`
5. `Elevation & Depth`
6. `Shapes`
7. `Components`
8. `Do's and Don'ts`

## Validation

When the CLI is available, run:

```sh
npx @google/design.md lint DESIGN.md
```

Treat errors as blockers. Warnings should be reviewed with the same seriousness as visual regressions: broken contrast, orphaned tokens, or missing typography usually become inconsistent UI.
