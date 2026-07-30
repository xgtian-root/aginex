---
name: add-admin-page
description: Add a production-quality Next.js administration page that uses Aginex API contracts, permission-aware navigation, accessible interactions, responsive layouts, and complete loading/error/empty states. Use for dashboards, reports, settings, detail views, and management screens.
---

# Add Admin Page

## Workflow

1. Reuse API types and the smallest existing layout/component pattern that fits.
2. Declare the required `resource:action` permission in navigation and page actions.
3. Use TanStack Query for server state; avoid duplicating it in global client stores.
4. Implement semantic markup, visible labels, focus states, 44px touch targets, helpful errors, and actionable empty states.
5. Start mobile-first. Convert dense tables into labeled records on narrow screens.
6. Keep accent color rare, use design tokens, animate only transform/opacity, and respect reduced motion.
7. Run typecheck, Biome, tests, and production build.

## Constraints

- Do not add a page that reveals unauthorized navigation or actions.
- Do not use placeholders as labels, color as the only signal, or hover as the only interaction.
- Do not introduce another design system or state library for local convenience.

## Completion Gate

The page works at narrow and wide widths, keyboard focus is visible, every data state is handled, copy names the outcome, and `pnpm check:web && pnpm build:web` pass.
