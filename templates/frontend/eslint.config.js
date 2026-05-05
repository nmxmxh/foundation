import js from '@eslint/js'
import globals from 'globals'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import tseslint from 'typescript-eslint'

export default tseslint.config(
  {
    ignores: [
      'coverage/**',
      'dist/**',
      'foundation/**',
      'src/types/protos/**',
    ],
  },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2020,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
      'react-refresh': reactRefresh,
    },
    rules: {
      ...reactHooks.configs.recommended.rules,
      'react-refresh/only-export-components': [
        'warn',
        { allowConstantExport: true },
      ],
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_' },
      ],
      'no-restricted-syntax': [
        'warn',
        {
          selector: "NewExpression[callee.name='MutationObserver']",
          message:
            'Prefer explicit React/store flow, ResizeObserver, or IntersectionObserver. MutationObserver needs a narrow adapter with cleanup.',
        },
        {
          selector: "NewExpression[callee.property.name='MutationObserver']",
          message:
            'Prefer explicit React/store flow, ResizeObserver, or IntersectionObserver. MutationObserver needs a narrow adapter with cleanup.',
        },
        {
          selector: "CallExpression[callee.object.name='Atomics'][callee.property.name='wait']",
          message:
            'Do not block browser/runtime code with Atomics.wait. Use workers, Atomics.waitAsync, transferable buffers, or message fallback.',
        },
        {
          selector: "NewExpression[callee.name='WebSocket']",
          message:
            'Use @ovasabi/runtime-transport for app/backend realtime communication unless this is an audited adapter.',
        },
      ],
      'no-restricted-imports': [
        'error',
        {
          patterns: [
            {
              group: [
                '*foundation/ui-minimal/ts/src*',
                '*foundation/runtime-transport/ts/src*',
                '*foundation/frontend-kit/ts/src*',
              ],
              message:
                'Consume foundation packages through @ovasabi/* package dependencies, not raw source imports.',
            },
          ],
        },
      ],
      'no-restricted-properties': [
        'warn',
        {
          object: 'window',
          property: '__OVASABI_RUNTIME_TRANSPORT',
          message:
            'Use @ovasabi/runtime-transport or @ovasabi/frontend-kit helpers; raw globals are compatibility shims only.',
        },
        {
          object: 'globalThis',
          property: '__OVASABI_RUNTIME_TRANSPORT',
          message:
            'Use @ovasabi/runtime-transport or @ovasabi/frontend-kit helpers; raw globals are compatibility shims only.',
        },
      ],
      complexity: ['warn', 24],
      'max-lines-per-function': [
        'warn',
        {
          max: 160,
          skipBlankLines: true,
          skipComments: true,
        },
      ],
    },
  }
)
