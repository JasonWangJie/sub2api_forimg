# 异步生图接口文档（新）

面向下游对接的持久化异步生图说明。**只覆盖当前在用、推荐对接**的能力：
Host://https://api.tokensfree.xyz
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

建议每 **30~60 秒**查询一次（也可按业务改为 30～60 秒，**请勿频繁**）。提交成功响应通常包含：

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
| `aspect_ratio` | string | 否 | 画面比例：`auto`、`1:1`、`3:2`、`2:3`、`16:9`、`9:16`；省略时默认 `1:1`；`auto` 时上游 `size` 为 `auto` |
| `size` | string | 否 | 可写三类值：① 比例（如 `9:16`，等同 `aspect_ratio`）；② 像素尺寸（如 `1024x1024`）或 `auto`；③ 档位（如 `2K`，等同 `resolution`） |
| `quality` | string | 否 | 画质，如 `high` / `medium` / `low`（视模型支持） |
| `background` | string | 否 | 背景相关参数（视模型支持） |
| `output_format` | string | 否 | 输出格式：`png` / `jpeg` / `webp`（视模型支持） |
| `mask.image_url` | string | 否 | 可选遮罩图 URL（局部编辑，仅图生图有意义） |
| `response_format` | string | 否 | 透传上游；异步结果最终以 OSS URL 返回 |
| `stream` | boolean | 否 | **必须为 `false` 或省略**；异步接口不支持流式 |

\* JSON 图生图依赖 `image_urls` 有有效值；multipart 图生图改为上传 `image` 文件字段。参考图建议格式：PNG / JPG / WEBP。

尺寸优先级：

- 同时有 `aspect_ratio` 与比例形式的 `size` 时，以 `aspect_ratio` 为准。
- `size` 为 `WxH` / `auto` 时按像素尺寸处理。
- 仅有 `resolution` + `size:"9:16"` 时，等价于 `resolution` + `aspect_ratio:"9:16"`。

`resolution` + `aspect_ratio` → 上游 OpenAI `size`（WxH）映射：

| resolution | aspect_ratio | 上游 size |
|---|---|---|
| `1K` | `auto` | `auto` |
| `1K` | `1:1` | `1024x1024` |
| `1K` | `3:2` / `16:9` | `1536x1024` |
| `1K` | `2:3` / `9:16` | `1024x1536` |
| `2K` | `1:1` | `2048x2048` |
| `2K` | `3:2` / `16:9` | `2048x1152` |
| `2K` | `2:3` / `9:16` | `1152x2048` |
| `4K` | `1:1` | `4096x4096` |
| `4K` | `3:2` / `16:9` | `4096x2304` |
| `4K` | `2:3` / `9:16` | `2304x4096` |

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
| `size` | string | 否 | 宽高比别名，如 `1:1`、`2:3`、`3:2`、`3:4`、`4:3`、`4:5`、`5:4`、`9:16`、`16:9`、`21:9`；未传 `resolution` 时也可写 `2K` 表示清晰度 |
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
| `size` | string | 否 | 宽高比，例如 `3:2`；与 `aspect_ratio` 等价；不传则用上游默认比例 |
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

需要指定宽高比时再加 `"size": "3:2"` 或 `"aspect_ratio": "3:2"`。

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
| `failed` | 失败终态 | 读取 `fail_reason` |

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
  "fail_reason": "image generation failed"  // 失败原因
}
```

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
| `400` | 参数无效、`stream=true`、非法尺寸/比例、参考图无效等 |
| `401` | API Key 无效 |
| `403` | 平台不匹配，或普通生图 / 异步生图未开启 |
| `404` | 任务不存在，或非提交所用 API Key |
| `409` | 幂等冲突 / 输入字节额度不足 |
| `413` | 请求体或参考图超限 |
| `503` | 对象存储或运行配置不可用 |

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

---

## 9. 快速对接清单

1. 确认分组已开启「图片生成」+「异步生图」，平台与路径匹配。
2. 按平台选择提交路径：
   - OpenAI 文生图 / 图生图 → `POST /v1/images/generations_oa`（有有效 `image_urls` 或 multipart `image` → 图生图，否则文生图）
   - Gemini 文生图 / 图生图 → `POST /v1/images/generations_sc`
3. 建议携带 `Idempotency-Key`，避免网络重试重复计费。
4. 查询统一：`GET /v1/images/tasks_async/{task_id}`（OpenAI / Gemini 相同）。
5. 仅在 `status === "succeeded"` 时下载 `data[].url`；链接约 24小时有效，过期则无效。
