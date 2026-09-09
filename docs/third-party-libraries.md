# Aginex v1 第三方库清单

> 状态：Draft  
> 目标版本：v1.0  
> 更新日期：2026-07-24

## 1. 选型原则

- 优先选择活跃、许可证宽松、职责单一且有清晰升级路径的项目。
- 优先使用 Go 和 Web 标准库；不为少量便利引入大型依赖。
- 生产依赖必须与 Apache-2.0 项目分发兼容。
- 锁定具体版本并通过自动 PR 升级；major 版本不自动合并。
- 所有云厂商能力通过 Aginex 接口隔离，业务模块不得直接依赖 SDK。

## 2. Go 后端

| 库 | 作用 | 优先级 |
|---|---|---|
| `github.com/gin-gonic/gin` | HTTP 路由、中间件和请求上下文 | P0 |
| `github.com/danielgtaylor/huma/v2`、`humagin` | 类型化 API、请求校验、OpenAPI 3.1/3.0、API 文档 | P0 |
| `gorm.io/gorm` | ORM、事务、关联和查询构建 | P0 |
| `gorm.io/driver/postgres` | PostgreSQL 方言 | P0 |
| `gorm.io/driver/mysql` | MySQL/MariaDB 方言 | P0 |
| `github.com/glebarez/sqlite` | 默认无 CGO SQLite 方言；发布前必须通过兼容性门槛 | P0 |
| `gorm.io/driver/sqlite` | CGO SQLite 可选后备构建 | Optional |
| `github.com/pressly/goose/v3` | 显式 SQL 迁移、版本和回滚 | P0 |
| `github.com/knadh/koanf/v2` | 环境变量与配置文件组合 | P0 |
| `github.com/google/uuid` | 用户、会话、审计和文件 ID | P0 |
| `golang.org/x/crypto` | Argon2id 与安全辅助能力 | P0 |
| `golang.org/x/time/rate` | 登录和敏感接口限流 | P0 |

结构化日志、随机数、文件 IO、嵌入模板和 HTTP 基础能力优先使用
`log/slog`、`crypto/rand`、`os`、`io`、`embed`、`text/template` 和
`net/http`。

## 3. 认证与安全

| 库 | 作用 | 优先级 |
|---|---|---|
| `github.com/coreos/go-oidc/v3` | OIDC Discovery 和 ID Token 校验 | P0 |
| `golang.org/x/oauth2` | Authorization Code、PKCE 和 Token Exchange | P0 |
| `github.com/gorilla/securecookie` | OIDC state、nonce 等临时 Cookie 的认证加密 | P0 |
| `github.com/gorilla/csrf` | Cookie 会话下的 CSRF 防护候选；在安全 spike 后固化 | P0 |

v1 的 RBAC 使用显式关系表，不引入 Casbin。浏览器认证使用数据库会话，
不引入 JWT 作为主认证机制。

## 4. 对象存储

| 库 | 作用 | 优先级 |
|---|---|---|
| `github.com/aws/aws-sdk-go-v2/config` | S3 凭证、区域与 endpoint 配置 | P0 |
| `github.com/aws/aws-sdk-go-v2/service/s3` | AWS S3、MinIO、R2 和预签名请求 | P0 |
| `github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss` | Alibaba OSS 原生 adapter | P0 |

Local Storage 使用 Go 标准库。MinIO 与 R2 复用 S3 adapter，不额外引入
MinIO SDK。

## 5. Next.js 前端

| 库 | 作用 | 优先级 |
|---|---|---|
| `next`、`react`、`react-dom` | Next.js App Router 与 React | P0 |
| `typescript` | 类型系统 | P0 |
| `tailwindcss` | 样式与设计 token | P0 |
| `shadcn/ui`、Radix primitives | 可访问、源码可控的组件体系 | P0 |
| `lucide-react` | 图标 | P0 |
| `@tanstack/react-query` | 请求缓存和 mutation | P0 |
| `@tanstack/react-table` | 表格、排序、筛选和选择 | P0 |
| `react-hook-form` | 表单状态 | P0 |
| `zod`、`@hookform/resolvers` | 表单校验 | P0 |
| `openapi-typescript` | OpenAPI 到 TypeScript 类型 | P0 |
| `openapi-fetch` | 类型安全 HTTP 客户端 | P0 |
| `next-intl` | 国际化基础 | P0 |
| `sonner` | 操作反馈 | P0 |
| `date-fns` | 日期格式化 | P0 |
| `class-variance-authority`、`clsx`、`tailwind-merge` | 组件变体和 class 合并 | P0 |
| `cmdk` | 全局命令搜索 | P1 |
| `recharts` | 基础仪表盘图表 | P1 |

默认不引入 Redux、Zustand 和 Auth.js。服务端数据由 TanStack Query 管理，
认证事实源位于 Go 后端。

## 6. CLI 与生成

| 库/工具 | 作用 | 优先级 |
|---|---|---|
| `github.com/spf13/cobra` | CLI 命令结构 | P0 |
| `github.com/charmbracelet/huh` | 交互式初始化表单 | P0 |
| `github.com/Masterminds/semver/v3` | 版本兼容判断 | P0 |
| Go 标准库 + GitHub CLI / Actions | 六平台 CLI 打包、校验、Release 与 Homebrew tap 发布；支持 `cli/` 标签前缀 | P0 |

生成模板使用 Go 标准库 `embed` 和 `text/template`。CLI 不覆盖已修改文件，
不依赖 AST 魔改工具自动合并业务代码。

## 7. 测试、质量与运维

| 库/工具 | 作用 | 优先级 |
|---|---|---|
| `github.com/testcontainers/testcontainers-go` | PostgreSQL、MySQL、MinIO 集成测试 | P0 |
| `github.com/google/go-cmp` | Go 结构化测试比较 | P0 |
| Vitest | 前端单元与组件测试 | P0 |
| Testing Library、`user-event` | 用户行为测试 | P0 |
| MSW | API mock | P0 |
| Playwright | 浏览器 E2E | P0 |
| `@axe-core/playwright` | 自动化可访问性检查 | P1 |
| golangci-lint | Go 静态检查 | P0 |
| govulncheck | Go 漏洞扫描 | P0 |
| Biome | TypeScript/React 格式和 lint | P0 |
| OpenTelemetry Go SDK | 可选的部署侧 OTLP/OTel `observability.Sink` 适配器；框架内置的厂商中立埋点与 W3C 传播不依赖它 | P1 |
| `prometheus/client_golang` | 可选的部署侧 Prometheus `observability.Sink` 和内部指标监听器 | P1 |
| Renovate | 依赖升级 PR | P1 |
| Syft | SBOM | P1 |

## 8. Agent Skills 与文档

| 工具 | 作用 | 优先级 |
|---|---|---|
| Agent Skills `skills-ref` | Skill 格式与 frontmatter 校验 | P0 |
| 自研 `aginex skills validate` | 断链、版本、eval 和危险命令检查 | P0 |
| Fumadocs | 基于 Next.js 的公共文档站候选 | P1 |
| Markdownlint | Markdown 基础质量检查 | P1 |

## 9. 明确不进入 v1

| 依赖/类别 | 原因 |
|---|---|
| Casbin | v1 显式 RBAC 表更透明；ABAC 和数据范围后置 |
| Redis | 不作为启动或生产强制依赖 |
| Swaggo | Huma 自动生成 OpenAPI |
| Zap、Logrus | 使用 `log/slog` |
| Viper | 使用 Koanf |
| JWT 库 | 浏览器认证采用服务端会话 |
| Temporal、Asynq、River | v1 无后台任务系统 |
| Sharp、libvips | v1 无图片派生处理 |
| 运行时 LLM SDK | v1 聚焦 Coding Agent 开发体验 |

## 10. 发布前依赖门槛

- 所有直接依赖均完成许可证核对并进入 NOTICE/SBOM。
- SQLite 默认驱动通过 Go 1.25、Go 1.26、三大桌面系统和 GORM 行为测试。
- AWS S3 adapter 通过 AWS/MinIO/R2 配置契约测试。
- Alibaba OSS adapter 使用官方 v2 SDK并通过真实测试 bucket 验证。
- 不允许存在已知 Critical 或 High 漏洞。
