# Web internationalization

Aginex ships its administration UI in English (`en`) and Simplified Chinese
(`zh-CN`). English remains the deterministic fallback. Locale selection changes
presentation only: route paths, API payloads, permission identifiers, Problem
codes, audit actions, and user-entered data remain language-neutral or verbatim.

## Request and persistence contract

The Next.js request locale is resolved in this order:

1. An exact, allowlisted `aginex_locale` cookie value.
2. The request `Accept-Language` header, including quality weights and supported
   English or Simplified-Chinese variants.
3. English.

Routes are intentionally not locale-prefixed. A language switch writes the
non-sensitive preference cookie, updates the document `lang` and `dir`, and
refreshes Server Components. This keeps metadata, server rendering, Client
Components, runtime guards, and safe redirects on one locale without changing
URLs.

Request configuration lives in `apps/web/i18n/request.ts`; the finite locale
contract and negotiation logic live in `apps/web/i18n/config.ts`. Dates use UTC
until Aginex gains a persisted user timezone. Product prices remain explicitly
USD; a locale changes formatting, not business currency.

## Message catalogs

Repository-owned catalogs live at:

- `apps/web/messages/en.json`
- `apps/web/messages/zh-CN.json`

The English catalog defines the TypeScript message shape through
`apps/web/global.d.ts`. Catalog tests require exact recursive key parity,
non-empty messages, matching ICU arguments, and valid ICU syntax. Use complete
ICU messages for interpolation and plurals; do not assemble sentences from
translated fragments.

Stable API `problem.code` values map to localized presentation in
`apps/web/lib/problem-message.ts`. Transport code keeps the raw RFC Problem
envelope for diagnostics, while UI fallbacks never default to rendering an
untranslated `problem.detail`.

Backend-owned role descriptions and audit summaries are currently stored as
English content. Fully localizing them requires a future structured message-key
and parameter contract; adding translated database columns is not the intended
design.

## Add another locale

1. Add the BCP 47 tag to `locales` and its direction to `localeDirections` in
   `apps/web/i18n/config.ts`.
2. Add explicit normalization rules only for language ranges Aginex intends to
   support.
3. Copy the English catalog to `apps/web/messages/<locale>.json` and translate
   every value without changing keys or ICU argument names.
4. Add the catalog loader in `apps/web/i18n/request.ts` and an option in
   `apps/web/components/locale-switcher.tsx`.
5. If the locale is right-to-left, verify icons and all logical CSS properties
   with `dir="rtl"`.
6. Run `pnpm check:web`, `pnpm test:web`, `pnpm build:web`, and the Playwright
   language-persistence smoke test.
