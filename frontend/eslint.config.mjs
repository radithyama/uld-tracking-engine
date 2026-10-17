import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";
import security from "eslint-plugin-security";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  // Common Node/JS security pitfalls: unsafe regexes, eval, dynamic require,
  // non-literal fs paths, timing-unsafe comparisons...
  security.configs.recommended,
  {
    rules: {
      // XSS: raw HTML and script URLs bypass React's escaping. The one
      // legitimate use (the theme bootstrap script) is allow-listed inline.
      "react/no-danger": "error",
      "react/jsx-no-script-url": "error",
      "react/jsx-no-target-blank": ["error", { allowReferrer: false }],
      "no-eval": "error",
      "no-implied-eval": "error",
      "no-new-func": "error",
      // Flags every obj[key]; too noisy on typed lookups like ROLE_INFO[role].
      "security/detect-object-injection": "off",
    },
  },
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
  ]),
]);

export default eslintConfig;
