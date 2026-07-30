# Aginex v1 产品需求规划

> 状态：Draft  
> 目标版本：v1.0  
> 更新日期：2026-07-24  
> 许可证：Apache-2.0

## 1. 产品摘要

Aginex 是一个以 Go、Gin 和 Next.js 为核心的开源管理后台框架。它首先是
一个正常、透明、可维护的工程框架，同时通过标准化 Agent Skills、明确的
模块边界、生成工具和自动验证，让 Coding Agent 能够可靠地完成二次开发。

Aginex 不以“后台中内置 AI 聊天框”为 v1 卖点。其核心价值是缩短从业务
需求到经过测试的数据库迁移、API、权限和管理页面之间的交付路径。

## 2. 市场与差异化

### 2.1 参考项目

| 类别 | 代表项目 | 市场优势 | Aginex 的机会 |
|---|---|---|---|
| Go 全家桶后台 | [gin-vue-admin](https://github.com/flipped-aurora/gin-vue-admin)、[go-admin](https://github.com/go-admin-team/go-admin) | CRUD、RBAC 和运维功能完整 | 提供现代 React/Next.js、开放许可和可验证的跨 Agent 工作流 |
| React Headless Admin | [Refine](https://github.com/refinedev/refine)、[React Admin](https://github.com/marmelab/react-admin) | React 数据和组件抽象成熟 | 提供深度集成的 Go 后端、迁移和权限契约 |
| 低代码内部工具 | [Appsmith](https://github.com/appsmithorg/appsmith) | 可视化搭建和数据连接快速 | 保持源码所有权、复杂业务可扩展性和标准工程流程 |
| Agentic Admin | [AdminForth](https://github.com/devforth/adminforth) | 运行时 Agent 可操作业务数据 | 聚焦 Coding Agent 如何高质量开发项目 |

### 2.2 核心差异

1. 遵循 [Agent Skills 开放规范](https://agentskills.io/specification)，
   不绑定特定 Coding Agent。
2. 数据库、Go API、OpenAPI、TypeScript 客户端、权限与页面之间具有
   机器可验证的契约。
3. CLI 只执行确定性工作：初始化、生成骨架、诊断、检查和安装 Skills。
4. 使用固定 eval 任务衡量 Agent 的真实开发成功率，而不是只提供提示词。
5. 每个官方功能同时交付实现、测试、文档和对应 Skill。

## 3. 用户与目标

### 3.1 目标用户

- 需要快速交付内部系统的中小型开发团队。
- 希望以 Go 作为业务后端、React 作为管理端的独立开发者。
- 使用 Codex、Claude Code、GitHub Copilot、Cursor 等 Coding Agent 的团队。
- 需要保留源码所有权和自托管能力的交付团队。

### 3.2 v1 成功指标

- 新用户在十分钟内完成项目初始化、迁移、登录和首个 CRUD 页面。
- Agent 使用官方 Skill 添加标准资源后，至少 80% 的基准运行无需人工修复
  即可通过全部检查。
- PostgreSQL、MySQL、SQLite 使用同一服务和 HTTP 层并通过完整测试矩阵。
- OpenAPI 与生成的 TypeScript 类型不存在未提交漂移。
- Codex、Claude Code 和至少一种其他主流 Coding Agent 完成发布前 eval。

## 4. v1 功能需求

### 4.1 项目初始化与 CLI

提供单一 `aginex` 命令：

- `aginex new <project>`：生成可运行的 Go/Next.js 项目。
- `aginex dev`：跨平台启动 API、Web 和选定的本地基础设施。
- `aginex doctor`：检查工具链、配置、数据库、迁移和生成文件。
- `aginex generate resource`：生成标准资源的迁移、后端、权限、前端和测试骨架。
- `aginex generate client`：从 OpenAPI 生成 TypeScript 客户端。
- `aginex check`：执行格式、静态检查、测试、迁移和契约检查。
- `aginex skills validate|install`：校验并安装 Agent Skills 兼容入口。

CLI 发布 Linux、macOS、Windows 的 amd64/arm64 二进制。

### 4.2 认证、权限与审计

- 支持本地管理员账号和通用 OIDC 登录。
- 首个管理员由 CLI 安全创建，不提供公开注册。
- 密码使用 Argon2id；浏览器使用可撤销的服务端会话和
  HttpOnly、Secure、SameSite Cookie。
- 提供用户、角色、权限、用户角色和角色权限管理。
- 权限标识统一为 `resource:action`，例如 `users:read`。
- 菜单与路由由代码声明权限，不提供数据库动态菜单编辑器。
- 所有写操作记录操作者、动作、资源、资源 ID、变更摘要、时间、IP 和请求 ID。
- v1 为单租户，不提供组织隔离、数据范围权限或 ABAC。

### 4.3 管理端体验

- Next.js App Router、响应式布局、亮暗主题、面包屑和权限感知导航。
- 通用数据表格、分页、排序、筛选、批量选择、表单和删除确认。
- 用户、角色、权限、审计日志、文件元数据、个人资料和基础仪表盘。
- 产品 UI 以英文为默认语言，并预留国际化接口。
- 键盘导航、可见焦点、表单标签和核心流程满足 WCAG AA 基础要求。

### 4.4 业务资源开发闭环

标准业务资源必须包含：

1. PostgreSQL、MySQL、SQLite 迁移。
2. GORM 模型、仓储、服务和 API。
3. Huma 输入输出类型和自动生成的 OpenAPI。
4. 权限声明和初始化数据。
5. 生成的 TypeScript API 类型。
6. Next.js 列表、详情和表单页面。
7. Go 单元/集成测试和 Playwright 主流程测试。
8. 对应 Agent Skill 的完成检查。

生成器不得覆盖已修改文件。v1 不自动合并框架升级与业务代码；升级通过
核心包版本、迁移指南、`doctor` 和升级 Skill 完成。

### 4.5 图片与对象存储

提供统一 `Storage` 接口与三种官方实现：

- Local：本地开发和自动测试。
- S3-compatible：AWS S3、MinIO、Cloudflare R2 等。
- Alibaba Cloud OSS：使用官方 v2 SDK 原生适配。

上传流程：

1. 后端创建上传意图并返回短时预签名目标。
2. 浏览器直传对象存储。
3. 客户端确认上传，后端通过 Stat/Head 校验对象。
4. 保存 provider、bucket、key、文件名、MIME、大小、校验和、所有者、
   可见性和状态。

默认允许 JPEG、PNG、WebP，禁止 SVG；大小和 MIME 白名单可配置。私有
文件使用短时签名 URL。删除失败必须保留可重试状态。

v1 不包含缩略图、裁剪、压缩、格式转换、分片上传、断点续传和文件版本。

## 5. 技术与公共契约

### 5.1 技术基线

- Go 1.25 最低兼容，Go 1.26 构建。
- Gin 作为 HTTP 路由与中间件主框架。
- Huma v2 通过 Gin adapter 提供类型化 API、校验和 OpenAPI 3.1/3.0。
- GORM 作为 ORM；Goose 管理显式迁移。
- Next.js 16 Active LTS、React 19、TypeScript、Tailwind CSS、shadcn/ui。
- TanStack Query/Table、React Hook Form、Zod。
- Testcontainers、Vitest、Testing Library、MSW、Playwright。

完整选型见[第三方库清单](third-party-libraries.md)。

### 5.2 公共接口

- HTTP API 前缀固定为 `/api/v1`。
- 错误响应使用 `application/problem+json`。
- 列表响应统一为 `{ items, page, pageSize, total }`。
- 时间统一为 UTC RFC 3339。
- 业务模块通过稳定的 `Module` 契约注册 API、权限和初始化数据。
- 前端资源通过 `ResourceDefinition` 声明字段、表格、表单、权限和导航。
- `Storage` 提供创建上传、确认、签名访问、Stat 和删除能力。
- 数据库方言差异只允许存在于迁移和数据库 adapter 层。

## 6. Agent Skills

规范源位于 `.agents/skills/<skill>/SKILL.md`。CLI 为不同 Agent 生成兼容
入口，CI 防止内容分叉。

首批 Skills：

- `create-aginex-project`
- `add-business-resource`
- `add-custom-api-operation`
- `add-admin-page`
- `change-database-schema`
- `configure-rbac`
- `add-image-upload`
- `test-and-debug`
- `upgrade-aginex`

每个 Skill 必须描述触发条件、输入、输出、禁止事项、步骤、失败处理、示例
和完成检查。根目录 `AGENTS.md` 只保存全局不变量和 Skill 路由。

Agent eval 在临时项目中执行新增 CRUD、自定义动作、修改字段、配置权限和
图片上传等固定任务，并按照编译、测试、迁移、权限、契约、E2E 和人工修正量
评分。

## 7. 非功能需求

- 默认安全：最小权限、服务端会话、CSRF 防护、安全 Cookie、上传白名单。
- 可观测：结构化日志、请求 ID、健康检查、优雅关闭和 OpenTelemetry 接口。
- 可移植：CLI 支持三大桌面系统和 amd64/arm64。
- 可维护：依赖锁定、SemVer、迁移说明、SBOM、漏洞和许可证扫描。
- 可恢复：迁移可回滚；文件删除和外部调用失败可追踪、可重试。
- 无强制 Redis、队列、云服务或托管平台依赖。

## 8. 路线图与验收

### M1：纵向基础切片

完成仓库骨架、CLI 初始化、三数据库迁移、Gin/Huma API、Next.js Shell、
本地认证、RBAC 和一个示例资源。

### M2：Agent 开发闭环

完成资源生成器、客户端生成、核心 Skills、`aginex check` 和首版 eval。

### M3：生产能力

完成 OIDC、审计、Local/S3/OSS、Docker、健康检查、日志和跨平台 CLI。

### M4：v1.0 候选版

完成英文公共文档、示例项目、升级指南、安全审查、性能基准和全平台测试。

发布条件：

- 三数据库测试矩阵通过。
- OpenAPI 和生成客户端无差异。
- 登录、权限、CRUD、审计和上传 E2E 通过。
- Local、MinIO 和真实 Alibaba OSS 契约测试通过。
- 所有 Skills 通过规范、链接和 eval 检查。
- 无已知 Critical/High 依赖漏洞。

## 9. 明确排除项

v1 不包含运行时业务 Agent、多租户、低代码设计器、插件市场、动态菜单、
后台任务系统、复杂工作流、图片派生处理和自动业务代码升级合并。

