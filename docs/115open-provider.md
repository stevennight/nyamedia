# 115 Open Provider

## 凭据配置

`115open` 支持两种配置方式：

1. 使用自己的 115 Open `client_id` 走 PKCE 扫码授权。官方 PKCE 模式不需要
   AppSecret。
2. 从 `api.oplist.org`、自建 OpenList 服务或其他 Client ID 获取
   `access_token` / `refresh_token`，再在数据源页面直接导入。

导入已有 Token 时，`client_id` 可以留空。115 的 Token 刷新接口只需要
`refresh_token`，不需要签发 Token 时使用的 Client ID 或 AppKey。建议同时保存
Access Token 和 Refresh Token；只有 Refresh Token 时，provider 会在首次访问时换取
新的 Access Token。

扫码授权成功或 Token 自动刷新后，新的 `access_token`、`refresh_token` 和
`access_token_expires_at` 会写回数据源密钥。Provider 会在 Access Token 到期前
1 分钟主动刷新；对于没有有效期信息的外部导入 Token，则在接口返回
`40140125`（`access_token` 无效）等鉴权错误后刷新并自动重试原请求。

## 请求与缓存策略

- 目录 children 缓存会持久化 10 分钟，目录节点也会持久化。管理页的“强制刷新”
  会绕过 children 缓存。
- 完整扫描和当前层扫描会绕过 children 缓存以获取最新目录内容，并对扫描期间的
  115 Open API 请求限速。默认请求起始间隔为 500ms（约 2 QPS），可在数据源配置中
  调整为 250-10000ms；管理页浏览和播放请求不使用该扫描限速。
- 下载链接请求使用播放端传入的 User-Agent；后台下载使用 provider 默认
  User-Agent，并把同一 User-Agent 传给实际下载请求。
- 临时网络错误、HTTP 429 和服务端 5xx 最多重试 3 次，退避时间从 250ms 开始。
- 并发请求发现 Access Token 失效时只执行一次刷新，避免轮换后的 Refresh Token
  被并发重复使用。刷新返回的新 Access Token、Refresh Token 和过期时间会一起
  持久化。
- 健康检查会执行真实的已授权目录请求，不再只检查本地根节点缓存。

## 风控结论

115 Open 使用 OAuth Token，不依赖 Cookie 终端会话，因此没有 `115cookie` 中同终端
登录可能互相挤下线的问题。普通资源接口也没有套用 Cookie provider 的每次请求
2-5 秒全局等待，以免拖慢目录浏览和扫描。

这不表示 115 Open 没有风控：

- OpenList 文档说明 Token 刷新存在基于 IP 的频率限制。
- OpenList 文档明确警告不要用于多人共享、图床、软件托管或向视频网站提供视频
  外链播放等非规范用途。
- 115 官方 PKCE 文档说明 Refresh Token 有效期为 1 年，Access Token 的示例有效期
  为 7200 秒；二维码状态接口是长轮询接口。

因此 NyaMedia 只放开普通读取请求，通过缓存减少调用量，并对 Token 刷新做并发合并
和有限退避。高并发分发、跨用户共享和公开外链仍可能触发平台限制或账号处置。

## 参考资料

- [115 Open 平台](https://open.115.com)
- [115 官方：手机扫码授权 PKCE 模式](https://www.yuque.com/115yun/open/shtpzfhewv5nag11)
- [OpenList：115 Open 配置说明](https://doc.oplist.org/guide/drivers/115_open)
- [OpenListTeam/115-sdk-go](https://github.com/OpenListTeam/115-sdk-go)
