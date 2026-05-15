## 问题确认

- webhook 现在触发的是 current-level scan。
- 只要同一个 `libraryID` 已有 `library_scan` pending/running，后续 webhook 会被忽略，不会排队。
- current-level scan 只在开始时 `List(sourcePath)` 一次，后续生成阶段不会再看新 children。
- `115cookie` 的 `children:<path>` 缓存 TTL 是 10 分钟。缓存存在时，多次扫描同目录也可能拿到旧 children。

因此，连续上传时可能出现：第一次 webhook 扫到旧快照，后续 webhook 被忽略，或者后续扫描仍命中旧 children 缓存，导致缺集数、图片、nfo 等。

## 目标

- webhook 不丢。
- 高频 webhook 不变成高频 115 请求。
- 手动扫描可以立即排队执行。
- 同一范围或被更大范围覆盖的任务可以合并。
- 扫描不使用目录 children 缓存，避免旧列表。
- 队列和队列元素的状态更新需要线程安全。
- 队列需要持久化，服务重启后不丢待执行任务。
- 前端需要展示队列，让用户知道有哪些任务正在等待执行。
- 队列出队后再创建现有 scan task，尽量不大改当前任务列表。

## 队列模型

使用一个持久化延迟扫描队列，队列项本身是“待执行扫描意图”。

```text
scan_queue
- library_id
- provider_id
- source_path
- mode            current_level / recursive
- run_after       到点后才允许执行
- status          pending / running
- event_count
- last_event_at
- options_json    overwrite 等
- reason_json
- created_at
- updated_at
```

队列按 `run_after ASC` 出队。

队列和现有 scan task 的关系：

- `scan_queue` 保存待执行、可合并、可延迟的任务。
- 现有 `scan_tasks` 继续保存已经开始执行的任务和执行历史。
- worker 从 `scan_queue` 成功出队后，再创建现有 `scan_tasks` 记录。
- 前端任务列表不需要大改；可以额外增加“待执行队列”区域展示 `scan_queue`。
- 用户应能看到队列项的媒体库、路径、模式、预计执行时间、来源、合并次数、状态。

队列需要保证线程安全：

- 多个 webhook、手动扫描、worker 可能同时读写队列。
- 同一个 key 的 upsert/merge 必须是原子操作。
- 任务从 `pending` 变为 `running` 必须是原子抢占，避免多个 worker 执行同一项。
- running 期间如果同 key 又被更新，需要能可靠记录 dirty 状态，任务结束后再决定删除或重新 pending。
- 如果队列持久化到数据库，应使用事务或条件更新保证状态转换正确。
- 队列必须持久化到数据库，而不是只存在内存中。
- 服务启动时 worker 应从数据库恢复 pending 队列项继续调度。

## 扫描范围

- 媒体库扫描：`library_id + library root + recursive`。
- 指定目录扫描：`library_id + source_path + recursive`。
- webhook 文件扫描：归一化为父目录，通常是 `library_id + parent source_path + current_level`。
- webhook 目录扫描：可归一化为该目录，通常是 `library_id + source_path + current_level`。

手动扫描可以设置 `run_after = now`，即延迟 0。webhook 设置 `run_after = now + debounce`。

## 合并规则

核心原则：小范围可以合并到更大范围；相同 current-level 才能互相合并。

- 相同 `library_id/provider_id/source_path/mode`：合并为一条，更新 `run_after`、`last_event_at`、`event_count`。
- 已有小范围 recursive，新进大范围 recursive：删除或覆盖小范围，只保留大范围。
- 已有 current-level，新进覆盖它的 recursive：删除或覆盖 current-level，只保留 recursive。
- 已有大范围 recursive，新进小范围 recursive/current-level：不新增任务，只更新大范围任务的 reason/event_count 即可。
- current-level 只能和完全相同目录的 current-level 合并。
- current-level 不能合并到父级 current-level。

例子：

```text
library1, /tv/show/season1, recursive
+ library1, /tv/show, recursive
=> library1, /tv/show, recursive

library1, /tv/show/season1, current_level
+ library1, /tv/show, recursive
=> library1, /tv/show, recursive

library1, /tv/show/season1, current_level
+ library1, /tv/show, current_level
=> 两条都保留，不能合并

library1, /, recursive
+ library1, /tv/show/season1, recursive/current_level
=> library1, /, recursive
```

注意：`/tv/show,current_level` 只扫描 `/tv/show` 的直接子项，不保证扫描 `/tv/show/season1` 内部内容，所以不能覆盖 `/tv/show/season1,current_level`。

## 执行约束

任务到点后不一定立刻执行，需要满足执行约束。

- 有 full/library recursive 扫描时，同库其他扫描等待。
- 同一 provider 串行执行，避免风控和并发一致性问题。
- 父子路径不要并发扫描，避免 entries 清理和输出清理互相影响。
- 达到全局并发上限时等待。

如果暂时不能执行，不丢任务，设置 `run_after = now + retry_delay` 后继续排队。

同一 provider 串行意味着：

- worker 取到到期任务后，需要先确认该 provider 当前没有 running scan。
- 如果 provider 正在执行，当前任务保持 pending，并推迟 `run_after` 或等待任务完成后唤醒。
- 对 115、其他网盘 provider、local provider 都使用同一套串行规则，不做 provider 类型特例。

## Webhook Debounce

- webhook 入队时不立即扫，设置 `run_after = now + debounce`。
- 同 key 再次到达时，更新 `run_after = now + debounce`。
- 对大量集数上传，同目录多次 webhook 最终通常只触发一次 current-level scan。
- 建议 debounce 初始值为 `60-180s`。

## 目录缓存处理

- 扫描流程不应该使用目录/children 缓存。
- 目录/children 缓存的定位是提升前端目录选择器、文件选择器体验。
- current-level scan 的 `List(source_path)` 应请求 provider 最新 children。
- recursive scan 的递归枚举也应请求 provider 最新 children。
- 不建议通过 webhook 清全 provider 缓存来解决扫描一致性问题。
- 更合适的方式是在扫描接口提供明确的 bypass cache 能力。
- 不引入所谓“扫描路径/浏览路径分离”，避免路径语义变复杂或混乱。

原因：

- 扫描结果是生成 STRM、副文件和 entries 清理的依据，旧 children 会导致缺文件或误清理。
- 前端浏览更关注响应速度，可以接受短 TTL 缓存。
- 网盘风控应通过 debounce、队列合并、provider 串行执行解决，而不是让扫描使用旧缓存。

## 推荐流程

```text
webhook
  -> normalize 到 library/provider/source_path/mode
  -> upsert/merge scan_queue，run_after = now + debounce

manual scan
  -> upsert/merge scan_queue，run_after = now

worker
  -> 按 run_after ASC 取到期 pending 任务
  -> 应用合并/覆盖规则
  -> 检查执行约束
  -> 创建现有 scan_tasks 记录
  -> 使用不走 children 缓存的扫描枚举
  -> 执行 current_level 或 recursive scan
  -> 如果运行期间同 key 又被 webhook 更新，重新 pending 并延迟下一轮
  -> 否则完成并删除或标记 completed
```

## 勘误

- “合并到更上级”只对 recursive 成立。
- current-level 的覆盖范围只有当前目录直接子项，不能覆盖子目录内部。
- 媒体库扫描本质是该媒体库根目录 recursive scan，不需要单独特殊化执行逻辑，但可以保留特殊 mode/key 方便展示和权限控制。
- children 缓存不应参与扫描一致性路径；它只适合前端目录/文件选择体验。
- local provider 不需要特殊调度规则，和其他 provider 一样经过持久化队列并按 provider 串行执行。
- 当前任务列表仍表示“已经出队并开始执行/执行过”的任务；待执行内容由新增队列视图展示。
