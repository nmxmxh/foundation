import js from "@eslint/js";
import reactHooks from "eslint-plugin-react-hooks";
import simpleImportSort from "eslint-plugin-simple-import-sort";
import tseslint from "typescript-eslint";
import unusedImports from "eslint-plugin-unused-imports";

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    plugins: {
      "react-hooks": reactHooks,
      "simple-import-sort": simpleImportSort,
      "unused-imports": unusedImports,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "@typescript-eslint/consistent-type-imports": "error",
      "@typescript-eslint/no-unused-vars": "off",
      "unused-imports/no-unused-imports": "error",
      "unused-imports/no-unused-vars": [
        "warn",
        {
          vars: "all",
          varsIgnorePattern: "^_",
          args: "after-used",
          argsIgnorePattern: "^_",
        },
      ],
      "simple-import-sort/imports": "error",
      "simple-import-sort/exports": "error",
      "no-restricted-syntax": [
        "error",
        {
          selector: "NewExpression[callee.name='MutationObserver']",
          message:
            "Prefer explicit state flow, ResizeObserver, or IntersectionObserver. MutationObserver is exception-only and must be isolated behind an audited adapter with cleanup."
        },
        {
          selector: "NewExpression[callee.property.name='MutationObserver']",
          message:
            "Prefer explicit state flow, ResizeObserver, or IntersectionObserver. MutationObserver is exception-only and must be isolated behind an audited adapter with cleanup."
        },
        {
          selector: "CallExpression[callee.object.name='Atomics'][callee.property.name='wait']",
          message:
            "Main-thread/runtime TypeScript must not use blocking Atomics.wait. Use workers, Atomics.waitAsync, transferable buffers, or message fallback."
        },
        {
          selector: "NewExpression[callee.name='WebSocket']",
          message:
            "Route browser/backend messages through @ovasabi/runtime-transport instead of app-local raw WebSocket construction."
        },
        {
          selector: "NewExpression[callee.property.name='WebSocket']",
          message:
            "Route browser/backend messages through @ovasabi/runtime-transport instead of app-local raw WebSocket construction."
        }
      ],
      "no-restricted-properties": [
        "error",
        {
          object: "window",
          property: "__OVASABI_RUNTIME_TRANSPORT",
          message:
            "Use @ovasabi/runtime-transport or @ovasabi/frontend-kit helpers; raw globals are compatibility shims only."
        },
        {
          object: "globalThis",
          property: "__OVASABI_RUNTIME_TRANSPORT",
          message:
            "Use @ovasabi/runtime-transport or @ovasabi/frontend-kit helpers; raw globals are compatibility shims only."
        }
      ],
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["*foundation/ui-minimal/ts/src*", "*foundation/runtime-transport/ts/src*", "*foundation/frontend-kit/ts/src*"],
              message:
                "Consume foundation communication/UI packages through @ovasabi/* package boundaries, not raw source aliases."
            }
          ]
        }
      ],
      complexity: ["error", 20],
      "max-lines-per-function": [
        "error",
        {
          max: 120,
          skipBlankLines: true,
          skipComments: true,
        },
      ],
    },
  }
);
