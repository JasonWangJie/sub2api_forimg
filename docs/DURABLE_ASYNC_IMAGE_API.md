# Sub2API 持久化异步生图 API

本文档说明 Sub2API 对下游提供的持久化异步生图兼容层。这里的 **BB** 和 **SC** 是两种下游请求/响应方言，不是上游供应商。实际调用 Gemini 还是 OpenAI，由请求路径决定；计费分组由 API Key 的 `image_platform_groups` 映射（优先）或主分组平台回退解析。详见仓库 [wiki-new/API密钥双平台生图映射.md](../wiki-new/API密钥双平台生图映射.md)。

## 1. 范围与兼容性

本功能新增固定路径，不替换也不改写现有接口：

| 方言 | 平台与场景 | 方法与路径 | 提交 HTTP 状态 |
|---|---|---|---|
| BB | Gemini 文生图/图生图 | `POST /v1/chat/completions_gm` | `202 Accepted` |
| BB | OpenAI 文生图 | `POST /v1/images/generations_oa` | `202 Accepted` |
| BB | OpenAI 图生图 | `POST /v1/images/edits_oa` | `202 Accepted` |
| BB | 查询图片任务 | `GET /v1/images/tasks_async/{task_id}` | `200 OK` |
| SC | 上传 Gemini 参考图 | `POST /v1/uploads/images_sc` | `200 OK` |
| SC | Gemini 文生图/图生图 | `POST /v1/images/generations_sc` | `200 OK` |
| SC | 查询图片任务 | `GET /v1/tasks_sc/{task_id}` | `200 OK` |

这些路径和状态语义是固定协议，不提供配置项，也不提供省略 `/v1` 的别名。部署时只配置生成绝对链接所需的 `async_image.public_base_url`。

以下行为保持不变：

- `/v1/chat/completions`、`/v1/images/generations` 和 `/v1/images/edits` 仍是原有同步接口，不会因为分组打开“异步生图”而改变响应体。
- `/v1/images/generations/async`、`/v1/images/edits/async` 和 `/v1/images/tasks/{task_id}` 是原有 Redis 异步接口，路由、数据、有效期和响应体均不迁移。其说明仍见 [ASYNC_IMAGE_TASKS.md](./ASYNC_IMAGE_TASKS.md)。
- 新开关只授权调用本页的新路径；关闭开关后，下游仍可按原权限使用同步接口。
- 本期只实现图片。视频提交、视频任务、视频转存和视频计费均不在范围内。
- SC 方言首版仅支持 Gemini 分组。OpenAI 分组必须使用 `_oa` 路径和 OpenAI 原生图片参数，系统不会把 SC 的 `4K`、`16:9` 近似转换为 OpenAI 尺寸。

## 2. 启用条件

新任务必须同时满足以下条件：

1. 请求携带有效的 Sub2API API Key。
2. 能为该路径解析出计费分组：Key 的 `image_platform_groups`（`gemini`/`openai`）优先；否则主 `group_id` 且平台与路径匹配。
3. 计费分组平台与接口匹配：`_gm`、`_sc` 需要 Gemini，`_oa` 需要 OpenAI。
4. 计费分组已开启普通“图片生成”和新增的“异步生图”。字段名分别为 `allow_image_generation` 与 `allow_async_image_generation`。
5. 全站对象存储已启用、凭证完整并可用。
6. 用户、API Key、分组、订阅/余额、额度和并发等现有检查通过。

`allow_async_image_generation` 默认关闭，只对 Gemini/OpenAI 分组有效。关闭普通图片生成或把分组切换到其他平台时，该值会自动关闭。未开启异步生图的分组调用新提交接口返回 `403`，而不是自动回退到同步接口。

同一把 Key 可同时映射 Gemini 与 OpenAI 生图分组（1 Key 双用）；聊天等非异步生图仍使用主分组。旧 Key 仅有主分组时行为与升级前一致。

对象存储支持 `qiniu`（七牛云）、`aliyun`（阿里云）、`tencent`（腾讯云）和兼容既有部署的 `custom_s3`。部署参数见 [deploy/config.example.yaml](../deploy/config.example.yaml) 中的 `image_storage` 与 `async_image`。

## 3. 通用请求约定

### 3.1 鉴权

所有提交、上传和公共查询接口都使用同一种 API Key：

```http
Authorization: Bearer <API_KEY>
```

JSON 提交接口还应发送：

```http
Content-Type: application/json
```

查询任务时必须使用**提交任务的同一个 API Key**。即使两个 Key 属于同一用户，另一个 Key 查询也会得到 `404`，以免泄露任务是否存在。登录后的站内任务页面可以查看该用户所有 Key 提交的任务；管理员任务页面可以查看全站任务。

### 3.2 查询地址

BB 提交响应的 `query_url` 以及所有提交响应的 `Location` 头由 `async_image.public_base_url` 加固定查询路径生成。本机目录存储时，该字段为空才回落同表单「站点/API 公开地址」；OSS / Superbed 的 `public_base_url` 是对象/CDN 根地址，不能用于任务查询。仅当管理端从未保存过图片存储时，才回落 `config.yaml` 的 `async_image.public_base_url`。例如：

```yaml
async_image:
  public_base_url: "https://apiimg.example.com"
```

则 BB 查询地址形如：

```text
https://apiimg.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef
```

`public_base_url` 留空时返回 `/v1/...` 相对路径。它是 Sub2API 的外部访问根地址，不是 OSS/CDN 地址；本机专用静态图床根地址配置在 `image_storage.local.local_url`。

提交响应还包含：

```http
Cache-Control: no-store
Location: <对应方言的查询地址>
Retry-After: 3
```

处理中查询也带 `Cache-Control: no-store` 与 `Retry-After: 3`。建议每 3 秒轮询一次，并在业务侧设置总等待上限。

### 3.3 幂等提交

所有新提交接口支持可选请求头：

```http
Idempotency-Key: <最多 255 字节的客户端唯一值>
```

幂等范围是“同一 API Key + 同一 `Idempotency-Key`”：

- 请求路径和原始请求体哈希相同：返回第一次创建的任务，不会再次生成或再次计费。
- 同一个 `Idempotency-Key` 配不同路径或不同请求体：返回 `409 Conflict`。
- 未发送该请求头：每次提交都会创建新任务，网络重试可能产生多次生成和多笔费用。

请求哈希包含平台、方言、固定路径和原始请求体；仅仅调整 JSON 空白或字段顺序也可能被视为不同请求。客户端重试时应原样重发请求体。

## 4. BB / Gemini

### 4.1 文生图

```http
POST /v1/chat/completions_gm
Authorization: Bearer <GEMINI_GROUP_API_KEY>
Content-Type: application/json
Idempotency-Key: order-20260720-001
```

```json
{
  "model": "gemini-3-pro-image-preview",
  "stream": false,
  "max_tokens": 4096,
  "messages": [
    {
      "role": "user",
      "content": "现代客厅，北欧风，自然光"
    }
  ],
  "extra_body": {
    "google": {
      "image_config": {
        "image_size": "2K",
        "aspect_ratio": "16:9"
      }
    }
  }
}
```

### 4.2 图生图

`messages[].content` 使用 Chat Completions 多模态数组。参考图可以是受支持的 `data:image/...;base64,...`，也可以是公网 HTTPS URL：

```json
{
  "model": "gemini-3-pro-image-preview",
  "stream": false,
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "image_url",
          "image_url": {
            "url": "https://cdn.example.com/reference.png"
          }
        },
        {
          "type": "text",
          "text": "保留构图，把场景改成夜景"
        }
      ]
    }
  ],
  "extra_body": {
    "google": {
      "image_config": {
        "image_size": "4K",
        "aspect_ratio": "auto"
      }
    }
  }
}
```

BB Gemini 参数约束：

| 字段 | 规则 |
|---|---|
| `model` | 必填，继续使用分组现有模型映射和账号调度。 |
| `stream` | 必须为 `false` 或省略；异步接口不接受流式结果。 |
| `messages` | 至少一条；本接口只接受 `role: "user"`。 |
| `content` | 非空字符串，或只包含 `text` / `image_url` 的非空数组；必须包含非空文本提示。 |
| `image_size` | 可省略，或为 `1K`、`2K`、`4K`。`0.5K` 默认拒绝；仅当模型命中 `async_image.gemini_half_k_models` 的精确名称或末尾 `*` 前缀规则时透传。计费仍使用现有分组档位：Worker 以实际输出宽高定档，合法 `0.5K` 产物归入最低的 `1K` 档，不会回退为 `2K`。 |
| `aspect_ratio` | 可省略，或为 `auto`、`1:1`、`2:3`、`3:2`、`4:5`、`5:4`、`4:3`、`3:4`、`16:9`、`9:16`、`21:9`、`9:21`。 |
| `auto` / `自动` | 文生图与图生图均可用；系统通过省略上游比例让 Gemini 自动决定。 |

系统把 `image_size` / `aspect_ratio` 映射为 Gemini `generationConfig.imageConfig`，强制 `stream=false`，并要求上游返回 `TEXT` 与 `IMAGE`。HTTPS 参考图以 Gemini `fileData.fileUri` 透传（由上游拉取，本机不再下载转 base64）；`data:` URI 仍转换为 `inlineData`。返回中的所有 `inlineData` 图片都会进入同一任务结果。

### 4.3 提交响应

成功受理返回 `202 Accepted`：

```json
{
  "id": "asyncimg_0123456789abcdef",
  "task_id": "asyncimg_0123456789abcdef",
  "object": "image.task",
  "status": "queued",
  "query_url": "https://api.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef"
}
```

## 5. BB / OpenAI

OpenAI `_oa` 接口复用现有 OpenAI Images 解析、模型映射、内容审核、账号选择、故障切换、并发和计费链路。`model`、`prompt`、`n`、`quality`、`background`、`output_format`、`output_compression`、`response_format`、`moderation`、`style` 等既有原生参数按模型能力继续兼容；`stream` 必须为 `false` 或省略。

尺寸推荐传 `resolution`（`1K`/`2K`/`4K`）+ `aspect_ratio`（`auto`/`1:1`/`2:3`/`3:2`/`4:5`/`5:4`/`4:3`/`3:4`/`16:9`/`9:16`/`21:9`/`9:21`/`2:1`/`1:2`）。计费按显式档位，不因宽比例映射 WxH 抬档。`size` 兼容：

- 比例字符串（如 `9:16`），等价于 `aspect_ratio`
- 旧版 WxH（如 `1024x1024`）或 `auto`
- 档位字符串（如 `2K`），等价于 `resolution`

网关会映射为上游 OpenAI Images 的 WxH `size` 后再转发。

### 5.1 文生图

```http
POST /v1/images/generations_oa
```

```json
{
  "model": "gpt-image-2",
  "prompt": "一只在沙滩上的猫，写实风格",
  "n": 1,
  "resolution": "1K",
  "aspect_ratio": "3:2",
  "quality": "high"
}
```

```json
{
  "model": "gpt-image-2",
  "prompt": "竖屏建筑工艺长镜头",
  "resolution": "2K",
  "size": "9:16"
}
```

### 5.2 图生图 JSON

```http
POST /v1/images/edits_oa
```

```json
{
  "model": "gpt-image-2",
  "prompt": "保留主体，把背景换成夜景",
  "image_urls": [
    "https://cdn.example.com/reference.png"
  ],
  "resolution": "1K",
  "aspect_ratio": "1:1"
}
```

JSON 图生图使用 `image_urls`（URL 字符串数组）。仍兼容旧的 `images[].image_url` 对象数组。`images[].file_id` 不受支持；遮罩图 URL 使用现有的 `mask.image_url` 格式。网关在转发上游 API Key 通道前会把 `image_urls` 规范化为上游 `images[].image_url`。

### 5.3 图生图 multipart

也兼容现有 OpenAI multipart 图片编辑格式：

```bash
curl -X POST 'https://api.example.com/v1/images/edits_oa' \
  -H 'Authorization: Bearer sk-...' \
  -H 'Idempotency-Key: edit-20260720-001' \
  -F 'model=gpt-image-1' \
  -F 'prompt=把背景换成夜景' \
  -F 'image=@reference.png'
```

重试 multipart 请求时，边界和原始请求体也必须保持一致，才能命中相同请求哈希。

### 5.4 提交响应

文生图和图生图成功受理都返回 `202 Accepted`：

```json
{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "https://api.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef"
}
```

## 6. BB 任务查询

```http
GET /v1/images/tasks_async/{task_id}
Authorization: Bearer <提交任务的同一个 API_KEY>
```

排队中：

```json
{
  "status": "queued",
  "task_id": "asyncimg_0123456789abcdef"
}
```

处理中，包括调用上游、上传 OSS 或等待计费确认：

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
      "url": "https://cdn.example.com/images/results/output-1.png"
    }
  ]
}
```

失败终态：

```json
{
  "status": "failed",
  "task_id": "asyncimg_0123456789abcdef",
  "fail_reason": "image generation failed"
}
```

成功和失败查询都返回 HTTP `200`。只有 `status` 为 `succeeded` 时才可以消费 `data[].url`；不要根据 HTTP `200` 单独判断任务成功。

同一 API Key 提交的任务可用 BB 或 SC 查询路径读取；响应按请求路径的方言格式返回，避免因路径与提交方言不一致而误报 `task_not_found`。

## 7. SC / Gemini

### 7.1 上传参考图

上传不是文生图的必需步骤。需要图生图且参考图没有安全可访问的 HTTPS URL 时，可先上传：

```bash
curl -X POST 'https://api.example.com/v1/uploads/images_sc' \
  -H 'Authorization: Bearer sk-...' \
  -H 'Idempotency-Key: upload-reference-001' \
  -F 'file=@reference.png'
```

成功返回 `200 OK`：

```json
{
  "url": "https://storage.example.com/images/inputs/1/asyncimg_0123456789abcdef.png",
  "filename": "reference.png",
  "content_type": "image/png",
  "bytes": 204800,
  "created_at": 1784548800
}
```

`created_at` 是 Unix 秒。把 `url` 放入提交请求的 `image_urls`。上传 URL 的默认有效期由 `async_image.input_retention_hours` 控制；任务正在使用的输入对象不得提前清理。

#### 上传 admission 与配置边界

SC 上传使用 PostgreSQL 两阶段 admission，任何阶段数据库不可用都会 fail closed 返回 `503`，不会退化到进程内计数或直接上传 OSS：

1. 在解析 multipart body 前，按 API Key 获取事务锁，执行精确的 60 秒滚动限频并写入一次 attempt。无效或超限 body 也不能绕过请求频率统计。
2. 有界读取文件后、图片解码和 OSS 写入前，消费第一阶段 admission，再按同一 API Key 串行检查幂等键和字节额度；额度同时统计仍有效的输入对象、活跃 reservation，以及带未清理 object intent 的 failed/stale reservation，防止多实例并发超卖或通过失败上传释放额度。

| 配置 | 默认值 | 强制最大值 | 含义 |
|---|---:|---:|---|
| `async_image.upload_per_minute` | `20` | `1000` | 每 API Key 的 60 秒滚动上传尝试数 |
| `async_image.max_input_bytes_per_key` | `1 GiB` | `100 GiB` | 每 API Key 的有效输入对象加活跃 reservation 总字节 |
| `async_image.download_max_bytes` | `32 MiB` | `64 MiB` | 单图与请求有效图片负载上限；multipart wire body 只额外允许固定协议开销 |
| `async_image.upload_timeout_seconds` | `300` | `600` | 单次 SC 图片写入 OSS 的硬超时；必须短于 15 分钟 reservation lease |
| `async_image.input_retention_hours` | `24` | `720` | 上传输入对象最长保留时间 |

`413` 表示上传超过有效图片上限；额度不足返回 `409`，SC `error.type` 为 `async_image_upload_byte_quota`；限频返回 `429 async_image_upload_rate_limited` 并带 `Retry-After: 60`。

#### 上传幂等与重签

`Idempotency-Key` 可选，最多 255 字节，范围是“同一 API Key + 同一幂等键”。请求指纹绑定原始图片字节、声明 MIME、净化后的文件名和字节数：

- 同一幂等键、相同请求且输入对象仍有效：不重新上传 OSS，返回原对象的新签名 URL，并设置 `X-Idempotency-Replayed: true`。原 URL 与每次重签 URL 都可以在各自有效期内用于后续 `image_urls`。
- 同一幂等键、不同请求：返回 `409 async_image_upload_idempotency_conflict`。
- 相同上传仍在处理：返回 `409 async_image_upload_in_progress` 和 `Retry-After: 60`。
- 已完成上传的对象已经过期、被清理或进入清理认领：reservation 作为短期结果墓碑返回 `409 async_image_upload_result_unavailable`，客户端必须使用新的 `Idempotency-Key`，不能用旧键静默创建新对象。

每个重签 URL 只保存 SHA-256 alias。任务提交解析原始 URL hash 和 alias 时都会再次检查 API Key 所有权、对象过期时间与清理状态；已知但失效或属于其他 Key 的 alias 会被拒绝，不会被降级成普通外部 URL。每个输入对象最多保留 `128` 个 alias；注册时先锁定输入对象行，相同 hash 只延长有效期，第 129 个新 hash 返回 `429 async_image_upload_alias_limit`。过期 alias 在输入对象删除前仍作为所有权墓碑保留并计数。

#### 对象意图与文件名安全

对象 key 由 API Key ID、reservation ID 和实际 MIME 对应扩展名确定，客户端文件名不会进入对象路径。完整校验后、写 OSS 前，服务端先把 provider、bucket、deterministic object key、MIME、字节和 checksum 作为 object intent 持久化；进程若在上传后、完成 reservation 前崩溃，保留任务可按 intent 精确删除孤立对象，而不会猜测前缀或重新上传。

上传调用受 `upload_timeout_seconds` 限制。过期或失败 intent 的恢复删除采用两次 OSS Delete：第一次成功后只记录删除次数和时间并保留 reservation/intent，至少十分钟后第二次 Delete 成功才删除恢复事实。这样即使一次超时 Put 在取消边界迟到提交，第二次删除仍会收敛对象；第一次删除后的 intent 继续计入 API Key 容量。

客户端文件名只用于响应元数据和请求指纹：服务端统一斜杠后只取 basename，删除控制字符、首尾空白并限制为 255 字节；空名称回退为按实际 MIME 生成的 `image.png/.jpg/.webp`。

### 7.2 提交文生图或图生图

```http
POST /v1/images/generations_sc
Authorization: Bearer <GEMINI_GROUP_API_KEY>
Content-Type: application/json
Idempotency-Key: sc-20260720-001
```

文生图可以省略 `image_urls`：

```json
{
  "model": "gemini-3-pro-image-preview",
  "prompt": "现代客厅，北欧风，自然光",
  "resolution": "2K",
  "size": "16:9"
}
```

图生图传入一个或多个参考图 URL：

```json
{
  "image_urls": [
    "https://getapib.org/image/9998215222749966-61ac6f26-55a3-4d4f-aa37-93461286910d-e7440568a6521799795c8e1feb93de35.jpg",
    "https://getapib.org/image/9998215222677541-0a42ff34-e8c6-4086-a45e-3840b9e69377-d44cee78b0c0334f29f54d99e05d3945.jpg"
  ],
  "model": "gemini-3-pro-image-preview",
  "prompt": "去掉图1中所有的车，植物和铺地细节参考图2，地面阴影参考图2",
  "resolution": "4K",
  "size": "3:2"
}
```

`model` 和 `prompt` 必填。图生图时 `image_urls` 为参考图 HTTPS URL 数组（顺序对应提示词中的图1、图2…）。

维度字段：

| 字段 | 说明 |
|---|---|
| `resolution` | 清晰度：`1K` / `2K` / `4K`（以及受控的 `0.5K`） |
| `size` | **推荐**：宽高比别名，如 `3:2`、`16:9`；也可传像素尺寸，如 `1080x1350`（约分为 `4:5`）；若未传 `resolution`，也可写 `2K` 表示清晰度 |
| `aspect_ratio` | 与 `size` 同义的比例字段；显式传入时优先于 `size` 的比例别名 |

`0.5K` 限制以及 `auto`（文生图与图生图均可）规则与 BB Gemini 相同。像素尺寸只用于推导 Gemini 支持的宽高比；上游会结合该比例与 `resolution` 生成，不保证返回精确像素尺寸。

成功受理返回 HTTP `200`，且响应体中的 `code` 也是数字 `200`：

```json
{
  "code": 200,
  "message": "success",
  "data": {
    "id": "asyncimg_0123456789abcdef",
    "status": "pending",
    "type": "image",
    "progress": 0
  }
}
```

### 7.3 查询任务

```http
GET /v1/tasks_sc/{task_id}
Authorization: Bearer <提交任务的同一个 API_KEY>
```

排队中：

```json
{
  "code": 200,
  "data": {
    "id": "asyncimg_0123456789abcdef",
    "status": "pending",
    "progress": 0,
    "type": "image"
  }
}
```

处理中：

```json
{
  "code": 200,
  "data": {
    "id": "asyncimg_0123456789abcdef",
    "status": "processing",
    "progress": 60,
    "type": "image"
  }
}
```

成功：

```json
{
  "code": 200,
  "data": {
    "id": "asyncimg_0123456789abcdef",
    "status": "completed",
    "progress": 100,
    "type": "image",
    "result": {
      "images": [
        {
          "url": [
            "https://storage.example.com/images/results/output-1.png"
          ],
          "expires_at": 1784552400
        }
      ],
      "videos": []
    }
  }
}
```

`result.images[].url` 始终是字符串数组。使用私有桶时，`expires_at` 是该次查询新生成的签名 URL 的实际 Unix 过期秒数；使用 `image_storage.public_base_url` 公开直链时为 `0`。`videos` 固定为空数组，仅用于保持 SC 图片响应结构，不表示本期支持视频。

失败：

```json
{
  "code": 200,
  "data": {
    "id": "asyncimg_0123456789abcdef",
    "status": "failed",
    "progress": 60,
    "type": "image",
    "error": {
      "message": "image generation failed",
      "type": "task_failed"
    },
    "failReason": "image generation failed"
  }
}
```

查询成功、处理中和任务失败都使用 HTTP `200`。以 `data.status` 为准：`pending` / `processing` 继续轮询，`completed` 消费图片，`failed` 停止并读取错误。

## 8. 状态、恢复与成功条件

内部持久状态及含义如下：

| 内部状态 | 含义 | BB / SC 对外状态 |
|---|---|---|
| `queued` | 已写入 PostgreSQL 与 Outbox，等待 Worker | `queued` / `pending` |
| `invoking` | 正在通过现有 Gemini/OpenAI 链路调用上游 | `processing` / `processing` |
| `upstream_succeeded` | 上游图片与固定计费计划已进入短期暂存 | `processing` / `processing` |
| `uploading` | 正在上传生成物 | `processing` / `processing` |
| `billing_pending` | 图片已持久化，等待扣费确认 | `processing` / `processing` |
| `succeeded` | OSS 清单已保存且扣费已确认 | `succeeded` / `completed` |
| `storage_failed` | 上传/存储后处理失败，未超过上限时可续跑 | `processing`，耗尽重试后 `failed` |
| `billing_failed` | 固定账单命令应用失败，未超过上限时可续跑 | `processing`，耗尽重试后 `failed` |
| `failed` / `expired` | 执行失败或任务过期 | `failed` / `failed` |
| `execution_unknown` | 上游请求发出后进程中断，无法确认是否产生结果 | `failed` / `failed` |

只有 OSS 结果清单已持久化且账务状态已确认，任务才对外显示成功。标准模式要求原计费入口确认成功；全站 `simple` 模式沿用项目现有“不扣费但记录用量”语义，以 `not_billable` 作为已确认终态。因而 `processing` 可能表示图片已经生成但仍在上传或结算，客户端必须继续轮询。

单次上游调用受 `async_image.execution_timeout_seconds` 限制（默认 `1200`，即 20 分钟）。Worker 会对该次 invoke 使用 `context` 超时；若上游忽略取消、心跳又不断刷新 `updated_at`，恢复环仍会按 `started_at`（没有则用 `created_at`）的墙钟时间把仍为 `invoking` 的任务标记为 `failed`，`error_code=execution_timeout`。对外 BB `fail_reason` / SC `failReason` 为可读超时文案。配置文件与后台图片存储设置里的异步运行参数均可调整；已保存的后台设置优先于配置文件。`async_image.auto_archive_to_library` 默认关闭：成功结果仍会保存到持久化图片存储，任务查询照常返回 `data[].url`，但不会创建个人图库记录或触发图库逻辑字节配额；开启后才执行幂等图库归档。

标准 Gemini/OpenAI 同步上游没有可依赖的幂等保证。系统不会自动重新调用处于 `execution_unknown` 的任务，以免生成第二份图片和产生第二次上游费用。自动重试和管理员“续跑”只处理存储、计费和用量日志等后处理阶段，不会在原任务号下重新生成。

## 9. 计费语义

异步生图没有新的价格公式，完整复用当前分组计费规则和原有执行链路，包括：

- Gemini 分组 `1K` / `2K` / `4K` 图片单价。
- 普通图片倍率或独立图片倍率、用户专属分组倍率、账号倍率与高峰倍率。
- OpenAI 原生数量、尺寸、质量等实际用量。
- 订阅、余额、API Key 额度、账号额度及现有资格检查。

提交成功只表示任务已持久化，不表示最终一定能执行，也不会新增余额冻结规则。Worker 调用上游前会重新加载 API Key、用户、分组和订阅上下文；如果 Key 被禁用、换组、分组平台变化、普通生图或异步开关关闭，任务会在调用上游前失败且不计费。

上游成功后，系统按当时的现有规则和已解码图片的实际宽高准备一份不可变账单命令。存储或计费重试只应用这份固定命令，不会因后台改价或高峰时段变化重新计算。存储与计费分别维护重试次数，互不消耗对方的配置预算。任务账务键固定且带指纹校验，重复队列投递或重复 Apply 不会重复扣费。计费失败只重试结算，绝不重新调用上游。

API Key 在调用上游前仍会执行活动状态、分组与额度复查。上游已经开始后，即使用户软删除该 Key，系统仍会通过仅供固定账单使用的 tombstone 读取路径结算已准备命令；该路径不能用于鉴权，也不会让已删除 Key 恢复可调用状态。

## 10. 对象存储与 URL

全站同时使用一个当前图片存储供应商：

| `image_storage.provider` | 用途 | 端点行为 |
|---|---|---|
| `qiniu` | 七牛云 Kodo 的 S3 兼容接口 | `endpoint` 留空时按 `region` 生成 |
| `aliyun` | 阿里云 OSS | `endpoint` 留空时按 `region` 生成 |
| `tencent` | 腾讯云 COS | `endpoint` 留空时按 `region` 生成 |
| `custom_s3` | AWS S3、Cloudflare R2、MinIO 及其他 S3 兼容存储 | 可自定义 `endpoint`、`region`、寻址方式 |

任务表保存 `provider`、`bucket`、`object_key`、MIME、字节数、校验和及宽高，不保存会过期的预签名 URL。每次查询都会根据当前权限动态生成：

- 配置 `image_storage.public_base_url`：返回稳定公开 CDN URL，SC `expires_at` 为 `0`。
- 未配置公开地址：返回实时 presigned URL，有效期由 `async_image.signed_url_expiry_seconds` 控制。

后台“测试连接”不是只检查字段格式，而是实际执行探测对象的上传、HEAD/读取和删除；任一步失败都不能视为可用配置，启用配置在保存前也会执行同一探测。保留策略默认按参考图 24 小时、任务与结果 90 天执行；清理器先认领数据库记录并删除 OSS，成功后才删除对应记录，活动任务引用的输入对象不会提前删除。实际值由 `async_image` 保留参数控制。生产环境还应给存储桶配置生命周期规则作为兜底，且生命周期不得短于 Sub2API 配置值。

存在任何非 `deleted` 结果/图库对象、任意 SC 输入对象，或尚有已持久化 object intent 的上传 reservation 时，服务端拒绝改变 provider、bucket、endpoint、region 或 path-style 存储身份，避免历史输入或孤立对象无法恢复。完整历史多凭证 resolver 仍未实现，切换身份前必须先迁移或清理全部关联对象。

迁移 `187_ZJ_async_image_upload_reservations.sql` 为 SC 上传增加三张 PostgreSQL 事实表：

| 表 | 作用 |
|---|---|
| `async_image_upload_attempts` | body 解析前的滚动限频 attempt 与 admission 消费状态 |
| `async_image_upload_reservations` | 活跃字节 reservation、幂等状态、结果墓碑和 object intent |
| `async_image_input_url_aliases` | 原 URL 与重签 URL hash 到同一输入对象的所有权关联 |

`async_image.gemini_half_k_models` 配置显式支持 `0.5K` 的 Gemini 模型列表；例如 `gemini-image-*` 表示模型名前缀。`async_image.prompt_preview_enabled` 控制是否保存任务中心提示摘要，`async_image.prompt_preview_max_chars` 控制长度；摘要会先对 API Key、token、secret、authorization 等常见敏感片段脱敏，完整规范化请求仍只以加密形式保存并在任务终态清除。

## 11. 参考图安全限制

Gemini BB 的 `image_url` 和 SC 的 `image_urls`：

- HTTPS 参考图在 Worker 内只做绝对 HTTPS + 公网主机校验后透传上游（`fileData.fileUri`），不再本机下载解码转 base64。
- `data:image/...;base64,...` 仍在本机校验并转为 Gemini `inlineData`。
- 仅允许绝对 HTTPS URL 或受限的 `data:` URI，不接受普通 HTTP。
- DNS 解析会拒绝内网、回环、链路本地、多播、未指定及保留地址，防止 SSRF。
- SC multipart 上传仍使用字节、像素、MIME 和解码校验，并在完整解码/OSS 前通过 PostgreSQL 两阶段 admission。
- OpenAI `image_urls` / `images[].image_url` 本身即上游 URL 透传，网关不会本机转 base64。

规范化请求体加密写入 PostgreSQL，任务终态会清除完整请求载荷，只保留请求哈希和截断后的提示摘要。提示摘要仍可能包含业务敏感文本，应按敏感数据保护任务库和管理员页面。数据库不保存原始 API Key，Worker 只按 API Key ID 重新加载上下文；对外错误会经过日志脱敏规则处理，不透出上游凭证或内部地址。

## 12. 错误响应

常见 HTTP 状态：

| HTTP 状态 | 场景 |
|---|---|
| `400` | JSON/multipart 无效、缺少模型或提示、`stream=true`、非法尺寸/比例、上传文件无效 |
| `401` | API Key 无效 |
| `403` | 分组平台不匹配、普通生图关闭或异步生图关闭 |
| `404` | 任务不存在、方言不匹配，或不是提交任务的同一个 API Key |
| `409` | 幂等键请求冲突/仍在处理/结果墓碑不可重放，或 SC 输入字节额度不足 |
| `413` | 请求体、上传或参考图超过配置上限 |
| `429` | SC 上传超过每 API Key 的滚动限频；响应带 `Retry-After: 60` |
| `503` | 对象存储、加密密钥、异步运行配置或 PostgreSQL 上传 admission 不可用 |

BB 错误格式：

```json
{
  "error": {
    "type": "async_image_generation_disabled",
    "code": "async_image_generation_disabled",
    "message": "asynchronous image generation is not enabled for this group"
  }
}
```

SC 错误格式：

```json
{
  "code": 403,
  "message": "asynchronous image generation is not enabled for this group",
  "data": null,
  "error": {
    "type": "async_image_generation_disabled",
    "message": "asynchronous image generation is not enabled for this group"
  }
}
```

任务查询的 `failed` 是业务终态并使用 HTTP `200`；上述非 `200` 错误用于请求本身无法被鉴权、验证或处理的情况。
