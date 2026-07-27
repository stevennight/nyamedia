# 123pan Provider

`123pan` 使用 123 云盘开放平台 API，只支持开放平台应用的
`client_id` / `client_secret`，不使用个人登录 Cookie 或分享链接。

## 配置

1. 在 [123 云盘开放平台](https://www.123pan.com/developer) 创建应用并取得
   Client ID 和 Client Secret。
2. 在 NyaMedia 管理页创建类型为 `123pan` 的数据源。首次创建时可将根路径设为
   `/`，保存后进入“授权与密钥”。
3. 在固定的 123pan 表单中填写 Client ID 和 Client Secret。保存凭据会清除旧的
   派生 Token；随后的状态检查、目录浏览或扫描会自动申请新的 Access Token，
   并把 Token 及服务端返回的过期时间写入数据源密钥。
4. 状态显示正常后，可在根路径输入框旁使用“浏览”选择完整云盘目录。凭据或根路径
   无效时，数据源状态会显示官方 API 返回的错误。

管理页不会回显 Client Secret 或 Access Token 原文。`access_token` 和
`access_token_expires_at` 是内部派生值，通常不应手动修改。

## 官方 API 映射

实现使用官方开放平台约定的基础地址 `https://open-api.123pan.com`：

| 能力 | 官方接口 | NyaMedia 用法 |
| --- | --- | --- |
| 获取 Token | `POST /api/v1/access_token` | JSON 字段为 `clientID`、`clientSecret` |
| 列目录 | `GET /api/v2/file/list` | 使用 `parentFileId`、`limit`、`lastFileId` 游标分页 |
| 获取下载地址 | `GET /api/v1/file/download_info` | 使用持久化的 `fileId` 获取临时直链 |

所有请求携带 `Content-Type: application/json` 和
`Platform: open_platform`，普通资源请求另外携带
`Authorization: Bearer <access token>`。成功响应的业务码为 `0`；官方接口可能
在 HTTP 200 中返回非零业务码，因此实现会同时检查 HTTP 状态和 `code`。错误内容
不会记录凭据或签名直链。

Token 不使用 Refresh Token。Provider 以官方响应中的 `expiredAt` 为准，在过期前
通过 Client ID 和 Client Secret 重新申请。API 返回未授权时会强制更新一次 Token
并重试；同一数据源的运行时实例共享 Token 状态，并发更新会合并。Token 和过期时间
以一组数据原子写回，写入前还会确认 Client ID / Client Secret 没有被替换。

## 路径、分页与元数据

123 云盘 API 主要使用父目录 ID，而 NyaMedia 的 mount 和 STRM 使用完整逻辑路径。
Provider 从云盘根目录 ID `0` 开始逐级列目录并解析路径，同时缓存
`path -> fileId` 映射。配置的 `root_path` 只是访问边界，Entry.Path 仍是完整云盘
路径。

目录列表使用 v2 的 `lastFileId` 游标连续取页，并在游标结束时停止。每个条目会把
以下字段写入 `entries.metadata_json`：

- `parent_id`
- `entry_type`
- `file_id`
- `etag`
- `size`
- `mtime`
- `mime_type`

`provider_entry_id` 保存 123 云盘 `fileId`。播放和副文件下载优先使用持久化 ID，
因此服务重启后无需重新按路径解析文件即可申请直链。

## 缓存、扫描与直链

- children 缓存持久化 10 分钟，key 为 `children:<完整路径>`。管理页“强制刷新”
  和扫描会绕过 children 缓存。
- 官方当前为 Access Token 接口标注 10 QPS、v2 文件列表接口标注 15 QPS。
  NyaMedia 扫描默认使用更保守的 500ms 请求起始间隔，可在数据源设置中调整为
  250-10000ms。HTTP 429、临时网络错误和服务端 5xx 使用有限退避重试。
- 下载地址按需申请，不长期缓存，也不依赖未在官方响应中约定的固定过期时长。
  NyaMedia 会把客户端的 Range 等播放请求头转发给签名 URL；下载配额、流量限制
  和链接可用性仍以 123 云盘账号与官方接口响应为准。
- 更换 Client ID / Client Secret 时会同时清理旧 Token、目录缓存、直链缓存和
  条目索引，防止旧账号的 `fileId` 被新账号复用；后续扫描会重建条目索引。
- 123pan 当前不提供 NyaMedia 所需的实时变更监听能力，因此管理页会强制关闭
  `watch_enabled`。

## 参考资料

- [123 云盘开放平台](https://www.123pan.com/developer)
- [123 云盘开放平台官方文档](https://www.yuque.com/org-wiki-123yunpan-muaork/cr6ced)
- [获取 Access Token](https://www.yuque.com/org-wiki-123yunpan-muaork/cr6ced/gn1nai4x0v0ry9ki)
- [开发须知与接口频率](https://www.yuque.com/org-wiki-123yunpan-muaork/cr6ced/txgcvbfgh0gtuad5)
- [v2 文件列表](https://www.yuque.com/org-wiki-123yunpan-muaork/cr6ced/zrip9b0ye81zimv4)
- [获取下载信息](https://www.yuque.com/org-wiki-123yunpan-muaork/cr6ced/fnf60phsushn8ip2)
