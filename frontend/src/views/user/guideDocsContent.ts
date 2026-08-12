export function buildGuideDocsMarkdown(params: {
  locale: string
  siteName: string
  apiBase: string
}): string {
  const { siteName, apiBase } = params
  const apiRoot = apiBase.replace(/\/+$/, '')
  const apiV1 = /\/v1$/i.test(apiRoot) ? apiRoot : `${apiRoot}/v1`
  const isEn = params.locale.toLowerCase().startsWith('en')

  if (isEn) {
    return `# ${siteName} Documentation

This guide is for regular users: how to create API keys, connect clients, and handle common errors. Admin settings, payment setup, and server ops are intentionally omitted.

**Beginner tutorials**

0. Codex desktop: [https://openai.com/codex/](https://openai.com/codex/)  
   CC-Switch: [https://github.com/farion1231/cc-switch/releases](https://github.com/farion1231/cc-switch/releases)  
   Codex CLI: [https://github.com/openai/codex](https://github.com/openai/codex)

You can also open the in-app [Beginner Basics](/guides/basics) and [CC-Switch Setup](/guides/cc-switch) pages.

## Quick start

1. After signing in, open the **API Keys** page.
2. Create a key and give it a recognizable name.
3. Copy and store the key safely — you will need it for API requests.
4. Fill the base URL and key into your client, script, or third-party tool.

Base URL:

\`\`\`text
${apiRoot}
\`\`\`

Auth header:

\`\`\`text
Authorization: Bearer YOUR_API_KEY
\`\`\`

## OpenAI-compatible calls

If your tool supports OpenAI Compatible / OpenAI API format, you usually only need:

\`\`\`text
Base URL: ${apiRoot}
API Key: YOUR_API_KEY
Model: use a model available for your group
\`\`\`

Example request:

\`\`\`bash
curl ${apiV1}/chat/completions \\
  -H "Authorization: Bearer YOUR_API_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "replace-with-available-model",
    "messages": [
      {"role": "user", "content": "Hello, briefly introduce yourself"}
    ]
  }'
\`\`\`

## API key security

- Do not share API keys in chats, screenshots, public repos, or frontend code.
- If a key may be leaked, disable/delete it and create a new one.
- Prefer one key per device or project so usage is easier to audit.
- On 401 responses, first check whether the key is complete and whether \`Bearer\` is present.

## Balance and usage

Open the **Usage** page to review recent requests, spend, and model usage.  
If you see insufficient balance, the account balance or plan quota is exhausted — contact an admin or wait until top-up is available.

## Common errors

### 401 Unauthorized

Usually a wrong API key, missing \`Authorization\` header, or a deleted key.

### 403 Forbidden

Usually the account has no permission for that model, group, or feature.

### 404 Model Not Found

Wrong model name, or the model is not assigned to your group.

### 429 Too Many Requests

Too many requests hit the rate limit. Lower concurrency or retry later.

### 500 / 502 / 503

The upstream model channel may be temporarily unavailable. Retry later; if it persists, ask an admin to check channel health.

## Third-party tools

Most tools that support the OpenAI API format can connect. Fill in:

\`\`\`text
API Base: ${apiRoot}
API Key: YOUR_API_KEY
Model: a model available for your group
\`\`\`

If the tool needs a full chat endpoint:

\`\`\`text
${apiV1}/chat/completions
\`\`\`

## Contact an admin

If models are unavailable, balances look wrong, or you cannot sign in, contact an admin with:

- your account email or username;
- when the error happened;
- the model name you used;
- the page message or API error body.

Never send API keys, login passwords, or payment secrets to anyone.
`
  }

  return `# ${siteName} 使用文档

这里整理的是给普通用户看的快速教程：怎么创建密钥、怎么接入接口、常见报错怎么处理。后台配置、支付配置和服务器维护不放在这里，避免普通用户看到不必要的信息。

**新手使用教程**

0. Codex 桌面端下载地址：[https://openai.com/zh-Hans-CN/codex/](https://openai.com/zh-Hans-CN/codex/)  
   CC-Switch 下载地址：[https://github.com/farion1231/cc-switch/releases](https://github.com/farion1231/cc-switch/releases)  
   Codex CLI 下载地址：[https://github.com/openai/codex](https://github.com/openai/codex)

站内也可查看 [小白基础教程](/guides/basics) 与 [CC-Switch 安装](/guides/cc-switch)。

## 快速开始

1. 登录网站后进入 **API 密钥** 页面。
2. 点击创建密钥，给密钥取一个容易识别的名称。
3. 复制密钥并保存好，后续请求接口时会用到。
4. 在你的客户端、脚本或第三方工具里填写基础地址和密钥。

基础地址：

\`\`\`text
${apiRoot}
\`\`\`

鉴权方式：

\`\`\`text
Authorization: Bearer 你的_API_Key
\`\`\`

## OpenAI 兼容调用

如果你的工具支持 OpenAI Compatible / OpenAI API 格式，一般只需要填写：

\`\`\`text
Base URL: ${apiRoot}
API Key: 你的_API_Key
Model: 按网站后台可用模型填写
\`\`\`

示例请求：

\`\`\`bash
curl ${apiV1}/chat/completions \\
  -H "Authorization: Bearer 你的_API_Key" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "请替换为可用模型名",
    "messages": [
      {"role": "user", "content": "你好，简单介绍一下你自己"}
    ]
  }'
\`\`\`

## API 密钥安全

- 不要把 API Key 发到聊天群、截图、公开仓库或网页前端代码里。
- 如果怀疑密钥泄露，先禁用或删除旧密钥，再创建新密钥。
- 每个设备或项目尽量单独创建一个密钥，后续排查用量更方便。
- 如果接口返回 401，优先检查密钥是否复制完整、前面是否带了 \`Bearer\`。

## 余额和用量

在 **用量统计** 页面可以查看近期请求量、消耗和模型使用情况。  
如果提示余额不足，说明当前账号余额或套餐额度不够，需要联系管理员处理或等待充值入口开放。

## 常见报错

### 401 Unauthorized

通常是 API Key 错误、缺少 \`Authorization\` 请求头，或者密钥已经被删除。

### 403 Forbidden

通常是账号没有权限访问该模型、分组或功能。

### 404 Model Not Found

模型名称填写错误，或者当前账号所属分组没有分配该模型。

### 429 Too Many Requests

请求太频繁，触发了速率限制。降低并发或稍后重试。

### 500 / 502 / 503

可能是上游模型通道临时不可用。稍后重试；如果持续出现，联系管理员排查通道状态。

## 第三方工具接入

大部分支持 OpenAI 接口格式的工具都可以接入。通常填写三项：

\`\`\`text
API Base: ${apiRoot}
API Key: 你的_API_Key
Model: 网站后台可用模型名
\`\`\`

如果工具要求填写完整接口地址，聊天接口一般是：

\`\`\`text
${apiV1}/chat/completions
\`\`\`

## 联系管理员

如果遇到模型不可用、余额异常、账号无法登录等问题，可以联系管理员并提供：

- 你的账号邮箱或用户名；
- 出错时间；
- 使用的模型名；
- 页面提示或接口返回的错误信息。

请不要把 API Key、登录密码、支付密钥发给任何人。
`
}
