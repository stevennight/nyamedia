# 百度网盘 Open Provider

`baiduopen` 只使用百度网盘开放平台的 OAuth 2.0 和 XPan 官方 REST API，不依赖
Cookie、浏览器会话或第三方百度网盘 SDK。

## 应用与授权

1. 在 [百度网盘开放平台](https://pan.baidu.com/union/) 创建应用。
2. 创建 `baiduopen` 数据源并保存应用的 API Key 和 Secret Key，分别作为
   `client_id` 和 `client_secret`。
3. 把管理页显示的回调地址完整登记到百度应用配置。
4. 点击“打开百度授权页”，确认 `basic,netdisk` 权限。

服务保存以下密钥：

- `client_id`
- `client_secret`
- `access_token`
- `refresh_token`
- `access_token_expires_at`

百度 Refresh Token 与签发它的应用绑定。刷新 Access Token 时仍需要对应的
`client_id` 和 `client_secret`。更换应用凭据会清除旧 Token、Provider 缓存、
已扫描条目和直链状态，避免混用不同应用签发的 Token。

## 官方接口

当前实现直接调用：

- `https://openapi.baidu.com/oauth/2.0/authorize`
- `https://openapi.baidu.com/oauth/2.0/token`
- `https://pan.baidu.com/rest/2.0/xpan/file?method=list`
- `https://pan.baidu.com/rest/2.0/xpan/multimedia?method=filemetas`

`provider_entry_id` 保存百度 `fs_id`。扫描同时持久化 `md5`、文件大小、修改时间、
文件分类和通用 MIME 信息。播放时优先使用已保存的 `fs_id` 获取临时 `dlink`，
不需要重新按路径遍历。

百度下载链接要求使用 `User-Agent: pan.baidu.com`，Provider 会固定返回该请求头，
追加当前 Access Token，并声明支持 HTTP Range。内部把直链到期时间保守设置为
获取后 7 小时。

## 频控策略

百度开放平台没有一条适用于所有应用和所有接口的统一公开 QPS 数值，实际额度可能
随接口、应用权限和账号状态变化。实现不能把某个固定 QPS 当成平台保证。

NyaMedia 使用以下保守策略：

- 扫描请求默认起始间隔为 500ms，约 2 QPS。
- 可在数据源中配置为 250-10000ms。
- 目录 children 缓存持久化 10 分钟；扫描和“强制刷新”会绕过该缓存。
- HTTP `429`、`5xx`、百度频控错误码 `31034`，以及明确包含频控含义的错误响应，
  最多尝试 3 次，退避从 500ms 开始。
- Token 失效会自动刷新并重试原请求；并发刷新合并为一次，新的 Access Token、
  Refresh Token 和到期时间一起写回。
- 播放取链不使用扫描请求间隔，避免固定增加首帧延迟，但仍执行有限频控重试。

默认 2 QPS 是 NyaMedia 的保守默认值，不代表百度官方承诺的额度。出现持续频控时，
应增加数据源的扫描请求间隔，而不是提高重试次数。

## 依赖策略

百度官方的 `baidubce/bce-sdk-go` 面向百度智能云 BCE/BOS，不是个人百度网盘。
当前没有引入社区百度网盘包；OAuth、XPan 请求、分页、Token 刷新、缓存和错误处理
均使用 Go 标准库实现。
