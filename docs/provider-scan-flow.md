# Provider And Scan Flow

本文档记录当前 provider 接口、扫描生成流程，以及一个可优化的目标流程。

## Provider 接口

当前接口定义在 `internal/provider/provider.go`。

### `Provider`

核心能力接口，所有数据源都需要实现。

```go
type Provider interface {
    ID() string
    Type() string
    List(ctx context.Context, path string) ([]Entry, error)
    Stat(ctx context.Context, path string) (*Entry, error)
    GetDirectLink(ctx context.Context, path string) (*DirectLinkResult, error)
}
```

- `ID()`：返回 provider ID。
- `Type()`：返回 provider 类型，例如 `local`、`115cookie`、`115open`。
- `List(ctx, path)`：列出指定目录的直接子项。
- `Stat(ctx, path)`：查询指定路径的文件或目录信息。
- `GetDirectLink(ctx, path)`：获取文件直链。

### `Entry`

`Entry` 是 provider 返回给 app 层的统一文件条目。

```go
type Entry struct {
    ID       string
    Name     string
    Path     string
    IsDir    bool
    Size     int64
    ModTime  string
    MimeType string
    Metadata map[string]string
}
```

- `ID`：provider 原始条目 ID，例如 115 file id / cid。
- `Path`：provider 内的逻辑路径。
- `Metadata`：provider 专用扩展字段，目前 115 会放 `pick_code`、`parent_id`、`entry_type`、`mime_type`、`mtime`、`size`。
- `ID` 不等于一定可以直接下载的参数；115 取直链主要依赖 `pick_code`。

### `DirectLinkResult`

`GetDirectLink` 的结果。

```go
type DirectLinkResult struct {
    URL           string
    Headers       map[string]string
    ExpireAt      string
    SupportsRange bool
}
```

- `URL`：实际下载或播放地址。
- `Headers`：访问直链需要附加的请求头。
- `ExpireAt`：直链过期时间，当前多数实现未使用。
- `SupportsRange`：是否支持 Range 请求。

### `ScanProvider`

扫描能力接口。

```go
type ScanProvider interface {
    WalkFiles(ctx context.Context, sourcePath string, fn func(entry Entry) error) error
}
```

- `WalkFiles` 从 `sourcePath` 开始递归遍历 provider 文件树。
- 当前全量/部分扫描生成主要依赖这个接口。
- `WalkFiles` 会把目录和文件都回调给 app 层。

### `PersistedEntryMetadataProvider`

用于从 `entries` 恢复 provider 内部缓存。

```go
type PersistedEntryMetadataProvider interface {
    LoadPersistedEntryMetadata(providerPath string, providerEntryID string, metadata map[string]string)
}
```

- 播放 `/stream/...` 时，app 会从 `entries` 读取 `provider_entry_id` 和 `metadata_json`。
- 115 provider 可以用这些信息把 `pick_code` 等字段灌回内存缓存。
- 这样播放时不一定需要重新按路径查询文件信息。

### `LocalFileProvider`

本地文件专用接口。

```go
type LocalFileProvider interface {
    ResolveFilePath(providerPath string) (string, error)
}
```

- 本地 provider 下载副文件时不需要直链。
- app 直接把 provider path 解析成本地真实路径，然后复制文件。

### `WatchProvider`

监听变更接口。

```go
type WatchProvider interface {
    Watch(ctx context.Context, path string, emit func(ChangeEvent)) error
}
```

- 当前用于 provider 变更监听。
- 本文档重点是扫描和生成流程，不展开监听细节。

## 当前 Provider 行为

### `local`

- `List`：读取本地目录直接子项。
- `WalkFiles`：使用 `filepath.WalkDir` 递归遍历本地文件系统。
- `GetDirectLink`：不使用，返回错误。
- 副文件下载：通过 `ResolveFilePath` 找到本地文件后复制。

### `115cookie`

- `List`：先解析目录，再列出直接子项。
- `WalkFiles`：递归调用目录列举。
- 有持久目录 children 缓存，key 形如 `children:<realPath>`，TTL 当前是 10 分钟。
- 有 node 缓存，key 形如 `node:<realPath>`，可保存路径到 file id / pick_code 的映射。
- 获取直链优先依赖 `pick_code`。
- `provider_entry_id` 存 file id / cid，`metadata_json.pick_code` 才是更关键的直链参数。

### `115open`

- `List`：调用 115 Open API 按目录 ID 分页列目录。
- `WalkFiles`：递归调用 `List`。
- 当前只有 provider 实例内存缓存。
- app 每次 `buildProvider` 会创建新实例，所以列表请求和后台扫描之间基本不共享缓存。
- 获取直链也依赖 provider 缓存里的 `pick_code`。

## 当前扫描流程

入口在 `internal/app/app.go`。

### 任务入口

- 全量扫描：`handleScanFull` 创建 `full_scan` 任务。
- 单库扫描：`handleScanLibrary` 创建 `library_scan` 任务。
- 当前层扫描：走 `scanLibraryCurrentLevel`，只扫描一个目录的直接子项。

### `scanLibrary`

`scanLibrary(ctx, taskID, libraryID, sourcePath, targetPath)` 负责选择 mount 和扫描范围。

- 如果传入 `targetPath`，先反查对应 `sourcePath`。
- 如果传入 `sourcePath`，只扫描该 source path 所属 mount 下的局部目录。
- 如果没有传入路径，遍历 library 下所有启用 mount。
- 每个 mount 调用 `scanMount`。
- 扫描结束后调用 `cleanupStaleSTRM` 清理本地输出目录中过期的 `.strm`。

### `scanMount`

`scanMount(ctx, taskID, mount, scanSourcePath)` 是当前主要扫描生成逻辑。

当前流程：

1. 读取 provider 配置并 `buildProvider`。
2. 确认 provider 实现了 `ScanProvider`。
3. 调用 `provider.WalkFiles(ctx, scanSourcePath, fn)` 递归遍历 provider 文件树。
4. 每个 entry 落库到 `entries`。
5. 同时在内存里构建 `filesByDir` 和 `mediaEntries`。
6. 先同步目录级副文件，例如 `tvshow.nfo`、`season.nfo`、目录图片。
7. 遍历 `mediaEntries`，按配置生成 `.strm`。
8. 为每个媒体文件匹配同目录副文件，例如 nfo、字幕、图片、bif、mediainfo。
9. 副文件通过 `downloadProviderFile` 下载或复制。
10. 删除 `entries` 中这次扫描范围下未再出现的旧条目。

重点：当前生成阶段不会从 `entries` 直接读取已有文件列表作为主输入，而是重新通过 provider `WalkFiles` 枚举扫描范围。

### `scanLibraryCurrentLevel`

当前层扫描只处理一个目录的直接子项。

当前流程：

1. 定位 source path 所属 mount。
2. `buildProvider`。
3. 调用 `runtimeProvider.List(ctx, sourcePath)` 只列当前目录。
4. 子项写入 `entries`。
5. 用本次 `List` 返回的数据生成 `.strm` 和同步副文件。
6. 调用 `cleanupStaleSTRMCurrentDir` 只清理当前输出目录里的 `.strm`。

## 当前生成和下载逻辑

### `.strm` 生成

`writeSTRM(providerID, mount, providerPath)` 只根据 provider path 写入播放 URL。

生成内容形如：

```text
<public_base_url>/stream/<provider_id>/<provider_path>
```

`.strm` 生成本身不需要取直链，也不需要下载源文件。

### 副文件下载

`downloadProviderFile(ctx, runtimeProvider, providerPath, targetPath, progress)` 负责下载 nfo、字幕、图片等副文件。

- 如果是 `LocalFileProvider`，解析成本地路径后复制。
- 否则调用 `runtimeProvider.GetDirectLink(ctx, providerPath)` 获取直链。
- 拿到直链后用 HTTP GET 下载到 `.part`，完成后 rename 到目标路径。
- 当前传入的是 `providerPath`，不是直接传入 `provider_entry_id` 或 `pick_code`。

### 播放 `/stream/...`

播放入口会：

1. 根据 URL 取出 `providerID` 和 `providerPath`。
2. `buildProvider`。
3. 对非本地 provider，调用 `loadPersistedEntryMetadata`。
4. `loadPersistedEntryMetadata` 从 `entries` 读取 `provider_entry_id` 和 `metadata_json`。
5. 如果 provider 实现 `PersistedEntryMetadataProvider`，把这些信息加载回 provider。
6. 调用 `GetDirectLink(ctx, providerPath)`。

所以播放链路已经部分使用了 `entries` 的持久 metadata。

## `entries` 当前作用

`entries` 是扫描索引表，当前用途包括：

- 保存 provider 文件树快照。
- 保存 path、parent path、name、size、mtime、mime type。
- 保存 provider 原始 ID 到 `provider_entry_id`。
- 保存 provider 扩展下载参数到 `metadata_json`，例如 115 `pick_code`。
- 播放时用来恢复 provider metadata。
- 扫描完成后删除扫描范围内过期条目。

当前限制：

- 生成阶段没有直接用 `entries` 作为文件列表来源。
- 副文件下载没有直接把 `entries.metadata_json.pick_code` 传给 provider。
- `provider_entry_id` 是通用字段，不保证是直链参数。

## 你提出的目标流程

这个方向是合理的，可以拆成两段：扫描索引和基于索引生成。

### 1. 扫描索引

从 mount 根目录或指定 source path 开始递归扫描。

1. app 调用 provider 列目录或递归 walk。
2. provider 返回每个文件/目录的标准 `Entry`。
3. app 把 `Entry` 落到 `entries`。
4. 对 115，`provider_entry_id` 存 file id / cid，`metadata_json.pick_code` 存直链所需参数。
5. 扫描结束后删除这次扫描范围内未出现的旧 entries。

### 2. 基于 `entries` 生成输出

从 mount 根目录开始，遍历数据库里的 entries，而不是重新拉 provider 目录。

1. 查询 `entries` 中 mount/source path 下的所有条目。
2. 用 `entry_type` 区分目录和文件。
3. 用 `path`、`parent_path`、`name` 组织目录关系。
4. 找出媒体文件，按规则生成 `.strm`。
5. 找出同目录副文件，生成下载任务。
6. 下载任务从 `entries` 查出条目后，转换成 provider 层的直链输入对象。
7. provider 根据自己的需求选择最优参数获取文件。
8. 对 `115cookie`，优先使用输入对象里的 `Metadata["pick_code"]` 获取直链。
9. 如果 metadata 不完整，再回退到 `Stat` 或目录查询补齐。

### 3. 建议的 provider 下载输入

当前 `GetDirectLink(ctx, path)` 只有 path，导致 provider 可能需要重新解析路径。

可以考虑增加一个更适合索引生成的输入结构。

示例：

```go
type DirectLinkInput struct {
    Path            string
    ProviderEntryID string
    Metadata        map[string]string
}
```

这个结构属于 provider 层，不应该直接使用数据库的 `model.Entry`，避免 provider 和 `entries` 表强绑定。app 层负责从 `entries` 读取数据，再转换成 `DirectLinkInput`。

然后 provider 可以这样处理：

- `115cookie`：优先读 `Metadata["pick_code"]`。
- `115open`：优先读 `Metadata["pick_code"]`。
- `local`：仍然用 `Path` 解析本地文件。
- 其他 provider：按自己的 ID 或 metadata 策略实现。

这可以保留旧接口兼容，也可以在 app 层先调用 `LoadPersistedEntryMetadata` 再调用旧 `GetDirectLink`。

## 当前和目标流程的差异

| 项目 | 当前实现 | 目标优化 |
| --- | --- | --- |
| 文件列表来源 | 生成时重新 `WalkFiles` | 扫描后从 `entries` 读取 |
| 目录缓存 | 主要是 provider 内部缓存 | 数据库 entries 成为稳定索引 |
| 115 下载参数 | path -> provider 缓存/解析 -> pick_code | entries.metadata_json.pick_code 直达 |
| `.strm` 生成 | 使用本次 walk 得到的 media entries | 使用数据库里的 media entries |
| 副文件匹配 | 使用本次 walk 的同目录文件列表 | 使用 entries 的 parent_path 查询 |
| 卡顿来源 | 远端递归枚举、分页、限速、清理 | 主要变成数据库查询和必要下载 |

## 结论

你总结的方向基本正确，但需要把“扫描”和“生成”拆开。

推荐目标是：

1. 扫描阶段负责拉 provider 目录并落 `entries`。
2. 生成阶段优先遍历 `entries`，不重新拉 provider 目录。
3. 下载副文件时把 `provider_entry_id` 和 `metadata_json` 一起传给 provider。
4. 115 provider 优先使用 `pick_code` 获取直链。
5. metadata 缺失时才回退到 provider 查询。
