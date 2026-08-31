# 异步生图接口文档（新）

面向下游对接的持久化异步生图说明。**只覆盖当前在用、推荐对接**的能力：
Base URL: `https://api.tokensfree.xyz`

| 平台 | 场景 | 方法 | 路径 | 受理 HTTP |
|---|---|---|---|---|
| OpenAI | 文生图 / 图生图 | `POST` | `/v1/images/generations_oa` | `202` |
| Gemini | 文生图 / 图生图 | `POST` | `/v1/images/generations_sc` | `202` |
| OpenAI / Gemini | 查询任务 | `GET` | `/v1/images/tasks_async/{task_id}` | `200` |

要点：

- 任务 ID 前缀均为 `asyncimg_*`。
- OpenAI 与 Gemini 的**请求体**方言不同，按平台选用对应提交路径；**受理 / 查询 / 错误响应体**格式一致。
- **查询任务统一**走 `GET /v1/images/tasks_async/{task_id}`（与平台无关）。
- 实际走哪家上游，由 API Key 所属分组决定：`_oa` → OpenAI 分组；`_sc` → Gemini 分组。
- OpenAI / Gemini 均在**同一提交路径**上用 `image_urls` 区分文生图与图生图（见下）。

---

## 1. 启用条件

须同时满足：

1. 有效 API Key，且已分配分组。
2. 平台匹配：OpenAI 用 `_oa`；Gemini 用 `_sc`。
3. 分组同时开启「图片生成」与「异步生图」（`allow_image_generation`、`allow_async_image_generation`）。后者默认关闭。
4. 全站对象存储已启用且可用。
5. 余额 / 订阅 / 额度 / 并发等既有检查通过。

未开启异步生图时，提交接口返回 **`403`**，不会回退到同步接口。

---

## 2. 通用约定

### 2.1 鉴权

```http
Authorization: Bearer <API_KEY>
Content-Type: application/json
```

查询任务必须使用**提交该任务的同一个 API Key**；换 Key 一律 `404`。

### 2.2 轮询

提交成功或任务查询响应可能包含 `Retry-After: 3`。客户端应优先遵守该响应头：首次查询可在 3 秒后发起，之后按 3～30 秒退避轮询，避免高频请求。

```http
Cache-Control: no-store
Location: <查询地址>
Retry-After: 3
```

是否成功以响应体中的 **`status`** 为准，不要仅凭 HTTP `200` 判断成功。

### 2.3 幂等（可选，强烈建议）

```http
Idempotency-Key: <最多 255 字节的客户端唯一值>
```

| 情况 | 结果 |
|---|---|
| 同 Key + 同幂等键 + 同路径 + 同请求体哈希 | 返回首次创建的任务，不重复生成、不计费 |
| 同键但路径或请求体不同 | `409 Conflict` |
| 未带该头 | 每次提交都新建任务（网络重试可能重复计费） |

重试时请原样重发请求体（含空白与字段顺序）。

### 2.4 结果链接

成功后返回的图片 URL 为对象存储（OSS/CDN）链接，**有效期约 24 小时**，请及时下载或转存。签名 URL 过期作废。

---

## 3. OpenAI · 文生图 / 图生图

文生图与图生图共用同一路径 `POST /v1/images/generations_oa`，按 `image_urls` **是否有有效值**分流：

| `image_urls` | 走法 |
|---|---|
| 不传 | 文生图 |
| 传了但无有效值：`null` / `[]` / 全为空字符串 | 文生图 |
| 至少 1 个非空 HTTPS URL | 图生图 |
| multipart 上传了 `image` 文件 | 图生图（即使未带 `image_urls`） |

### 3.1 请求

```http
POST /v1/images/generations_oa
Authorization: Bearer <OPENAI_GROUP_API_KEY>
Content-Type: application/json
Idempotency-Key: oa-img-20260727-001
```

也支持 `multipart/form-data` 直接上传参考图（图生图）。

### 3.2 请求体字段说明

| 字段 | 类型 | 必填 | 说明（注释） |
|---|---|---|---|
| `model` | string | 是 | 分组可用的图片模型名，例如 `gpt-image-2` |
| `prompt` | string | 是 | 画面描述（文生图）或改图 / 重绘指令（图生图） |
| `image_urls` | string[] | 否* | 参考图 HTTPS URL 数组（**字符串数组**，不要传对象数组）。有有效值 → 图生图；不传或无有效值 → 文生图 |
| `n` | number | 否 | 生成张数，受模型能力限制；默认按上游规则 |
| `resolution` | string | 否 | 清晰度档位：`1K` / `2K` / `4K`（推荐写法） |
| `aspect_ratio` | string | 否 | 画面比例：`auto`、`1:1`、`2:3`、`3:2`、`4:5`、`5:4`、`4:3`、`3:4`、`16:9`、`9:16`、`21:9`、`9:21`、`2:1`、`1:2`；省略时默认 `1:1`；`auto` 时上游 `size` 为 `auto`。计费按显式 `resolution` 档位，不因宽比例 WxH 抬档 |
| `size` | string | 否 | 可写三类值：① 比例（如 `9:16`，等同 `aspect_ratio`）；② 像素尺寸（如 `1024x1024`）或 `auto`；③ 档位（如 `2K`，等同 `resolution`） |
| `quality` | string | 否 | 画质，如 `high` / `medium` / `low`（视模型支持） |
| `background` | string | 否 | 背景相关参数（视模型支持） |
| `output_format` | string | 否 | 输出格式：`png` / `jpeg` / `webp`（视模型支持） |
| `mask.image_url` | string | 否 | 可选遮罩图 URL（局部编辑，仅图生图有意义） |
| `response_format` | string | 否 | 透传上游；异步结果最终以 OSS URL 返回 |
| `stream` | boolean | 否 | **必须为 `false` 或省略**；异步接口不支持流式 |

\* JSON 图生图依赖 `image_urls` 有有效值；multipart 图生图改为上传 `image` 文件字段。参考图建议格式：PNG / JPG / WEBP。

OpenAI 图生图自动重试：若上游返回 `image_url fetch failed` 且为连接超时（如 `curl: (28) Connection timed out`），系统会**自动重试 1 次**。任务详情中：

- `retry_count` 递增；
- 时间线出现事件 `upstream_image_url_fetch_retry`（文案含「已安排自动重试（1/1）」）；
- 重试期间 `error_message` 会暂时保留超时说明；若重试仍失败，最终 `fail_reason` / `error_message` 会注明「已自动重试 1 次仍失败」。

尺寸优先级：

- 同时有 `aspect_ratio` 与比例形式的 `size` 时，以 `aspect_ratio` 为准。
- `size` 为 `WxH` / `auto` 时按像素尺寸处理。
- 仅有 `resolution` + `size:"9:16"` 时，等价于 `resolution` + `aspect_ratio:"9:16"`。

`resolution` + `aspect_ratio` → 上游 OpenAI `size`（WxH）映射：

| resolution | aspect_ratio | 上游 size |
|---|---|---|
| any / `auto` | `auto` | `auto` |
| `1K` | `1:1` | `1024x1024` |
| `1K` | `3:2` / `16:9` | `1536x1024` |
| `1K` | `2:3` / `9:16` | `1024x1536` |
| `1K` | `5:4` | `1280x1024` |
| `1K` | `4:5` | `1024x1280` |
| `1K` | `4:3` | `1360x1024` |
| `1K` | `3:4` | `1024x1360` |
| `1K` | `21:9` | `2384x1024` |
| `1K` | `9:21` | `1024x2384` |
| `1K` | `2:1` | `2048x1024` |
| `1K` | `1:2` | `1024x2048` |
| `2K` | `1:1` | `2048x2048` |
| `2K` | `3:2` / `16:9` | `2048x1152` |
| `2K` | `2:3` / `9:16` | `1152x2048` |
| `2K` | `5:4` | `2048x1632` |
| `2K` | `4:5` | `1632x2048` |
| `2K` | `4:3` | `2048x1536` |
| `2K` | `3:4` | `1536x2048` |
| `2K` | `21:9` | `2048x880` |
| `2K` | `9:21` | `880x2048` |
| `2K` | `2:1` | `2048x1024` |
| `2K` | `1:2` | `1024x2048` |
| `4K` | `1:1` | `4096x4096` |
| `4K` | `3:2` / `16:9` | `4096x2304` |
| `4K` | `2:3` / `9:16` | `2304x4096` |
| `4K` | `5:4` | `4096x3272` |
| `4K` | `4:5` | `3272x4096` |
| `4K` | `4:3` | `4096x3072` |
| `4K` | `3:4` | `3072x4096` |
| `4K` | `21:9` | `4096x1752` |
| `4K` | `9:21` | `1752x4096` |
| `4K` | `2:1` | `4096x2048` |
| `4K` | `1:2` | `2048x4096` |

### 3.3 文生图请求示例

```json
{
  "model": "gpt-image-2",          // 模型名
  "prompt": "一只在沙滩上的猫，写实风格", // 提示词
  "n": 1,                          // 张数
  "resolution": "1K",              // 清晰度档位
  "aspect_ratio": "3:2",           // 画面比例
  "quality": "high"                // 画质（可选）
  // 不传 image_urls，或传 null / []，均为文生图
}
```

也可用 `size` 传比例：

```json
{
  "model": "gpt-image-2",
  "prompt": "竖屏建筑工艺长镜头",
  "resolution": "2K",
  "size": "9:16"                   // 等同 aspect_ratio: "9:16"
}
```

### 3.4 图生图 JSON 请求示例

```json
{
  "model": "gpt-image-2",          // 模型名
  "prompt": "保留主体，把背景换成夜景", // 改图指令
  "image_urls": [                  // 有有效 URL → 自动走图生图
    "https://cdn.example.com/reference.png"
  ],
  "resolution": "1K",              // 清晰度
  "aspect_ratio": "1:1"            // 比例
}
```

### 3.5 图生图 multipart 请求示例

```bash
curl -X POST 'https://api.example.com/v1/images/generations_oa' \
  -H 'Authorization: Bearer sk-...' \
  -H 'Idempotency-Key: oa-i2i-multipart-001' \
  -F 'model=gpt-image-2' \          # 模型名
  -F 'prompt=把背景换成夜景' \        # 改图指令
  -F 'resolution=1K' \              # 清晰度
  -F 'aspect_ratio=1:1' \           # 比例
  -F 'image=@reference.png'         # 参考图文件（字段名必须是 image）→ 自动走图生图
```

| 表单字段 | 必填 | 说明 |
|---|---|---|
| `model` | 是 | 模型名 |
| `prompt` | 是 | 提示词 / 改图指令 |
| `image` | 图生图时是 | 参考图文件；上传后即按图生图处理 |
| `resolution` / `aspect_ratio` / `size` 等 | 否 | 同 JSON 模式 |

### 3.6 受理响应 `202 Accepted`

```json
{
  "task_id": "asyncimg_0123456789abcdef",  // 任务 ID，后续查询用
  "query_url": "https://api.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef"
  // query_url：查询地址（由 async_image.public_base_url 拼接）
}
```

---

## 4. Gemini · 文生图

文生图与图生图共用同一路径 `POST /v1/images/generations_sc`：文生图**不传** `image_urls`。

### 4.1 请求

```http
POST /v1/images/generations_sc
Authorization: Bearer <GEMINI_GROUP_API_KEY>
Content-Type: application/json
Idempotency-Key: sc-t2i-20260727-001
```

### 4.2 请求体字段说明

| 字段 | 类型 | 必填 | 说明（注释） |
|---|---|---|---|
| `model` | string | 是 | 分组映射的 Gemini 图片模型，例如 `gemini-3-pro-image-preview` |
| `prompt` | string | 是 | 图片描述提示词 |
| `resolution` | string | 否 | 清晰度：`1K` / `2K` / `4K`（`0.5K` 默认拒绝） |
| `size` | string | 否 | 宽高比别名，如 `auto`、`1:1`、`2:3`、`3:2`、`4:5`、`5:4`、`4:3`、`3:4`、`16:9`、`9:16`、`21:9`、`9:21`；也可传像素尺寸，如 `1080x1350`（约分为 `4:5`）；未传 `resolution` 时也可写 `2K` 表示清晰度 |
| `aspect_ratio` | string | 否 | 与 `size` 同义的比例字段；显式传入时优先于 `size` 的比例别名；可用 `auto` / `自动`（省略上游比例，由模型决定） |

### 4.3 请求示例

```json
{
  "model": "gemini-3-pro-image-preview", // 模型名
  "prompt": "现代客厅，北欧风，自然光",   // 提示词
  "resolution": "2K",                    // 清晰度
  "size": "16:9"                         // 宽高比（也可用 aspect_ratio）
}
```

### 4.4 受理响应 `202 Accepted`

与 OpenAI 异步受理格式一致：

```json
{
  "task_id": "asyncimg_0123456789abcdef",  // 任务 ID，后续查询用
  "query_url": "https://api.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef"
}
```

---

## 5. Gemini · 图生图

与文生图同一路径；通过 `image_urls` 传入一张或多张参考图。

### 5.1 请求

```http
POST /v1/images/generations_sc
Authorization: Bearer <GEMINI_GROUP_API_KEY>
Content-Type: application/json
Idempotency-Key: sc-i2i-20260727-001
```

### 5.2 请求体字段说明

| 字段 | 类型 | 必填 | 说明（注释） |
|---|---|---|---|
| `model` | string | 是 | Gemini 图片模型 |
| `prompt` | string | 是 | 编辑说明；可按「图1、图2…」引用 `image_urls` 顺序 |
| `image_urls` | string[] | 是 | 参考图公网 HTTPS URL 数组（PNG / JPG / WEBP）；顺序对应提示词中的图1、图2… |
| `resolution` | string | 否 | `1K` / `2K` / `4K` |
| `size` | string | 否 | 宽高比，例如 `3:2`；也可传像素尺寸，如 `1080x1350`（约分为 `4:5`）；与 `aspect_ratio` 等价；不传则用上游默认比例 |
| `aspect_ratio` | string | 否 | 可选；多数客户端优先用 `size`；可用 `auto` / `自动`（由上游自动决定比例） |

参考图要求：

- 仅允许 HTTPS 公网 URL（上游 Gemini 按 URL 拉取，本机不下载转 base64）。
- 禁止内网地址（防 SSRF）。
- 建议格式：JPEG / PNG / GIF / WebP；体积受上游拉取限制。

### 5.3 请求示例

```json
{
  "image_urls": [                      // 参考图列表：图1、图2…
    "https://cdn.example.com/ref-1.jpg",
    "https://cdn.example.com/ref-2.jpg"
  ],
  "model": "gemini-3-pro-image-preview",
  "prompt": "画面通透干净，哑光高级展厅室内色调，完整保留竹编、实木、陶瓷、布艺全部原生肌理，光影柔和温润，视觉氛围与参考图二商业茶室柔和质感完全统一。",
  "resolution": "4K"                   // 清晰度；size/aspect_ratio 可选
}
```

需要指定宽高比时可加 `"size": "3:2"` 或 `"aspect_ratio": "3:2"`。也可用 `"size": "1080x1350"` 指定 `4:5` 比例；Gemini 上游按比例和 `resolution` 生成，不保证返回精确像素尺寸。

### 5.4 受理响应 `202 Accepted`

与文生图 / OpenAI 相同：

```json
{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "https://api.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef"
}
```

---

## 6. 查询任务（OpenAI / Gemini 共用）

```http
GET /v1/images/tasks_async/{task_id}
Authorization: Bearer <提交任务的同一个 API_KEY>
```

OpenAI 与 Gemini 异步任务均使用此路径；受理响应里的 `query_url` 也指向此处。

### 6.1 状态字段说明

| `status` | 含义 | 建议 |
|---|---|---|
| `queued` | 已入队，等待执行 | 继续轮询 |
| `processing` | 上游生成 / 上传 OSS / 计费确认中 | 继续轮询 |
| `succeeded` | 成功终态 | 读取 `data[].url` |
| `failed` | 失败终态 | 读取 `error_code` 和 `fail_reason` |

成功与失败查询均返回 HTTP `200`。仅当 `status === "succeeded"` 时可消费图片 URL。

### 6.2 响应示例

排队中：

```json
{
  "status": "queued",
  "task_id": "asyncimg_0123456789abcdef"
}
```

处理中：

```json
{
  "status": "processing",
  "task_id": "asyncimg_0123456789abcdef"
}
```

成功：

```json
{
  "status": "succeeded",
  "task_id": "asyncimg_0123456789abcdef",
  "data": [
    {
      "url": "https://cdn.example.com/images/results/output-1.png"  // OSS 结果链接，约 1 天有效
    }
  ]
}
```

失败：

```json
{
  "status": "failed",
  "task_id": "asyncimg_0123456789abcdef",
  "error_code": 601,                         // 应用层失败对照码，非 HTTP 6xx
  "fail_reason": "上游生图失败（HTTP 400）：非常抱歉，生成的图片可能违反了关于与第三方内容相似性的防护限制。如果你认为此判断有误，请重试或修改提示语。"  // 上游失败时保留脱敏后的完整提示
}
```

失败查询仍返回 HTTP `200`，请同时读取 `error_code` 和 `fail_reason`。应用层失败对照码如下：

| `error_code` | 含义 | 建议 |
|---:|---|---|
| `601` | 内容安全、内容政策或第三方相似性拦截 | 修改提示词后重试，并原样展示 `fail_reason` |
| `602` | 参考图 URL 拉取失败（DNS、TLS、超时、连接重置等） | 检查 HTTPS/CDN，或先上传参考图 |
| `603` | 账号或上游容量耗尽 | 稍后重试并检查账号容量 |
| `604` | 请求参数、提示词、尺寸、格式或参考图无效 | 按原文修正请求 |
| `605` | 上游限流（HTTP 429） | 按 `Retry-After` 退避 |
| `606` | 上游暂时不可用（网关/服务端 5xx） | 稍后重试 |
| `607` | 上游图片输出无法解析 | 检查模型和响应格式 |
| `608` | 生图超时或执行结果未知 | 避免自动重复提交，等待对账 |
| `609` | 存储或计费后处理失败 | 继续查询；不会重新调用上游 |
| `610` | 未分类上游错误（容错码） | 保留并展示 `fail_reason` 原文，携带任务 ID 联系排查；不要据此重复提交 |

`error_code` 不是 HTTP 状态码；任务查询的 HTTP 状态仍按协议固定为 `200`。

---

## 7. 状态说明

| 阶段 | `status` |
|---|---|
| 已入队 | `queued` |
| 调用上游 / 上传 / 计费等中间态 | `processing` |
| 成功 | `succeeded` |
| 失败 | `failed` |

注意：`processing` 可能表示图片已生成但仍在上传或结算，须继续轮询直到成功或失败终态。

---

## 8. 错误响应

### 8.1 常见 HTTP 状态

| HTTP | 场景 |
|---|---|
| `400` | 提交参数无效、`stream=true`、非法尺寸/比例、参考图无效等；任务失败时通常对应 `error_code=601`、`602` 或 `604` |
| `401` | API Key 无效 |
| `403` | 平台不匹配，或普通生图 / 异步生图未开启 |
| `404` | 任务不存在，或非提交所用 API Key |
| `409` | 幂等冲突 / 输入字节额度不足 |
| `413` | 请求体或参考图超限 |
| `408` | 上游请求超时；任务失败通常对应 `error_code=608` |
| `429` | 上游限流；任务失败通常对应 `error_code=605` |
| `502` | 上游网关或账号容量错误；按正文区分 `603` / `606` |
| `503` | 上游、本地运行依赖或对象存储/运行配置暂时不可用；任务失败通常对应 `error_code=606` 或 `609` |
| `504` / `524` | 上游或 CDN 超时；任务失败通常对应 `error_code=606` / `608` |

任务查询的 `failed` 是业务终态，仍使用 HTTP `200`。

### 8.2 错误格式（OpenAI / Gemini 相同）

```json
{
  "error": {
    "type": "async_image_generation_disabled",
    "code": "async_image_generation_disabled",
    "message": "asynchronous image generation is not enabled for this group"
  }
}
```

### 8.3 上游失败原文

Worker 会在现有脱敏和长度限制内保存上游错误正文。任务查询的 `fail_reason` 应直接展示，不要替换成笼统的 “image generation failed”。常见原文包括：

```text
上游生图失败（HTTP 400）：非常抱歉，生成的图片可能违反了关于与第三方内容相似性的防护限制。如果你认为此判断有误，请重试或修改提示语。
```

```text
上游生图失败（HTTP 400）：image_url fetch failed: Failed to perform, curl: (28) Connection timed out after 60001 milliseconds. See https://***.se/***/***/*** first for more details.
```

```text
上游生图失败（HTTP 502）：All available accounts exhausted
```

```text
上游生图失败（HTTP 429）：Upstream rate limit exceeded, please retry later
```

```text
上游生图失败（HTTP 400）：Invalid request
```

### 8.4 近期生产错误快照（只读审计）

以下统计来自 `2026-08-22 00:00` 至 `2026-08-29 11:45` 的服务器日志，事件为 `async_image.upstream_failed`，仅用于排查优先级，不代表接口固定配额或 SLA。关键词分类可能重叠。

| 项目 | 统计 |
|---|---:|
| 总失败事件 | 2312 |
| HTTP 400 / 502 / 503 / 504 | 1526 / 319 / 125 / 114 |
| HTTP 429 / 524 / 403 / 408 | 113 / 50 / 64 / 1 |
| 参考图 URL 拉取失败 | 1231 |
| 账号或上游容量耗尽 | 733（Gemini 669、OpenAI 64） |
| Gemini `Invalid request` | 154 |
| 第三方相似性拦截 | 36 |
| 内容安全/政策拦截 | 至少 29 |
| 缺少参考图、格式或尺寸输入问题 | 16 |
| 通用上游失败 | 4 |

生产服务器本次仅执行只读日志查询，未修改配置、数据库或服务进程。

### 8.5 卡住任务诊断（2026-08-31，只读）

服务器 `108.186.246.14` 在 `2026-08-31T13:25:34Z` 的数据库快照：`succeeded=13626`、`failed=1278`、`execution_unknown=60`、`invoking=8`。其中 1 个是最近两分钟内的正常执行，另有 **7 个 `invoking` 已超过 2 分钟租约**，最早的任务已停留约 2 天 4 小时；7 个卡住任务全部是 Gemini `gemini-3-pro-image-preview`。

这 7 个任务的事件链都只有 `queued -> invoking (invocation_started)`。日志同时记录了每个任务的：

```text
async_image.execution_timeout_cancel  timeout: 900000
async_image.execution_unknown_transition_failed  error: context canceled
```

含义是：Worker 达到后台设置中的 `execution_timeout_seconds=900`（15 分钟）后取消上游请求；随后 Worker 复用已被取消的 context 写入 `execution_unknown`，数据库状态更新失败，任务截至快照仍长期留在 `invoking`，既没有成功也没有失败终态。当前恢复扫描配置为 `worker_lease_seconds=120`、`recovery_interval_seconds=30`，但这些任务尚未被恢复扫描收敛，属于已知运行风险；不要据此重复提交同一请求，以免产生重复生成或计费。生产修复（改用未取消的父 context、增加超时兜底和告警）尚未执行。

近期 7 天任务表中的失败分类为：`upstream_failed=356`、`invalid_reference_image=297`、`local_capacity_exhausted=63`、`execution_timeout=46`、`upstream_capacity_exhausted=27`；另有 `execution_unknown=34`。失败查询仍返回 HTTP `200`，请以 `status`、`error_code` 和 `fail_reason` 为准。

---

## 8.5 管理员异步任务中心

管理员页面 `/admin/async-image-tasks` 支持查看全站任务详情、事件时间线、账号尝试和生成结果：

```http
GET  /api/v1/admin/async-image-tasks
GET  /api/v1/admin/async-image-tasks/{task_id}
POST /api/v1/admin/async-image-tasks/{task_id}/terminate
```

`terminate` 通过任务版本号和当前状态原子校验，将卡住或后处理失败任务结束为 `failed`，记录 `error_code=admin_terminated` 和 `admin_task_terminated` 事件，并清除加密请求载荷。成功、已失败、已过期任务不可重复操作；不会重新调用上游生图。

### 8.6 本轮冒烟、安全与缺陷审查（2026-08-31）

本轮仅在本地工作树执行，未连接生产服务、未部署、未重启。覆盖 Handler/Service 异步任务状态转换、超时终态、恢复扫描、管理员终止、错误码分类、路由注册和前端类型检查。

| 检查项 | 结果 |
|---|---|
| 异步错误分类与 `610` 未知兜底 | 通过；未知文案保留脱敏后的 `fail_reason`，明确像素/尺寸/MIME 错误归 `604` |
| 超时终态写入 | 通过；终态 transition 使用独立短超时且去除已取消信号的 context，不再复用 `context canceled` 的执行 context |
| 恢复扫描 | 通过；按 `started_at`（缺失时 `created_at`）墙钟时间收敛超时 `invoking`，`updated_at` 心跳不会无限延长任务 |
| 管理员终止安全性 | 通过；管理员路由受 AdminAuth/合规中间件保护，服务层使用状态+版本 CAS，成功任务不可覆盖，重复操作返回冲突 |
| 资源泄漏检查 | 已修复；Chat Completions 异步账号超时 context 在兼容服务未配置的提前返回路径显式取消；`go vet ./internal/handler ./internal/service` 通过 |
| 定向/完整冒烟 | `go test ./internal/handler -run 'AsyncImage|DurableAsyncImage|TaskCenter|GatewayRoutes' -count=1`、`go test ./internal/handler -count=1`、`go test ./internal/service -run 'AsyncImage|Gemini.*Async|Reference|Failover' -count=1`、`pnpm typecheck` 均通过 |

安全边界与剩余风险：`execution_unknown` 仍禁止自动重新调用上游，避免未知结果导致重复生成或重复计费；`610` 只表示“无法归类”，客户端不应据此盲目重试。生产真实上游、Redis/PostgreSQL/OSS 端到端、部署后告警和账单对账尚未验证，生产生效需另行授权。

生产复核（2026-08-31 14:50 UTC）显示当前 `invoking=3`，其中执行时间超过 1 小时的任务为 `0`；现有任务均为分钟级新任务。该查询为只读，生产仍运行旧二进制。

## 9. 快速对接清单

1. 确认分组已开启「图片生成」+「异步生图」，平台与路径匹配。
2. 按平台选择提交路径：
   - OpenAI 文生图 / 图生图 → `POST /v1/images/generations_oa`（有有效 `image_urls` 或 multipart `image` → 图生图，否则文生图）
   - Gemini 文生图 / 图生图 → `POST /v1/images/generations_sc`
3. 建议携带 `Idempotency-Key`，避免网络重试重复计费。
4. 查询统一：`GET /v1/images/tasks_async/{task_id}`（OpenAI / Gemini 相同）。
5. 仅在 `status === "succeeded"` 时下载 `data[].url`；任务失败时同时读取 `error_code` 与 `fail_reason`；链接约 24小时有效，过期则无效。

---

## 10. 当日异步生图统计

使用 API Key 所属用户身份，查询服务器配置时区当天的异步生图汇总。统计范围是该用户的全部持久化异步生图任务，不限于当前 API Key；正在处理的任务也计入请求数。

### 10.1 请求

```http
GET /v1/images/tasks_async/stats
Authorization: Bearer <API_KEY>
```

接口只支持 `Authorization: Bearer` 方式传入 API Key，不接受 URL 查询参数传 Key。鉴权通过但余额已耗尽的 Key 仍可读取自己的统计。

### 10.2 成功响应

```json
{
  "object": "async_image.stats",
  "date": "2026-08-23",
  "timezone": "Asia/Shanghai",
  "balance": 12.5,
  "today_requests": 20,
  "success_count": 15,
  "failure_count": 3,
  "success_rate": 83.3333333333
}
```

字段说明：

| 字段 | 类型 | 说明 |
|---|---|---|
| `date` | string | 统计日期，格式 `YYYY-MM-DD` |
| `timezone` | string | 服务器当前配置时区；日期边界按此时区计算 |
| `balance` | number | 当前用户余额 |
| `today_requests` | integer | 当天任务总数，包含处理中、成功和失败任务 |
| `success_count` | integer | 当天成功任务数 |
| `failure_count` | integer | 当天失败任务数，包括 `execution_unknown`、存储失败和计费失败等终态 |
| `success_rate` | number | 成功率百分比，计算为 `success_count / (success_count + failure_count) * 100`；没有终态任务时为 `0` |

响应带有 `Cache-Control: no-store`。余额和统计均为请求时读取，不应长时间缓存。

### 10.3 错误

```json
{
  "error": {
    "type": "authentication_error",
    "code": "authentication_error",
    "message": "invalid API key"
  }
}
```

常见状态码：

| HTTP | code | 说明 |
|---|---|---|
| `401` | `authentication_error` | 缺少或无效 API Key |
| `500` | `stats_unavailable` | 统计查询失败 |
| `503` | `stats_unavailable` | 当前实例未提供异步统计能力 |
