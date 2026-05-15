确认：你的推测大体是对的，而且还有一个 115 缓存因素会放大问题。

**现状**
- webhook 走 `enqueueLibraryCurrentLevelScan`，只要同一个 `libraryID` 已有任意 `library_scan` pending/running，就直接返回 `created=false`，不会排队。
- 这个去重不是按“数据源/目录”精确判断，而是按“库”挡住。同库不同目录的 webhook 也会被忽略。
- current-level scan 在 `scanLibraryCurrentLevel` 里只执行一次 `runtimeProvider.List(ctx, sourcePath)`，后续生成 STRM、图片、字幕都基于这一次返回。
- 所以如果扫描开始后，新文件才上传完成，且对应 webhook 又被 active scan 忽略，就会漏。
- 115 cookie provider 有 `children` 持久缓存，TTL 10 分钟。webhook 扫描不会主动清这个缓存，因此即使任务入队了，也可能拿到旧 children，继续漏。

**典型丢失链路**
1. clouddrive2 连续上传多集/图片。
2. 第一次 webhook 触发目录 A 当前层扫描。
3. 扫描开始时只 `List` 到当时已有的 children。
4. 后续文件继续上传，webhook 进来。
5. 因同库已有 `library_scan`，后续 webhook 被忽略，不会补扫。
6. 如果 115 children 缓存已经写入旧列表，后面短时间再扫也可能还是旧列表。

**更合适的方案**
- 不要“有 active 就丢弃通知”，改成“有 active 就记录 pending/dirty”。
- dirty key 建议按 `libraryID + providerID + sourcePath`，不要只按 library。
- webhook 到达时先做短 debounce，比如 5-30 秒，合并同目录连续通知。
- 入队前或扫描前，清理受影响目录的 115 `children:<path>` 缓存，至少清 parent dir；目录事件可清该目录和 parent。
- 如果扫描中又收到 dirty 标记，当前任务结束后自动 drain dirty 队列，再补扫一次。
- full scan 运行中也不建议直接丢 webhook，因为 full scan 可能已经扫过该目录；应记录 dirty，full scan 完成后补扫 dirty 路径。

**推荐流程**
1. webhook normalize 到“需要重扫的目录”。
2. 记录/合并 dirty 目录。
3. debounce 后尝试启动任务。
4. 如果同库扫描中，不丢，只保留 dirty。
5. 扫描任务结束后检查 dirty，有则继续补扫。
6. 扫描前 invalidate provider children cache。

这样能解决“通知太频繁 + 扫描 snapshot + 去重丢通知”的组合问题，也比简单放开并发更安全。




===================================================

可以用“**事件合并 + 延迟稳定窗口 + 精确缓存失效**”，不要每个 webhook 立刻扫 115。

**推荐策略**
- webhook 只记录 dirty 目录，不马上频繁扫。
- 对同一目录 debounce，例如最后一次通知后等 `60-180s` 再扫。
- 扫描前只清这个目录的 `children` 缓存，不清全 provider。
- 扫描只扫当前层，不递归整库。
- 扫描中如果又来通知，只标记 dirty，结束后再延迟补一次。
- 可加最小重扫间隔，例如同目录 `5-10min` 内最多强制刷新一次；期间只合并 dirty。

**对柯南这种大量集数**
- 100 个文件上传触发 100 次 webhook。
- 系统只保留一个 dirty key：`library + provider + 目录`。
- 每次通知重置 debounce 计时器。
- 上传停止一段时间后，只请求 115 一次该目录 children。
- 生成时已有 STRM 会跳过，不需要重新下载/重写。
- 图片、nfo 等如果存在且未开启 overwrite，也会跳过。

**关键取舍**
- 新内容实时性：不是秒级，通常延迟 1-3 分钟。
- 风控：同目录批量上传只打一次 115 list 请求。
- 正确性：扫描前清 children 缓存，避免旧列表。
- 并发安全：扫描中来的通知不丢，转成下一轮补扫。

**可以再加一层保护**
- 按 provider 做限速队列，例如 115 每 `30-60s` 最多一个 list/scan 任务。
- 对同一库同一 provider 串行执行，避免多个目录同时打 115。
- 如果 clouddrive2 能发“上传完成/批量完成”类事件，优先只响应完成事件。

=======================================================

这个思路是对的，而且比“dirty 层 + task queue 两套东西”更简单：**延迟 dirty 队列本身就是待执行队列**。

可以统一成一种队列项：

```text
scan_queue
- key: libraryID + providerID + sourcePath + mode
- run_after
- priority / reason
- status: pending/running
- event_count
- last_event_at
- payload/options
```

行为：

- 手动扫描：`run_after = now`，等价于延迟 0。
- webhook：`run_after = now + debounce`。
- 同 key 合并：更新 `run_after = max(current, now + debounce)` 或直接 `now + debounce`。
- worker 出队：按 `run_after ASC` 找到期 pending 任务。
- 有前置任务冲突：不丢，重新设置一个较近的 `run_after`，比如 `now + 10s/30s`。
- 扫描中又收到同 key webhook：把该 key 重新标 pending，`run_after = now + debounce`，等当前跑完后补扫。

这个模型很好，因为：
- 所有任务都有“可执行时间”。
- webhook debounce 天然就是推迟 `run_after`。
- 手动任务可以直接执行，不需要另一套路径。
- 队列按时间排序即可。
- 同 key 去重避免 100 个 webhook 产生 100 个任务。

我会建议两个细节：

1. **合并 key 不要太粗**
    - current-level webhook：`libraryID + providerID + sourcePath + current_level`
    - full/library scan：可以是特殊 key，例如 `libraryID + full_library`
    - 避免一个目录 webhook 把整库扫描合并掉，或反过来。

2. **冲突时不要频繁轮询**
    - 如果同 provider 正在跑，pending 项不要每秒重试。
    - 可以设 `run_after = now + 15-30s`。
    - 或 worker 用内存锁/通知机制，任务结束后唤醒下一轮。

简化后的最终流程：

```text
webhook -> upsert queue(key, run_after=now+debounce)
manual scan -> upsert/insert queue(key, run_after=now)

worker:
  取 run_after <= now 的 pending，按 run_after 排序
  检查 full/library/provider 冲突
  能跑 -> status=running -> 扫描
  不能跑 -> run_after=now+retry_delay
  扫描结束 -> 若期间被更新过，回 pending；否则 completed/delete
```

这个设计可以同时解决：
- 不丢 webhook。
- webhook 高频合并。
- 手动任务立即执行。
- 115 请求限速。
- 任务有序、可恢复。