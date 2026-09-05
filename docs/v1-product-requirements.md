# Aginex v1 产品需求规划

> 状态：Draft  
> 目标版本：v1.0  
> 更新日期：2026-08-11
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

- `aginex new`：在当前空目录生成可运行的 Go/Next.js 项目。
- `aginex new <project>`：在当前目录新建 `<project>` 子目录并初始化；
  `--module` 可独立指定可发布的 Go module path。两种模式都拒绝覆盖已有内容；
  未发布的源码构建可显式使用 `--aginex-path` 写入本地开发 `replace`，否则失败关闭。
- `aginex dev`：跨平台启动 API、Web 和选定的本地基础设施。
- `aginex doctor`：检查工具链、配置、数据库、迁移和生成文件。
- `aginex generate resource`：生成标准资源的迁移、后端、权限、前端和测试骨架。
- `aginex generate client`：从 OpenAPI 生成 TypeScript 客户端。
- `aginex check`：执行格式、静态检查、测试、迁移和契约检查。
- `aginex skills validate|install`：校验并安装 Agent Skills 兼容入口。

CLI 发布 Linux、macOS、Windows 的 amd64/arm64 二进制。

### 4.2 认证、权限与审计

- 支持本地管理员账号和通用 OIDC 登录。
- 首个管理员由安全初始化流程创建；本地账号仅由有权管理员创建，不提供公开注册。
- 密码使用 Argon2id；浏览器使用可撤销的服务端会话和
  HttpOnly、Secure、SameSite Cookie。
- 提供用户 CRUD、启用/停用、管理员发起的密码重置和角色分配，以及角色 CRUD
  和 permission grant 管理。
- Permission 定义由编译进应用的 module code 持有并在启动时幂等同步；管理端只分配
  已注册 permission，不提供任意 permission 定义或数据库动态菜单编辑器。
- `Administrator` 是系统管理角色，始终拥有全部已注册 permission 的 `all` scope；
  不允许重命名、删除或削弱其 grants。
- 用户、角色和 grant 变更必须保留至少一个可用管理员、拒绝操作者自锁，并实施
  delegation ceiling：操作者不得授予自己不具备的 permission 或更高 scope。
- 权限标识统一为 `resource:action`，例如 `users:read`。
- 菜单与路由由代码声明权限；前端隐藏无权入口，API 仍是最终授权边界。
- 所有写操作记录操作者、动作、资源、资源 ID、变更摘要、时间、IP 和请求 ID。
- v1 为单租户，不提供组织隔离、数据范围权限或 ABAC。

### 4.3 管理端体验

- Next.js App Router、响应式布局、亮暗主题、面包屑和权限感知导航。
- 通用数据表格、分页、排序、筛选、批量选择、表单和删除确认。
- 用户、角色、权限、审计日志、文件元数据、个人资料和基础仪表盘。
- 产品 UI 以英文为默认语言，内置简体中文切换，并通过类型化消息目录为新增语言提供扩展接口。
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

### 4.5 文件中心与对象存储

提供统一 `Storage` 接口与三种官方实现：

- Local：本地开发和自动测试。
- S3-compatible：AWS S3、MinIO、Cloudflare R2 等。
- Alibaba Cloud OSS：使用官方 v2 SDK 原生适配。

文件中心提供独立的拖放/选择区、显式确认队列、逐文件字节进度、部分失败隔离、
批次结果汇总和独立文件库。一次最多选择 20 个文件，默认保持 `private` 可见性。
不按扩展名或浏览器 MIME 设置文件类型白名单，但仍拒绝空文件、非法 MIME、超限
以及实际字节数与声明不一致的请求。

浏览器通过 XHR 报告上传进度，全局最多同时传输 4 个请求、单个文件最多同时传输
2 个分片，并在文件之间轮询。网络失败按 1/2/4 秒重试 3 次，每次重新获取签名；
暂停会中止活跃 XHR，并将持久进度回落到已确认分片边界。单个文件失败不影响其他
文件，全部结束后文件库只刷新一次；对象传完后以独立的“服务器合并并验证”状态
表达不确定时长的服务端工作。

上传流程：

1. 后端创建上传意图并返回短时预签名目标。
2. Local 使用受控二进制路由，云端由浏览器直传对象存储。
3. 客户端确认上传，后端以流式方式核对精确大小、计算 SHA-256、检测真实 MIME，
   并解析安全预览候选的结构。
4. 保存 provider、bucket、key、文件名、MIME、大小、校验和、所有者、
   可见性和状态。

单文件上限默认 10 MiB，可按 1 MiB 整数步长配置为 1 MiB～1 GiB。断点续传
默认关闭；开启且 Provider 支持 multipart 时，仅严格大于 32 MiB 的新上传使用
固定 32 MiB 分片，最多 32 片，会话有效期为 24 小时。等于或小于 32 MiB 的
文件继续单次上传。策略保存后需重启 API 和 worker 才成为运行值，已创建的上传
继续使用创建时的上限、续传策略和存储 Profile 快照。

对象 key 使用无扩展随机标识，对象按 `application/octet-stream` 存储。只有通过
结构校验的 JPEG、PNG、WebP、GIF 和 PDF 可预览；SVG、HTML、文本、Office、
压缩包、可执行文件、损坏或伪造的预览候选以及其他类型都强制以附件下载。Local
下载增加 `nosniff`，PDF 在前端 sandbox iframe 中预览，文件名使用安全编码。
私有文件使用短时签名 URL。删除失败、未完成 multipart 和过期会话必须保留可追踪、
可重试或可清扫状态。

刷新后恢复要求用户重新选择原文件，并通过文件名、大小、修改时间及首/中/尾各
1 MiB 内容的 SHA-256 指纹匹配；浏览器不得持久化 `File`/`Blob`、签名 URL、
ETag 或凭据。v1 不包含
病毒扫描、缩略图、裁剪、压缩、格式转换、文件版本、文件夹上传或跨设备自动恢复。

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
- Provider 可选能力提供 multipart 与受控读取；云 SDK 类型不得泄漏到业务模块。
- 数据库方言差异只允许存在于迁移和数据库 adapter 层。

Aginex 在 v1 发布前仍只维护一个当前 API 与数据库契约：文件模块在 SQLite、
PostgreSQL 和 MySQL 中各使用唯一的当前 migration baseline。正式发布边界明确前，
installation 配置的版本号固定为 `1`：未发布字段可以直接演进，但 reader/writer
只接受严格的当前 v1 文档，任何其他版本与未知字段均 fail closed。旧本地草稿只可
通过显式、可恢复的 `aginex dev reinitialize` 流程退役，不增加配置 reader、旧文件
migration 家族或旧生成客户端兼容分支。

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
通用多文件上传等固定任务，并按照编译、测试、迁移、权限、契约、E2E 和人工修正量
评分。

## 7. 非功能需求

- 默认安全：最小权限、服务端会话、CSRF 防护、安全 Cookie、流式文件验证和
  非安全类型强制附件下载。
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
