# Aginex 文档

Aginex 是一个面向 Coding Agent 的 Go/Gin + Next.js 开源管理后台框架。
本目录首先保存 v1 产品与技术决策，后续补充架构、开发、部署和 Skill 文档。

## 当前文档

| 文档 | 状态 | 说明 |
|---|---|---|
| [v1 产品需求规划](v1-product-requirements.md) | Draft | v1 定位、需求、公共契约、路线图和验收条件 |
| [第三方库清单](third-party-libraries.md) | Draft | v1 依赖选型、用途、优先级和明确排除项 |
| [生产部署与运维](operations.md) | Draft | 镜像、迁移、Worker、只读运行、健康检查与 CI 门禁 |
| [生产整改边界](remediation-scope.md) | Draft | 框架基础能力、可选模块、POSTA 业务归属和发布门禁 |
| [预稳定版升级说明](prestable-upgrade.md) | Draft | 2026-07-30 基线之后的破坏性 API、安全和发布迁移步骤 |
| [生成的 OpenAPI 契约](openapi.json) | Generated | Huma API 描述与 TypeScript 客户端输入 |

项目根目录的 `AGENTS.md` 负责把开发任务路由到 `.agents/skills/` 下的九个
标准 Agent Skills。实现状态以根目录 `progress.md` 和 `task_plan.md` 为准。

## 维护规则

- 产品范围、技术选型或公共契约发生变化时，必须同步更新相关文档。
- 依赖版本由锁文件管理；文档描述稳定的 major/minor 技术线，不追逐
  `latest`。
- 人类文档与 Agent Skills 共享事实源，不复制长期规则。
- v1 发布前将稳定内容翻译为英文；中文规划文档继续保留。
