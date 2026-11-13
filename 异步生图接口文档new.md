# 异步生图接口文档（新）

本文按当前代码说明持久化异步生图接口。OpenAI 与 Gemini 使用各自的请求参数，共用受理、查询和失败响应。图片请求示例统一分为**文生图**和**图生图**两类；格式、尺寸等变化在参数表中说明。

示例以 `https://api.example.com` 表示服务地址，`YOUR_API_KEY` 表示调用密钥，`https://cdn.example.com/reference.png` 表示可访问的参考图。接入时替换为实际值。

## 1. 接口与鉴权

| 用途 | 方法 | 路径 | 正常响应 |
|---|---|---|---|
| OpenAI 文生图 / 图生图 | `POST` | `/v1/images/generations_oa` | `202 Accepted` |
| OpenAI 显式图生图 | `POST` | `/v1/images/edits_oa` | `202 Accepted` |
| Gemini 文生图 / 图生图 | `POST` | `/v1/images/generations_sc` | `202 Accepted` |
| 查询上述任务 | `GET` | `/v1/images/tasks_async/{task_id}` | `200 OK`，检查 `status` |

请求使用以下头部；multipart 上传时让客户端自动设置带 boundary 的 `Content-Type`。

```http
Authorization: Bearer YOUR_API_KEY
Content-Type: application/json
Idempotency-Key: YOUR_REQUEST_ID
```

启用条件：有效 API Key、对应平台的计费分组、分组同时开启图片生成与异步生图，以及可用的结果存储。平台由提交路径决定：`_oa` 对应 OpenAI，`_sc` 对应 Gemini；Key 的平台生图分组映射优先于主分组，详见[双平台生图映射](wiki-new/API密钥双平台生图映射.md)。异步生图未开启时返回 `403`，不会自动改为同步调用。余额、订阅、额度与并发检查仍然生效。

`Idempotency-Key` 可选，最长 255 字节，建议为每个新请求生成唯一值。同 API Key、同幂等键、同平台/路径和相同原始请求体返回原任务；不同请求体或路径返回 `409`。重试提交时保留请求字节，包括空白、字段顺序及 multipart boundary。修改格式、尺寸或帧参数后使用新幂等键；省略此头会新建任务，网络重发可能重复生成。

查询必须使用提交任务的同一个 API Key；其他有效 Key 查询返回 `404`。不要在 URL 中传密钥。

## 2. 图片请求示例

以下四个 JSON 都是完整请求体，按平台选用对应路径。静态图示例统一使用 `1K`、`1:1`；OpenAI 显式请求 PNG，Gemini 使用其原生图片输出，不传 OpenAI 格式参数。

### 2.1 文生图

OpenAI：`POST https://api.example.com/v1/images/generations_oa`

```json
{
  "model": "gpt-image-2",
  "prompt": "窗边的一只橘猫，柔和自然光",
  "n": 1,
  "resolution": "1K",
  "aspect_ratio": "1:1",
  "output_format": "png"
}
```

Gemini：`POST https://api.example.com/v1/images/generations_sc`

```json
{
  "model": "gemini-3-pro-image-preview",
  "prompt": "窗边的一只橘猫，柔和自然光",
  "resolution": "1K",
  "aspect_ratio": "1:1"
}
```

文生图不传 `image_urls`。OpenAI 的统一入口也将 `null`、空数组或全空字符串视为没有参考图；Gemini 不接受数组内的空字符串。

### 2.2 图生图

OpenAI：`POST https://api.example.com/v1/images/edits_oa`，必须提供有效参考图。也可使用统一入口 `/v1/images/generations_oa`，有参考图时自动进入编辑流程。

```json
{
  "model": "gpt-image-2",
  "prompt": "保留主体，将背景改为夜景",
  "image_urls": ["https://cdn.example.com/reference.png"],
  "n": 1,
  "resolution": "1K",
  "aspect_ratio": "1:1",
  "output_format": "png"
}
```

Gemini：`POST https://api.example.com/v1/images/generations_sc`

```json
{
  "model": "gemini-3-pro-image-preview",
  "prompt": "保留主体，将背景改为夜景",
  "image_urls": ["https://cdn.example.com/reference.png"],
  "resolution": "1K",
  "aspect_ratio": "1:1"
}
```

`image_urls` 是字符串数组，多张参考图按数组顺序对应提示词中的图 1、图 2。支持公网 HTTPS URL、可校验的图片 data URI，以及同 API Key 上传且仍有效的本站参考图 URL。参考图使用 PNG、JPEG 或 WebP；GIF/SVG 输出能力不会扩展输入格式。图片数量、字节、像素及下载限制受模型和运行配置约束。

远程参考图可以由上游拉取，也可以按平台的运行配置由本站下载后转发；指定错误还可能触发本地文件回退。不要假设参考图永远不经过本站下载，也不要依赖固定重试次数。

OpenAI 还支持 multipart 图生图：把同一请求的标量参数写为同名表单字段，并以 `image=@reference.png` 上传文件，替代 JSON 的 `image_urls`。多图可使用 `image[]` 文件字段；不需要手工填写 boundary。GIF/SVG 扩展的表单字段见下表。

## 3. 参数说明

### 3.1 公共字段与平台差异

| 字段 | 类型 | 必填 | OpenAI | Gemini |
|---|---|---|---|---|
| `model` | string | Gemini 必填；OpenAI 建议显式提供 | 使用分组支持的图片模型；省略时本站默认 `gpt-image-2` | 使用分组支持的 Gemini 图片模型 |
| `prompt` | string | 是 | 画面描述或编辑指令 | 非空画面描述或编辑指令 |
| `image_urls` | string[] | 图生图需要 | 有有效值进入编辑流程；multipart 可改用文件 | 有参考图时进入图生图；空值元素无效 |
| `resolution` | string | 否 | `1K` / `2K` / `4K` 清晰度档位 | `1K` / `2K` / `4K`；`0.5K` 仅对运行配置明确开启的模型可用 |
| `aspect_ratio` | string | 否 | 比例或 `auto`，与 `resolution` 一起解析上游尺寸 | 比例或 `auto` / `自动`；省略时不指定上游比例 |
| `size` | string | 否 | 像素 `WIDTHxHEIGHT`、`auto`、比例或清晰度档位别名 | 比例、可约分为支持比例的像素尺寸，或清晰度档位别名；像素写法不保证成品精确像素 |
| `n` | integer | 否 | 默认 `1`，表示结果文件数量，仍受上游限制 | 此 SC 请求方言不提供多图数量控制 |
| `quality` | string | 否 | 例如 `low` / `medium` / `high`，以模型支持为准 | 不属于此 SC 请求方言 |
| `background` | string | 否 | 以模型及输出格式支持为准 | 不属于此 SC 请求方言 |
| `mask.image_url` | string | 否 | 编辑遮罩；multipart 可上传 `mask` 文件，支持范围取决于上游 | 不属于此 SC 请求方言 |
| `response_format` | string | 否 | 按上游协议处理；本站异步成功结果仍返回 URL | 不属于此 SC 请求方言 |
| `stream` | boolean | 否 | 省略或 `false`；`true` 在入队前拒绝 | SC 接口不提供流式结果，请省略 |

OpenAI 与 Gemini 的 `size` 语义不同，建议每次只选一组尺寸写法。OpenAI 未提供显式 `WIDTHxHEIGHT` / `auto`、需要用比例（`aspect_ratio` 或 `size` 比例别名）计算尺寸时，必须同时提供 `resolution`；仅有 `resolution` 时按方图解析。显式 `WIDTHxHEIGHT` / `auto` 优先于比例字段；像素尺寸与冲突的 `resolution` 会被拒绝，`size:auto` 则交由上游决定尺寸。比例别名与 `aspect_ratio` 同时存在时以显式比例字段为准。Gemini 显式 `resolution` / `aspect_ratio` 优先于 `size` 别名。

常用比例为 `1:1`、`2:3`、`3:2`、`3:4`、`4:3`、`4:5`、`5:4`、`9:16`、`16:9`、`21:9`、`9:21`。OpenAI 还接受 `2:1`、`1:2`；实际尺寸与模型限制需要匹配。GPT Image 2 / 2.5 的 4K 映射与校验见[尺寸专题](wiki-new/图片渠道错误与换号重试诊断.md#2026-11-05-gpt-image-4k-等比映射本地实现)，不要将静态图尺寸表直接套用于多帧 GIF。

### 3.2 OpenAI GPT Image 2.6+ 格式扩展

**新增 GIF、SVG 输出及帧参数仅属于 OpenAI GPT Image 2.6+ 系列能力。当前版本明确开放 `gpt-image-2.6-flare`、`gpt-image-2.6-sunburst`；后续型号仍需网关明确支持，不能仅凭名称或版本号推断。** 这组扩展不适用于 Gemini。旧 OpenAI 模型保留既有 PNG/WebP/JPEG 等静态格式能力，本轮不为其开放 GIF/SVG。

| 字段 | JSON 类型 | 必填 | 默认值与规则 | multipart 字段 |
|---|---|---|---|---|
| `output_format` | string | 否 | 两款已开放模型支持 `png`（默认）/ `webp` / `jpeg` / `gif` / `svg` | `output_format` |
| `frame_count` | integer | 否 | 仅 GIF；默认 `9`，范围 `2`～`16`（含端点） | `frame_count`，十进制整数文本 |
| `frame_duration_ms` | integer | 否 | 仅 GIF；默认 `200` 毫秒，范围 `20`～`2000`；官方编码按最近的 10 毫秒取整 | `frame_duration_ms`，十进制整数文本 |
| `svg.model` | string | 条件需要 | 仅 SVG；启用作者模型选择的上游要求提供受支持的作者模型名，其余上游按要求省略。本站不设默认，也不把顶层图片模型当作作者 | `svg[model]` |
| `svg.reasoning.effort` | string / null | 否 | 仅 SVG；`none` / `minimal` / `low` / `medium` / `high` / `xhigh` / `max`，具体档位须作者模型支持；省略或 `null` 不指定 | `svg[reasoning][effort]`；不指定时省略表单字段 |

在第 2 节对应图片请求上修改参数即可，无需换接口或查询方式：

| 目标 | 参数变化 |
|---|---|
| PNG / WebP / JPEG 静态图 | 将 `output_format` 设为所需格式，不传帧参数或 `svg` 对象 |
| GIF | 使用当前开放的 2.6 模型；设 `output_format=gif`、`size=512x512` 或 `size=auto`，删除示例中的 `resolution` / `aspect_ratio`；按需设置帧数和间隔，提示词描述动作及循环 |
| SVG | 使用当前开放的 2.6 模型；设 `output_format=svg`、`n=1`，删除帧参数；仅在上游要求时加入 `svg` 对象，并先确认可用作者模型 |

JSON 的 SVG 字段为嵌套对象，例如以下**参数片段**；`SVG_AUTHOR_MODEL` 是待替换占位符，不是可用模型名。

```json
{
  "svg": {
    "model": "SVG_AUTHOR_MODEL",
    "reasoning": {"effort": "low"}
  }
}
```

注意以下边界：

- OpenAI 异步入口的帧参数不能使用 JSON 数字字符串、小数、布尔值或 `null`；使用默认值时省略字段。非 GIF 带帧参数、非 SVG 带 `svg` 对象、旧模型请求新格式/帧参数、非法枚举或范围会在建任务前返回 `400`。Gemini SC 不解析这些扩展字段，传入它们不会获得对应格式能力。
- `frame_count` 是每份 GIF 的帧数，`n` 是结果文件数。GIF 的 `size` 表示单帧尺寸；帧数、尺寸和 `n` 共同受上游拼图长边及总像素预算约束。建议从 `512x512` 或 `auto` 开始，超限时降低尺寸、帧数或 `n`。
- 官方 GIF 不支持遮罩、部分图流式输出或自定义压缩；SVG 使用 `n=1`，不应搭配流式、部分图或输出压缩。其他供应商的能力以其协议为准。
- SVG 作者模型是否可用取决于上游及项目权限；目前 SVG 真实产出和编辑能力尚未验证通过，参数可透传不代表账号具备能力。确认可用作者模型后再使用，边界与验证记录见[结果格式专题](wiki-new/异步生图结果转存与格式兼容.md)。

### 3.3 输出文件与参考图限制

成功查询统一通过 `data[].url` 返回完整文件，不内联 SVG，也不逐帧返回 GIF。按实际文件格式保留扩展名和 MIME：GIF 为 `.gif` / `image/gif`，SVG 为 `.svg` / `image/svg+xml`；不要统一另存为 PNG。

结果受字节和像素限制。GIF 最多 16 帧，画布和所有帧矩形的累计像素均受现有下载像素上限约束。SVG 仅接受有有效尺寸或 `viewBox` 的静态、自包含内容；脚本、事件、外部资源、嵌入栅格图、动画及 DTD/实体等会被拒绝。参考图与普通上传仍仅使用 PNG、JPEG、WebP。完整说明见[结果转存与格式兼容](wiki-new/异步生图结果转存与格式兼容.md)。

## 4. 受理、查询与下载

提交成功返回 `202`：

```json
{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "https://api.example.com/v1/images/tasks_async/asyncimg_0123456789abcdef"
}
```

返回头包含 `Cache-Control: no-store`、`Location` 和 `Retry-After: 3`。`query_url` 按部署的公共地址设置生成；未配置绝对地址时可能返回相对路径。客户端优先使用该地址，携带同一 API Key 轮询。

```http
GET /v1/images/tasks_async/asyncimg_0123456789abcdef
Authorization: Bearer YOUR_API_KEY
```

| `status` | 含义 | 客户端处理 |
|---|---|---|
| `queued` | 等待执行或重新排队 | 继续轮询 |
| `processing` | 生成、结果转存或计费确认中 | 继续轮询 |
| `succeeded` | 成功终态 | 下载 `data[].url` |
| `failed` | 失败终态 | 展示 `error_code` 与 `fail_reason` |

未完成时响应为 `status` 和 `task_id`；排队/处理中会提供 `Retry-After: 3`。建议首次等待 3 秒，再按 3～30 秒退避查询。HTTP `200` 只表示查询成功，不能作为图片生成成功的依据。

成功查询：

```json
{
  "status": "succeeded",
  "task_id": "asyncimg_0123456789abcdef",
  "data": [{"url": "https://cdn.example.com/images/results/output-1.png"}]
}
```

结果 URL 可能是公开地址或带有效期的签名地址。URL 有效期与文件保留期不同，均由部署配置决定。管理员可按小时或天设置生成结果保留期，按间隔或每日定时清理，也可手动清理指定时间以前的结果。生成结果不保证永久保存，客户端应在成功后及时下载或转存，公开地址同样可能因文件清理而失效。

清理不会把已成功的生成改成失败。在任务记录仍可查询的前提下，部分结果已清理时，`status` 仍为 `succeeded`，`data` 只包含剩余结果 URL；全部结果已清理时，返回 `status: "succeeded"` 和 `data: []`。不要据此自动重新提交生成请求。图库或广场的有效引用会保护底层文件，但不保证旧任务结果行或旧任务 URL 继续保留；任务记录自身还受独立保留策略控制。

本系统生成结果正在被本系统活动图生图任务引用时受保护；已清理或正在清理的结果 URL 不可再用于新任务的参考图。外部 URL 仍按原有参考图规则验证，不承诺识别或保护未知的外部 CDN 别名。

未结束的任务可能正在结果上传或计费确认，请继续查询原任务，不要再次提交相同生成请求。

管理员可启用[生图分辨率保护](wiki-new/生图分辨率保护与告警.md)：真实成品像素面积低于模型目标的配置比例时，对最终出图账号的同模型、同档位进行冷却并发送管理员告警。默认比例75%、一次异常、冷却600秒，功能默认关闭；客户端无需增加请求字段。当前成品仍正常返回，沿用现有计费，不自动退款、降价或重新生成；无法判定真实尺寸时不触发质量冷却。因此 `succeeded` 不代表精确尺寸验收通过，客户端仍可按自身要求检查下载结果。

目标尺寸同时考虑模型、档位及请求宽高比；例如4K的16:9与9:16分别使用各自的目标宽高，不统一按4096×4096或单条边长判断。保护比较像素面积，默认容许轻微尺寸差异，不验证构图方向。处于质量冷却的账号仅在匹配模型和档位时被跳过，可由管理员提前解除。

## 5. 失败处理

### 5.1 提交或查询接口错误

| HTTP | 典型原因 |
|---|---|
| `400` | 参数、尺寸、输出格式或参考图无效，OpenAI 请求了流式，扩展参数类型/范围错误 |
| `401` | 缺少或无效 API Key |
| `403` | 对应平台计费分组不可用，图片生成或异步生图未开启 |
| `404` | 任务不存在，或不是提交任务所用的 Key |
| `409` | 幂等冲突；上传预留处理中或参考图字节额度不足等 |
| `413` | 请求体或参考图超限 |
| `429` | 准入/上传限频等；遵守 `Retry-After` |
| `500` / `502` / `503` | 本地处理、上传、结果存储或运行依赖暂时不可用，按错误正文处理 |

异步 Handler 的业务错误形态：

```json
{
  "error": {
    "type": "async_image_generation_disabled",
    "code": "async_image_generation_disabled",
    "message": "asynchronous image generation is not enabled for this group"
  }
}
```

入口鉴权中间件还可能返回顶层 `code` / `message`，客户端应兼容这两种错误结构。例如缺少密钥为 `API_KEY_REQUIRED`，无效密钥为 `INVALID_API_KEY`：

```json
{
  "code": "INVALID_API_KEY",
  "message": "Invalid API key"
}
```

### 5.2 已受理任务失败

查询失败任务仍返回 HTTP `200`，例如：

```json
{
  "status": "failed",
  "task_id": "asyncimg_0123456789abcdef",
  "error_code": 604,
  "fail_reason": "请求参数或参考图无效，请检查任务返回的具体错误。"
}
```

`error_code` 是应用层分类，**不是 HTTP 状态码**。`fail_reason` 包含脱敏后的实际原因，应展示原文并保留任务 ID；不要把所有错误替换为固定提示。

| `error_code` | 含义 | 建议 |
|---:|---|---|
| `601` | 内容安全或内容政策拦截 | 修改提示词或参考图后再提交 |
| `602` | 参考图拉取失败 | 检查 URL 可访问性与有效期，或上传参考图 |
| `603` | 账号或上游容量耗尽 | 检查容量并适当退避 |
| `604` | 请求参数、尺寸、格式或参考图无效 | 根据原文修正请求 |
| `605` | 上游限流 | 退避，避免密集重发 |
| `606` | 上游暂时不可用 | 根据任务结果和原文处理 |
| `607` | 上游图片输出无法解析或校验 | 检查输出格式和上游能力 |
| `608` | 超时或执行结果未知 | 不自动重复生成，先核对原任务 |
| `609` | 存储或计费后处理失败 | 联系管理员处理已有结果，不重新生图 |
| `610` | 未分类错误 | 保留原文和任务 ID 排查，勿盲目重发 |
| `611` | 参考图数量超过上游限制 | 按所选模型和错误原文减少数量 |
| `612` | 缺少或无法使用所需参考图 | 重新提供有效参考图 |
| `613` | 提示词或输入图片无法被上游处理 | 按原文调整输入 |

### 5.3 重试边界

服务器按错误分类、运行配置及任务预算处理可重试错误，可能重新排队、切换兼容渠道，或对特定参考图错误执行本地文件回退；没有对所有错误通用的固定重试次数。公共查询仅返回上述状态、结果或失败原因，不包含内部 `retry_count`、账号尝试或时间线。

上述持久异步文生图、图生图入口收到完整的上游 HTTP `400`～`599` 错误响应，且错误消息包含「超时」、`timeout`、`time out`、`timed out` 或 `context deadline exceeded` 等超时语义时，优先尝试其他兼容渠道；英文匹配不区分大小写。此规则检查错误消息，不因错误码、参数名或 URL 中出现这些词就重试。响应包含审核、已产图、已收费、执行结果未知、禁止重试或永久输入错误（如缺提示词、坏图/格式或受限地址）等终止详情时仍停止；未收到完整 HTTP 响应的网络超时不能据此确认生成失败，继续按执行结果未知等既有规则处理。

图生图收到上游 HTTP `404`、`error.code=404`，且完整消息为「文件引用不存在: HTTPS参考图URL」时，按参考图获取失败处理；普通 `404` 不适用。HTTP `454` / `asset_download_timeout`「下载参考图超时，请稍后重试」也属于可识别超时，其既有参考图回退路径优先保留。这些已识别参考图失败在 `passthrough_fallback_local` 模式下，先持久化失败账号审计和本地传输模式，由本站下载、校验参考图，再提交文件输入；`passthrough` 模式直接走现有选路。其他通用超时不自动更改参考图传输模式。

候选渠道耗尽时沿用现有有限后备重试，可能再次使用原渠道，仍受重试预算、实际调用次数和任务截止时间限制，不能保证一定换渠道成功。客户端继续查询原 `task_id`；同步请求不因上述新增规则改变，历史失败任务不会自动重跑。

结果获取、校验、存储或计费阶段的失败不会通过重新生成图片来恢复；执行结果未知也禁止自动重调上游。客户端遇到提交响应丢失时，应携带原幂等键原样重试受理请求，取得原任务后继续查询。实现细节见[异步生图架构](wiki-new/异步生图架构.md)与[渠道错误及重试说明](wiki-new/图片渠道错误与换号重试诊断.md)。

## 6. 辅助接口与兼容入口

### 6.1 参考图上传

`POST /v1/uploads/images_sc` 使用 multipart 字段 `file`，要求 API Key 有可用的 Gemini 平台生图计费分组。上传成功后将返回的 URL 放入图生图请求的 `image_urls`；使用同一个 API Key，且在输入对象有效期内引用。

```bash
curl 'https://api.example.com/v1/uploads/images_sc' \
  -H 'Authorization: Bearer YOUR_API_KEY' \
  -H 'Idempotency-Key: YOUR_UPLOAD_ID' \
  -F 'file=@reference.png;type=image/png'
```

成功返回 HTTP `200`：

```json
{
  "url": "https://cdn.example.com/images/inputs/reference.png",
  "filename": "reference.png",
  "content_type": "image/png",
  "bytes": 12345,
  "created_at": 1793923200
}
```

支持 PNG/JPEG/WebP，声明 MIME 应与文件一致。上传限频、文件大小及每 Key 的输入字节额度由部署配置控制；重复命中有效上传幂等结果时返回 `X-Idempotency-Replayed: true`。过期、跨 Key 或已进入清理流程的输入对象会被拒绝。

### 6.2 当日统计

`GET /v1/images/tasks_async/stats`，使用相同的 Bearer 鉴权。统计范围是 Key 所属用户当天的持久化异步任务，不限当前 Key；日期按服务器配置时区计算，已归入 `expired` 的任务不计入公开请求总数。余额耗尽但仍可通过此接口鉴权的 Key 可以查询统计。

```json
{
  "object": "async_image.stats",
  "date": "2026-11-06",
  "timezone": "Asia/Shanghai",
  "balance": 12.5,
  "today_requests": 20,
  "success_count": 15,
  "failure_count": 3,
  "success_rate": 83.3333333333
}
```

`today_requests` 为活动、成功和失败分类的数量之和，含未完成任务；`failure_count` 包含失败、执行未知及存储/计费失败。存储/计费失败可能仍在自动恢复，因此统计中的失败分类不等于公共查询已经显示 `failed`。`success_rate` 为成功数除以成功与失败数之和的百分比，分母为 `0` 时返回 `0`。响应带 `Cache-Control: no-store`；读取失败返回 `500 / stats_unavailable`，实例未提供统计能力返回 `503 / stats_unavailable`。

### 6.3 兼容入口

| 入口 | 约定 |
|---|---|
| `POST /v1/images/edits_oa` | 持久化 OpenAI 显式编辑入口，必须有参考图；结果仍查询 `tasks_async` |
| `GET /v1/tasks_sc/{task_id}` | 持久化任务查询兼容路径，当前响应与 `tasks_async` 一致 |
| `POST /v1/chat/completions_gm` | Gemini 的 BB 消息方言，使用用户消息而非本文 SC 的 `prompt` 结构；沿用相应受理字段，任务可通过 `tasks_async` 查询 |
| `POST /v1/images/generations/async`、`POST /v1/images/edits/async` | 旧式异步接口；支持已实现的 OpenAI 格式参数，但保留旧受理/结果结构，并使用 `GET /v1/images/tasks/{task_id}` 查询 |

推荐新接入使用第 1 节的持久化入口。不要把旧式 `/images/tasks/` 与持久任务 `/images/tasks_async/` 混用，也不要把其他网关的 `_async` 提交命名当成本服务已提供的路径。

### 6.4 运维与专题文档

管理员任务中心为 `/admin/async-image-tasks`；内部状态、尝试记录、参考图 URL 审计及人工处理不属于本文公共查询契约。

- [异步生图架构、状态机与恢复](wiki-new/异步生图架构.md)
- [图片结果转存、格式限制与验证记录](wiki-new/异步生图结果转存与格式兼容.md)
- [图片渠道错误、换号重试与尺寸诊断](wiki-new/图片渠道错误与换号重试诊断.md)
- [异步生图早期运行诊断归档](wiki-new/异步生图早期运行诊断归档.md)：原主文档中的历史只读快照和当时验证，不能作为当前运行状态
