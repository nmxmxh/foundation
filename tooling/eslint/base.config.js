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
