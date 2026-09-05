// Package scaffoldassets exposes the application-side assets used to create a
// dependency-based Aginex project.
package scaffoldassets

import "embed"

// ProjectTemplateFS contains the versioned project scaffold bundled with the
// Aginex CLI. Framework implementation packages and local build artifacts are
// intentionally excluded.
//
//go:embed all:.agents/skills
//go:embed .env.example .gitignore AGENTS.md LICENSE NOTICE go.mod go.sum package.json pnpm-lock.yaml pnpm-workspace.yaml playwright.config.ts compose.yaml docs/openapi.json
//go:embed apps/web/app apps/web/components apps/web/e2e apps/web/i18n apps/web/lib apps/web/messages
//go:embed apps/web/biome.json apps/web/global.d.ts apps/web/next-env.d.ts apps/web/next.config.ts apps/web/package.json apps/web/postcss.config.mjs apps/web/tsconfig.json apps/web/vitest.config.ts
var ProjectTemplateFS embed.FS
