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
    description: '只要外部系统能发 HTTP POST，就可以把变更路径推给 Emby115。服务会根据路径匹配已启用媒体库挂载，并触发局部扫描。',
    steps: [
      '在 configs/bootstrap.yaml 设置 webhook.token，重启服务使配置生效。',
      '让外部系统向 Webhook URL 发送 POST JSON。',
      '优先传 source_path，路径要和媒体库挂载的 source_path 使用同一套网盘路径。',
      '如果通知的是目录，传 is_dir: true；如果通知的是文件，可以省略或传 false。',
      '可选传 provider_id / library_id，用于限制匹配范围。',
    ],
    payload: {
      event: 'change',
      source_path: '/影视/电影/示例电影/示例电影.mkv',
      is_dir: false,
      provider_id: '可选 provider id',
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
    description: 'CloudDrive2 负责监听网盘或挂载层变化，Emby115 接收通知后执行局部扫描。这个入口和常规 Webhook 共用解析逻辑，但 URL 更明确，便于在 CloudDrive2 中管理。',
    steps: [
      '在 configs/bootstrap.yaml 设置 webhook.token，重启 Emby115。',
      '打开 CloudDrive2 的 Webhook 设置，新增一个 HTTP POST 通知。',
      'URL 填写 CloudDrive2 兼容入口，并通过 query token 或 header 传 token。',
      '请求体字段至少包含 path 或 source_path；如果 CloudDrive2 发的是挂载路径，需要让它对应到媒体库 mount.source_path。',
      '保存后新增、删除或重命名一个媒体文件，在 Tasks 页面确认是否产生 webhook partial library scan。',
    ],
    payload: {
      event: 'change',
      path: '/影视/电影/示例电影/示例电影.mkv',
      is_dir: false,
    },
    notes: [
      '如果 CloudDrive2 支持自定义 header，推荐使用 X-Webhook-Token。',
      '如果只能配置 URL，可以使用 ?token=你的token。',
      '如果 CloudDrive2 的 payload 字段名不是 path/source_path，本服务也兼容 file_path、filepath、full_path、provider_path、providerPath。',
    ],
  },
}

export function WebhooksPage() {
  const [mode, setMode] = useState('generic')
  const systemState = useAsyncData(() => api.systemInfo(), [])
  const doc = webhookModes[mode]
  const publicBaseURL = systemState.data?.public_base_url || window.location.origin
  const webhookURL = `${publicBaseURL}${doc.endpoint}`
  const tokenURL = `${webhookURL}?token=你的webhook.token`
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
              <span className="system-eyebrow">Realtime Scan</span>
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
                <span>Webhook URL</span>
                <strong className="mono-text">{webhookURL}</strong>
              </div>
              <div className="info-field">
                <span>URL Token 写法</span>
                <strong className="mono-text">{tokenURL}</strong>
              </div>
              <div className="info-field">
                <span>Header Token 写法</span>
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
        <PageSection title="JSON Payload 示例">
          <JsonBlock value={doc.payload} />
        </PageSection>

        <PageSection title="curl 测试命令">
          <pre className="doc-code">{curlCommand}</pre>
        </PageSection>
      </div>
    </div>
  )
}
