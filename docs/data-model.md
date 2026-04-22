# Data Model

## 1. Goal

本文档定义项目的数据库模型，作为后续：

- SQLite schema 设计依据
- migration 编写依据
- API 请求与响应建模依据
- Web Admin 表单与列表实现依据

默认数据库为 SQLite，设计上尽量保持对 MySQL / PostgreSQL 的可迁移性。

## 2. Design Principles

- 数据库是业务配置和运行状态的主存储
- 启动配置不进入本文档范围
- 配置类数据和运行时数据分表管理
- 敏感字段与普通配置尽量隔离
- 用显式状态字段代替隐式删除
- 尽量使用稳定、易迁移的通用字段类型

## 3. Naming Rules

- 表名使用复数小写下划线风格
- 主键默认使用 `text` 类型 ID
- 时间字段统一使用 UTC ISO8601 字符串或 Unix 时间戳
- JSON 扩展字段统一以 `*_json` 命名
- 布尔值在 SQLite 中使用 `integer` 存储，约定 `0/1`

建议 ID 规则：

- 主业务实体使用可读字符串 ID，例如 `pan115`、`movies`
- 日志和任务实体使用 `ulid` 或 `uuid`

## 4. Entity Overview

核心实体分为三类：

### 4.1 Configuration Entities

- `providers`
- `provider_secrets`
- `libraries`
- `library_mounts`
- `settings`
- `admin_users`

### 4.2 Index And Cache Entities

- `entries`
- `direct_link_cache`

### 4.3 Runtime Entities

- `scan_tasks`
- `playback_logs`
- `system_events`

`system_events` 在设计文档中未单独展开，这里建议预留，用于统一记录关键后台事件。

## 5. Table Definitions

### 5.1 `providers`

用途：保存 provider 的非敏感配置和状态。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | provider ID，例如 `pan115` |
| `type` | `text` | no | provider 类型，例如 `115`、`123pan`、`openlist` |
| `name` | `text` | no | 展示名称 |
| `root_path` | `text` | no | provider 默认根路径 |
| `status` | `text` | no | 当前状态，见状态枚举 |
| `last_check_at` | `text` | yes | 最近一次健康检查时间 |
| `last_error` | `text` | yes | 最近一次错误摘要 |
| `config_json` | `text` | yes | 非敏感扩展配置 |
| `enabled` | `integer` | no | 是否启用，`0/1` |
| `created_at` | `text` | no | 创建时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`id`
- `type` 非空
- `enabled in (0,1)`

建议索引：

- `idx_providers_type` on `type`
- `idx_providers_enabled` on `enabled`

状态枚举建议：

- `unknown`
- `healthy`
- `degraded`
- `error`
- `disabled`

### 5.2 `provider_secrets`

用途：隔离保存 provider 敏感信息。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `provider_id` | `text` | no | 对应 `providers.id` |
| `secret_type` | `text` | no | 例如 `cookie`、`token`、`refresh_token` |
| `secret_value` | `text` | no | 密文或原始值 |
| `masked_value` | `text` | yes | 用于 UI 展示的脱敏值 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 复合主键：`(provider_id, secret_type)`
- 外键：`provider_id -> providers.id`

建议索引：

- `idx_provider_secrets_provider_id` on `provider_id`

说明：

- 如果后续引入应用层加密，`secret_value` 存储密文
- API 默认不返回 `secret_value`

### 5.3 `libraries`

用途：保存逻辑媒体库配置。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | library ID，例如 `movies` |
| `name` | `text` | no | 展示名称 |
| `description` | `text` | yes | 说明 |
| `enabled` | `integer` | no | 是否启用 |
| `last_scan_at` | `text` | yes | 最近扫描时间 |
| `created_at` | `text` | no | 创建时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`id`
- `enabled in (0,1)`

建议索引：

- `idx_libraries_enabled` on `enabled`

### 5.4 `library_mounts`

用途：定义 library 与 provider 之间的挂载关系。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | mount ID |
| `library_id` | `text` | no | 所属 library |
| `provider_id` | `text` | no | 所属 provider |
| `source_path` | `text` | no | provider 内源路径 |
| `target_path` | `text` | no | 输出逻辑路径 |
| `media_type` | `text` | yes | 如 `movie`、`series`、`anime` |
| `priority` | `integer` | no | 扫描优先级，默认 `100` |
| `enabled` | `integer` | no | 是否启用 |
| `created_at` | `text` | no | 创建时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`id`
- 外键：`library_id -> libraries.id`
- 外键：`provider_id -> providers.id`
- `enabled in (0,1)`

建议索引：

- `idx_library_mounts_library_id` on `library_id`
- `idx_library_mounts_provider_id` on `provider_id`
- 唯一索引：`uq_library_mounts_unique` on `(library_id, provider_id, source_path, target_path)`

### 5.5 `settings`

用途：保存全局设置。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `key` | `text` | no | 设置名 |
| `value_json` | `text` | no | JSON 格式值 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`key`

建议 Key：

- `playback.default_mode`
- `playback.token`
- `playback.user_agent_rules`
- `server.public_base_url`
- `logging.level`

说明：

- 适合放体量小但结构可能演进的全局配置
- 不建议把 provider 明细配置放进 `settings`

### 5.6 `admin_users`

用途：保存管理后台用户。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | 用户 ID |
| `username` | `text` | no | 登录名 |
| `password_hash` | `text` | no | 密码哈希 |
| `role` | `text` | no | 角色，首版可固定 `admin` |
| `enabled` | `integer` | no | 是否启用 |
| `last_login_at` | `text` | yes | 最近登录时间 |
| `created_at` | `text` | no | 创建时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`id`
- 唯一索引：`username`
- `enabled in (0,1)`

### 5.7 `entries`

用途：保存 provider 扫描后的目录索引。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | 条目 ID，可由 provider 返回或本地生成 |
| `provider_id` | `text` | no | 对应 provider |
| `entry_type` | `text` | no | `file` 或 `dir` |
| `path` | `text` | no | provider 内完整路径 |
| `parent_path` | `text` | yes | 父路径 |
| `name` | `text` | no | 文件或目录名 |
| `size` | `integer` | yes | 文件大小 |
| `mtime` | `text` | yes | 上游修改时间 |
| `mime_type` | `text` | yes | MIME 类型 |
| `content_hash` | `text` | yes | 可选 hash |
| `last_seen_at` | `text` | no | 本次扫描最后见到时间 |
| `created_at` | `text` | no | 创建时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`id`
- 外键：`provider_id -> providers.id`
- 唯一索引：`(provider_id, path)`

建议索引：

- `idx_entries_provider_parent` on `(provider_id, parent_path)`
- `idx_entries_provider_name` on `(provider_id, name)`
- `idx_entries_last_seen_at` on `last_seen_at`

### 5.8 `direct_link_cache`

用途：缓存 provider 返回的短期直链。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `provider_id` | `text` | no | 对应 provider |
| `path` | `text` | no | 文件路径 |
| `url` | `text` | no | 直链地址 |
| `headers_json` | `text` | yes | 请求直链所需头 |
| `supports_range` | `integer` | no | 是否支持 Range |
| `expire_at` | `text` | no | 过期时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 复合主键：`(provider_id, path)`
- 外键：`provider_id -> providers.id`

建议索引：

- `idx_direct_link_cache_expire_at` on `expire_at`

缓存规则：

- 查询前先检查是否过期
- 403/401 触发失效并刷新
- 周期性清理过期记录

### 5.9 `scan_tasks`

用途：记录扫描和后台同步任务。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | task ID |
| `task_type` | `text` | no | `full_scan`、`library_scan` 等 |
| `library_id` | `text` | yes | 关联 library |
| `status` | `text` | no | 任务状态 |
| `progress_total` | `integer` | yes | 总数 |
| `progress_done` | `integer` | yes | 已完成数 |
| `message` | `text` | yes | 状态信息 |
| `error_message` | `text` | yes | 错误详情摘要 |
| `started_at` | `text` | no | 开始时间 |
| `finished_at` | `text` | yes | 完成时间 |
| `created_at` | `text` | no | 创建时间 |
| `updated_at` | `text` | no | 更新时间 |

约束：

- 主键：`id`
- 外键：`library_id -> libraries.id`

建议索引：

- `idx_scan_tasks_status` on `status`
- `idx_scan_tasks_library_id` on `library_id`
- `idx_scan_tasks_started_at` on `started_at`

状态枚举建议：

- `pending`
- `running`
- `completed`
- `failed`
- `cancelled`

### 5.10 `playback_logs`

用途：记录播放请求行为。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | 日志 ID |
| `provider_id` | `text` | no | 对应 provider |
| `path` | `text` | no | 请求播放路径 |
| `mode` | `text` | no | `redirect` 或 `proxy` |
| `client` | `text` | yes | 客户端名称 |
| `user_agent` | `text` | yes | 原始 UA |
| `status_code` | `integer` | no | 响应状态码 |
| `duration_ms` | `integer` | yes | 总耗时 |
| `remote_addr` | `text` | yes | 客户端 IP |
| `error_message` | `text` | yes | 异常摘要 |
| `created_at` | `text` | no | 记录时间 |

约束：

- 主键：`id`
- 外键：`provider_id -> providers.id`

建议索引：

- `idx_playback_logs_provider_id` on `provider_id`
- `idx_playback_logs_created_at` on `created_at`
- `idx_playback_logs_status_code` on `status_code`

说明：

- `user_agent` 可按需裁剪长度
- 高流量场景下需要考虑保留周期和归档策略

### 5.11 `system_events`

用途：统一记录关键系统事件。

字段：

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `id` | `text` | no | 事件 ID |
| `event_type` | `text` | no | 事件类型 |
| `level` | `text` | no | `info`、`warn`、`error` |
| `source` | `text` | no | 来源模块 |
| `message` | `text` | no | 简要信息 |
| `payload_json` | `text` | yes | 详细上下文 |
| `created_at` | `text` | no | 记录时间 |

建议索引：

- `idx_system_events_type` on `event_type`
- `idx_system_events_level` on `level`
- `idx_system_events_created_at` on `created_at`

## 6. Relationships

关系概览：

```text
providers 1 --- n provider_secrets
providers 1 --- n library_mounts
providers 1 --- n entries
providers 1 --- n direct_link_cache
providers 1 --- n playback_logs

libraries 1 --- n library_mounts
libraries 1 --- n scan_tasks
```

说明：

- `settings` 与其他表通常为逻辑关系，不强制外键
- `admin_users` 当前与业务表无直接关系

## 7. Index Strategy

索引目标：

- 保证按 provider/path 查询足够快
- 保证任务和日志列表按时间倒序读取足够快
- 保证管理页面常见筛选字段可直接命中索引

首版不建议过度加索引，避免 SQLite 写入成本上升。

优先索引：

- 所有主键和唯一键
- `entries(provider_id, path)`
- `direct_link_cache(expire_at)`
- `scan_tasks(status, started_at)`
- `playback_logs(created_at)`

## 8. Migration Strategy

建议使用显式 migration 文件。

约定：

- 每次 schema 变更一个迁移文件
- 文件名递增，例如 `0001_init.sql`
- 启动时自动执行未应用的 migration
- migration 表单独维护版本历史

建议增加：

### 8.1 `schema_migrations`

| Field | Type | Null | Description |
| --- | --- | --- | --- |
| `version` | `text` | no | migration 版本 |
| `applied_at` | `text` | no | 应用时间 |

主键：

- `version`

## 9. Retention Strategy

运行时表需要控制体量。

建议策略：

- `playback_logs` 默认保留 7 到 30 天
- `system_events` 默认保留 30 天
- `scan_tasks` 可长期保留最近 N 条
- `direct_link_cache` 自动清理过期数据

## 10. Future Extensions

可能新增的表：

- `sessions`
- `provider_tokens`
- `webhooks`
- `scan_rules`
- `subtitle_assets`
- `artwork_assets`

这些能力不进入 MVP，但当前模型已为其留出扩展空间。

### 10.1 Extensions For Next Backend Phase

结合当前实现进度，下一阶段后端增强可能优先需要以下扩展：

- `sessions`
  - 用于管理后台登录态
- `task_logs`
  - 用于记录 scan task 的分阶段执行日志
- `strm_assets`
  - 用于跟踪已生成 `.strm` 的输出路径和清理状态
- `entry_scan_runs`
  - 用于记录某次扫描中 entry 的归属和清理批次

这些表当前不是必须，但如果后续开始实现以下能力，会显著降低复杂度：

- 管理认证
- 扫描日志和任务详情
- `.strm` 清理与重建一致性
- 更精确的扫描回滚与审计

## 11. Final Recommendation

首版落地建议：

1. 先实现 `providers`、`provider_secrets`、`libraries`、`library_mounts`
2. 再实现 `entries`、`direct_link_cache`
3. 最后补齐 `scan_tasks`、`playback_logs`、`admin_users`

这样可以优先支撑配置、扫描和播放闭环，再逐步增强后台管理能力。
