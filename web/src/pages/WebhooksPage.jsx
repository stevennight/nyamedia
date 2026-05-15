import { useMemo, useState } from 'react'
import { api } from '../api/client'
import { JsonBlock } from '../components/JsonBlock'
import { PageSection } from '../components/PageSection'
import { StatusBanner } from '../components/StatusBanner'
import { useAsyncData } from '../hooks/useAsyncData'

const webhookModes = {
  generic: {
    label: '常规 Webhook',
    endpoint: '/api/v1/webhooks/filesystem',
    title: '适用于脚本、AList、OpenList、rclone 等自定义通知源',
    description: '只要外部系统能发 HTTP POST，就可以把变更路径推给 NyaMedia。服务会先按 provider_id 定位数据源，再根据路径匹配已启用媒体库挂载，并触发局部扫描。',
    steps: [
      '在 configs/bootstrap.yaml 设置 webhook.token，重启服务使配置生效。',
      '让外部系统向 Webhook URL 发送 POST JSON。',
      '必须传 provider_id；优先传 source_path，路径要和媒体库挂载的 source_path 使用同一套网盘路径。',
      '如果通知的是目录，传 is_dir: true；如果通知的是文件，可以省略或传 false。',
      '可选传 library_id，用于限制匹配范围。',
    ],
    payload: {
      event: 'change',
      source_path: '/影视/电影/示例电影/示例电影.mkv',
      is_dir: false,
      provider_id: '你的 provider id',
      library_id: '可选 library id',
    },
    notes: [
      'event 可以是 change、create、write、remove、rename 等任意字符串，目前主要用于任务日志记录。',
      '文件路径会扫描父目录，目录路径会扫描目录本身。',
      '如果当前已有全量扫描或同媒体库扫描在运行，本次通知会被接受但不会重复排队。',
    ],
  },
  clouddrive2: {
    label: 'CloudDrive2 兼容',
    endpoint: '/api/v1/webhooks/clouddrive2',
    title: '适用于 CloudDrive2 的 Webhook 通知',
    description: 'CloudDrive2 负责监听网盘或挂载层变化，NyaMedia 接收通知后执行局部扫描。这个入口和常规 Webhook 共用解析逻辑，但 URL 更明确，便于在 CloudDrive2 中管理。',
    steps: [
      '在 configs/bootstrap.yaml 设置 webhook.token，重启 NyaMedia。',
      '打开 CloudDrive2 的 Webhooks 配置文件，把页面下方的 TOML 示例复制进去。',
      '把 base_url 改成 NyaMedia 的 public_base_url，把 x-webhook-token 改成 webhook.token。',
      '保持 file_system_watcher.enabled = true；mount_point_watcher 对 STRM 扫描没有帮助，可以关闭。',
      '保存后新增、删除或重命名一个媒体文件，在任务页面确认是否产生 Webhook 局部扫描。',
    ],
    payload: {
      event: 'create/delete/rename',
      provider_id: '你的 provider id',
      source_path: '/影视/电影/示例电影/旧文件名.mkv',
      destination_path: '/影视/电影/示例电影/新文件名.mkv',
      is_dir: false,
      overwrite: true,
    },
    notes: [
      'CloudDrive2 的 body 是模板，可以直接改成 NyaMedia 需要的扁平 JSON，不需要使用默认 data 数组。',
      'provider_id 必须改成 NyaMedia 数据源页面里的数据源 ID。',
      'source_path 使用 {source_file}，destination_path 使用 {destination_file}；重命名/移动时两个目录都会触发扫描。',
      'CloudDrive2 发出的路径必须在该 provider 的 webhook.path_prefixes 下，并且去掉前缀后能匹配媒体库挂载的 source_path，否则 NyaMedia 会接受请求但 matched 为 0。',
    ],
    clouddriveExample: true,
  },
}

export function WebhooksPage() {
  const [mode, setMode] = useState('generic')
  const systemState = useAsyncData(() => api.systemInfo(), [])
  const doc = webhookModes[mode]
  const publicBaseURL = systemState.data?.public_base_url || window.location.origin
  const webhookURL = `${publicBaseURL}${doc.endpoint}`
  const tokenURL = `${webhookURL}?token=你的webhook.token`
  const clouddriveConfig = useMemo(() => `# NyaMedia CloudDrive2 Webhooks 示例
# 复制后至少修改 base_url、x-webhook-token 和 provider_id。

[global_params]
base_url = "${publicBaseURL}"
enabled = true
time_format = "rfc3339"

[global_params.default_headers]
content-type = "application/json"
user-agent = "clouddrive2/{version}"
x-webhook-token = "你的webhook.token"

[file_system_watcher]
url = "{base_url}/api/v1/webhooks/clouddrive2"
method = "POST"
enabled = true
body = '''
{
  "event": "{action}",
  "provider_id": "你的 provider id",
  "source_path": "{source_file}",
  "destination_path": "{destination_file}",
  "is_dir": {is_dir},
  "overwrite": true,
  "device_name": "{device_name}",
  "user_name": "{user_name}",
  "event_time": "{event_time}",
  "send_time": "{send_time}"
}
'''

[file_system_watcher.headers]
# 这里留空即可，默认 header 已经包含 x-webhook-token。

[mount_point_watcher]
# 挂载/卸载通知不会产生媒体文件变更，默认关闭。
url = "{base_url}/api/v1/webhooks/clouddrive2"
method = "POST"
enabled = false
body = '''
{}
'''
`, [publicBaseURL])
  const curlCommand = useMemo(() => {
    const body = JSON.stringify(doc.payload, null, 2).replaceAll("'", "'\\''")
    return `curl -X POST '${webhookURL}' \\
  -H 'Content-Type: application/json' \\
  -H 'X-Webhook-Token: 你的webhook.token' \\
  -d '${body}'`
  }, [doc, webhookURL])

  return (
    <div className="page-grid one-col">
      <PageSection title="Webhook 配置向导">
        <StatusBanner error={systemState.error} loading={systemState.loading}>
          <div className="webhook-hero">
            <div>
              <span className="system-eyebrow">实时扫描</span>
              <h3>用外部变更通知触发局部扫描</h3>
              <p>适合 CloudDrive2、脚本或其他网盘同步工具。Webhook 只负责触发扫描，不直接访问或修改网盘。</p>
            </div>
            <label className="webhook-mode-picker">
              <span>选择配置方式</span>
              <select value={mode} onChange={(event) => setMode(event.target.value)}>
                {Object.entries(webhookModes).map(([key, item]) => (
                  <option key={key} value={key}>{item.label}</option>
                ))}
              </select>
            </label>
          </div>
        </StatusBanner>
      </PageSection>

      <div className="page-grid two-col">
        <PageSection title={doc.label}>
          <div className="doc-stack">
            <div className="hint-block">
              <strong>{doc.title}</strong>
              <p>{doc.description}</p>
            </div>
            <div className="info-field-grid">
              <div className="info-field">
                <span>Webhook 地址</span>
                <strong className="mono-text">{webhookURL}</strong>
              </div>
              <div className="info-field">
                <span>URL Token 写法</span>
                <strong className="mono-text">{tokenURL}</strong>
              </div>
              <div className="info-field">
                <span>请求头 Token 写法</span>
                <strong className="mono-text">X-Webhook-Token: 你的webhook.token</strong>
              </div>
            </div>
          </div>
        </PageSection>

        <PageSection title="启用 Token">
          <div className="doc-stack">
            <p className="hint">在服务配置文件中增加或修改下面配置，保存后重启服务。</p>
            <pre className="doc-code">{`webhook:\n  token: "换成强随机字符串"`}</pre>
            <div className="hint-block compact">
              <strong>安全建议</strong>
              <p>不要使用 admin 密码作为 webhook token。公网暴露时建议放在反向代理后面，并限制来源 IP。</p>
            </div>
          </div>
        </PageSection>
      </div>

      <div className="page-grid two-col">
        <PageSection title="配置步骤">
          <ol className="doc-list">
            {doc.steps.map((step) => <li key={step}>{step}</li>)}
          </ol>
        </PageSection>

        <PageSection title="注意事项">
          <ol className="doc-list">
            {doc.notes.map((note) => <li key={note}>{note}</li>)}
          </ol>
        </PageSection>
      </div>

      <div className="page-grid two-col">
        <PageSection title="JSON 载荷示例">
          <JsonBlock value={doc.payload} />
        </PageSection>

        <PageSection title="curl 测试命令">
          <pre className="doc-code">{curlCommand}</pre>
        </PageSection>
      </div>

      {doc.clouddriveExample ? (
        <PageSection title="CloudDrive2 Webhooks 配置示例">
          <div className="doc-stack">
            <div className="hint-block">
              <strong>可直接使用的 TOML 示例</strong>
              <p>CloudDrive2 的 body 模板可以自定义，下面的配置会直接发送 NyaMedia 支持的 JSON 字段，不使用默认 data 数组。</p>
            </div>
            <pre className="doc-code">{clouddriveConfig}</pre>
            <div className="hint-block compact">
              <strong>路径映射提醒</strong>
              <p>source_path 最好是网盘内路径，例如 /影视/电影/xxx.mkv，并且要落在媒体库挂载的 source_path 下面。如果 CloudDrive2 发的是本地挂载路径，需要让媒体库 source_path 使用同一套路径。</p>
            </div>
          </div>
        </PageSection>
      ) : null}
    </div>
  )
}
