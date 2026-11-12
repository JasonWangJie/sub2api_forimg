export interface ApiParam {
  name: string
  type: string
  required: string
  desc: string
}

export function getAsyncImageApiDoc(locale: string, apiRoot: string) {
  const root = apiRoot.replace(/\/+$/, '')
  const v1 = /\/v1$/i.test(root) ? root : `${root}/v1`
  const isEn = locale.toLowerCase().startsWith('en')
  const l = (zh: string, en: string) => isEn ? en : zh
  const json = (value: unknown) => JSON.stringify(value, null, 2)
  const optional = l('否', 'No')
  const required = l('是', 'Yes')
  const param = (name: string, type: string, need: string, zh: string, en: string): ApiParam => ({ name, type, required: need, desc: l(zh, en) })
  const textPrompt = l('窗边的一只橘猫，柔和自然光', 'An orange cat by a window, soft natural light')
  const editPrompt = l('保留主体，将背景改为夜景', 'Keep the subject and change the background to a night scene')
  const reference = 'https://cdn.example.com/reference.png'
  const topicUrl = 'https://github.com/JasonWangJie/sub2api_forimg/blob/main/wiki-new/' + encodeURIComponent('异步生图结果转存与格式兼容.md')
  const acceptExample = json({ task_id: 'asyncimg_0123456789abcdef', query_url: `${v1}/images/tasks_async/asyncimg_0123456789abcdef` })
  const commonFields = { resolution: '1K', aspect_ratio: '1:1' }
  const examples = [
    {
      id: 'text-to-image', title: l('文生图', 'Text-to-image'),
      summary: l('不传参考图。两种平台使用相同提示词与规格，选择与你的 API Key 图片平台配置匹配的入口。', 'Omit reference images. Both platforms use the same prompt and settings; choose the endpoint matching your API key image-platform configuration.'),
      platforms: [
        { platform: 'OpenAI', path: `${v1}/images/generations_oa`, body: json({ model: 'gpt-image-2', prompt: textPrompt, ...commonFields, n: 1, output_format: 'png' }) },
        { platform: 'Gemini', path: `${v1}/images/generations_sc`, body: json({ model: 'gemini-3-pro-image-preview', prompt: textPrompt, ...commonFields }) },
      ],
    },
    {
      id: 'image-to-image', title: l('图生图', 'Image-to-image'),
      summary: l('在同一结构中加入 image_urls。OpenAI 使用明确编辑入口 edits_oa；Gemini 仍使用 generations_sc。参考图顺序对应提示词中的图1、图2。', 'Add image_urls to the same structure. OpenAI uses the explicit edits_oa endpoint; Gemini keeps generations_sc. Reference order matches image 1, image 2 in the prompt.'),
      platforms: [
        { platform: 'OpenAI', path: `${v1}/images/edits_oa`, body: json({ model: 'gpt-image-2', prompt: editPrompt, image_urls: [reference], ...commonFields, n: 1, output_format: 'png' }) },
        { platform: 'Gemini', path: `${v1}/images/generations_sc`, body: json({ model: 'gemini-3-pro-image-preview', prompt: editPrompt, image_urls: [reference], ...commonFields }) },
      ],
    },
  ]

  return {
    title: l('异步生图 API', 'Async Image API'),
    subtitle: l('按文生图、图生图两种场景接入。提交任务后用同一把 API Key 查询，成功后下载结果。', 'Integrate text-to-image and image-to-image. Submit a task, poll with the same API key, then download the successful result.'),
    baseUrl: root,
    toc: [
      { id: 'overview', label: l('总览', 'Overview'), level: 1 },
      { id: 'auth', label: l('接入约定', 'Getting started'), level: 1 },
      { id: 'parameters', label: l('参数说明', 'Parameters'), level: 1 },
      { id: 'text-to-image', label: l('文生图', 'Text-to-image'), level: 1 },
      { id: 'image-to-image', label: l('图生图', 'Image-to-image'), level: 1 },
      { id: 'openai-26', label: l('OpenAI 2.6+ 扩展', 'OpenAI 2.6+ extensions'), level: 1 },
      { id: 'query', label: l('受理与查询', 'Acceptance and polling'), level: 1 },
      { id: 'limits', label: l('限制与重试', 'Limits and retries'), level: 1 },
      { id: 'errors', label: l('错误说明', 'Errors'), level: 1 },
      { id: 'compatibility', label: l('兼容与辅助接口', 'Compatibility and helpers'), level: 1 },
      { id: 'oss', label: l('结果下载', 'Result downloads'), level: 1 },
    ],
    overview: {
      bullets: [
        l('分组需同时开启图片生成与异步生图，且存储、余额或订阅、并发等检查通过。', 'The group must enable image generation and async image generation, with usable storage and passing balance/subscription and concurrency checks.'),
        l('路径确定图片平台：_oa 为 OpenAI，_sc 为 Gemini。API Key 的图片平台分组映射优先，未配置时才回退主分组。', 'The path selects the image platform: _oa is OpenAI, _sc is Gemini. API key image-platform group mappings take precedence; the primary group is the fallback.'),
        l('推荐接口统一返回 HTTP 202、task_id 与 query_url；查询结构与平台无关。', 'The recommended endpoints return HTTP 202, task_id and query_url with a shared polling schema.'),
      ],
    },
    auth: {
      title: l('接入约定', 'Getting started'),
      headers: 'Authorization: Bearer YOUR_API_KEY\nContent-Type: application/json\nIdempotency-Key: YOUR_UNIQUE_REQUEST_KEY',
      notes: [
        l('推荐使用 Bearer；同时兼容 x-api-key / x-goog-api-key，禁止在 URL 查询参数传 Key。', 'Bearer is recommended; x-api-key and x-goog-api-key are also accepted. API keys in URL query parameters are rejected.'),
        l('Idempotency-Key 可选，最多 255 字节。同一把 Key、同幂等键、同路径、同请求字节返回原任务；内容变化返回 409。改变参数应使用新幂等键。', 'Idempotency-Key is optional, up to 255 bytes. The same API key, idempotency key, path and exact request bytes return the original task; changed content returns 409. Use a new key for changed parameters.'),
        l('只查询提交任务的同一把 API Key 所有的任务。遵守 Retry-After（通常为 3 秒），不要因尚未完成而重复提交。', 'Poll only tasks owned by the submitting API key. Honor Retry-After (normally 3 seconds); do not resubmit because a task is still running.'),
      ],
    },
    parameters: {
      title: l('参数说明', 'Parameters'),
      intro: l('先看公共字段，再按平台选择专属参数。以下两类主示例为普通静态图片；Gemini 不使用 OpenAI 的输出格式和帧参数。', 'Start with the common fields, then use platform-specific options. The two main scenarios below generate ordinary static images; Gemini does not use OpenAI output-format or frame controls.'),
      groups: [
        {
          title: l('公共字段', 'Common fields'),
          params: [
            param('model', 'string', l('Gemini 是 / OpenAI 否', 'Gemini: yes / OpenAI: no'), '对应平台与分组可用的图片模型名。OpenAI 省略或空值时默认 gpt-image-2；建议两平台均显式填写。', 'An image model available on the selected platform and group. OpenAI defaults to gpt-image-2 when omitted or empty; explicitly set it on both platforms.'),
            param('prompt', 'string', required, '文生图的画面描述，或图生图的编辑指令。', 'Scene description for generation or edit instructions for image-to-image.'),
            param('image_urls', 'string[]', l('图生图需要', 'For image-to-image'), '参考图 URL 数组；文生图省略。支持公网 HTTPS、图片 data URI，以及同 Key 上传的有效图片 URL；参考图格式为 PNG/JPEG/WebP。', 'Reference image array; omit for text-to-image. Supports public HTTPS, image data URIs, and valid uploads owned by the same API key. Inputs are PNG/JPEG/WebP.'),
            param('resolution', 'string', optional, '清晰度档位 1K / 2K / 4K；受模型能力约束。', 'Resolution tier 1K / 2K / 4K, subject to model capability.'),
            param('aspect_ratio', 'string', optional, '推荐比例 1:1 / 3:2 / 2:3 / 4:3 / 3:4 / 16:9 / 9:16，也支持 auto；各平台完整比例范围见下表。', 'Common ratios: 1:1 / 3:2 / 2:3 / 4:3 / 3:4 / 16:9 / 9:16, or auto. Full platform ranges are listed below.'),
          ],
        },
        {
          title: l('OpenAI 专属字段与差异', 'OpenAI-specific fields and behavior'),
          params: [
            param('n', 'integer', optional, '默认 1；生成文件数量，仍受上游能力与预算限制。', 'Defaults to 1. Number of result files, subject to upstream capability and budget.'),
            param('size', 'string', optional, 'WxH（如 1024x1024）或 auto；也兼容比例、1K/2K/4K 别名。WxH 与 resolution 冲突会被拒绝，auto 优先。仅传比例时还需 resolution；只传 resolution 时默认 1:1。', 'WxH (such as 1024x1024) or auto; ratio and tier aliases also work. Conflicting WxH and resolution are rejected; auto takes precedence. Ratio-only input needs resolution; a resolution alone defaults to 1:1.'),
            param('aspect_ratio', 'string', optional, 'auto、1:1、2:3、3:2、4:5、5:4、4:3、3:4、16:9、9:16、21:9、9:21、2:1、1:2；比例形式与 size 同时存在时本字段优先。', 'auto, 1:1, 2:3, 3:2, 4:5, 5:4, 4:3, 3:4, 16:9, 9:16, 21:9, 9:21, 2:1, 1:2. Overrides a ratio alias supplied in size.'),
            param('quality / background', 'string', optional, '按模型支持传入画质和背景选项；不保证每个模型接受相同枚举。', 'Quality and background options depend on the model; supported values vary.'),
            param('output_format', 'string', optional, '普通静态格式 png / webp / jpeg，依模型支持。GIF/SVG 仅适用下方 2.6+ 扩展。', 'Static png / webp / jpeg, where supported. GIF/SVG are restricted to the 2.6+ extension below.'),
            param('image / mask', 'file', optional, 'multipart 文件字段；image 用于图生图，mask 为可选遮罩。JSON 遮罩使用 mask.image_url。', 'Multipart files: image supplies a reference; mask is optional. JSON masks use mask.image_url.'),
            param('stream', 'boolean', optional, '必须为 false 或省略；异步接口不支持流式。', 'Must be false or omitted; asynchronous tasks are not streaming requests.'),
            param('response_format', 'string', optional, '依上游兼容；本站异步最终仍通过 data[].url 返回结果。', 'Subject to upstream compatibility; task results are still returned as data[].url.'),
          ],
          notes: [l('generations_oa 带有效参考图时自动转为图生图；edits_oa 要求参考图。JSON 的 image_urls 为 null、空数组或全为空字符串时不视为有效参考图。', 'generations_oa switches to edit mode when valid references exist; edits_oa requires references. Null, an empty image_urls array or all-empty strings do not count as references.')],
        },
        {
          title: l('Gemini 专属字段与差异', 'Gemini-specific fields and behavior'),
          params: [
            param('size', 'string', optional, '支持比例或像素尺寸别名，例如 1080x1350 约分为 4:5；上游按比例和清晰度生成，不保证精确像素。未传 resolution 时可用 2K 等档位。', 'Accepts a ratio or pixel-size alias: 1080x1350 maps to 4:5. The upstream uses ratio and resolution tier, not guaranteed exact pixels. A tier such as 2K works when resolution is absent.'),
            param('aspect_ratio', 'string', optional, 'auto、1:1、2:3、3:2、4:5、5:4、4:3、3:4、16:9、9:16、21:9、9:21；显式值优先于 size 比例。auto 或省略使用上游默认比例。', 'auto, 1:1, 2:3, 3:2, 4:5, 5:4, 4:3, 3:4, 16:9, 9:16, 21:9, 9:21. Overrides a size ratio alias. auto or omission uses the upstream default ratio.'),
          ],
          notes: [
            l('image_urls 中不能包含空字符串。参考图可按运行配置透传 URL 或本地下载校验，客户端不应依赖固定传输模式。', 'image_urls must not contain empty strings. Runtime settings may forward URLs or download and validate locally; clients should not depend on one transport mode.'),
            l('SC 请求不提供 n、output_format、frame_count、frame_duration_ms 或 svg 控制；不要把 OpenAI 字段复制到 Gemini 请求。0.5K 仅对管理员明确启用的模型开放。', 'SC requests do not provide n, output_format, frame_count, frame_duration_ms or svg controls. Do not copy OpenAI controls into Gemini requests. 0.5K requires an explicitly enabled model.'),
          ],
        },
      ],
    },
    examples,
    extensions: {
      title: l('OpenAI GPT Image 2.6+ 专属扩展', 'OpenAI GPT Image 2.6+ extensions'),
      intro: l('GIF、SVG 与帧参数属于 OpenAI GPT Image 2.6+ 系列能力。当前版本仅开放 gpt-image-2.6-flare、gpt-image-2.6-sunburst；未来型号必须由网关明确支持，不会按版本名称自动放行。它们仍属于上面的文生图或图生图场景。', 'GIF, SVG and frame controls belong to the OpenAI GPT Image 2.6+ family. This version enables only gpt-image-2.6-flare and gpt-image-2.6-sunburst. Future model names require explicit gateway support. These remain options within text-to-image or image-to-image.'),
      params: [
        param('output_format', 'string', optional, 'png（默认）/ webp / jpeg / gif / svg。', 'png (default), webp, jpeg, gif or svg.'),
        param('frame_count', 'integer', optional, '仅 GIF；默认 9，范围 2～16（含端点）。', 'GIF only; default 9, range 2–16 inclusive.'),
        param('frame_duration_ms', 'integer', optional, '仅 GIF；默认 200ms，范围 20～2000ms（含端点）；官方上游按最接近的 10ms 编码。', 'GIF only; default 200 ms, range 20–2000 ms inclusive. The official upstream rounds to the nearest 10 ms.'),
        param('svg.model', 'string', l('上游条件需要', 'Upstream-dependent'), '仅 SVG；JSON 嵌套 svg.model，multipart 为 svg[model]。启用作者选择的上游要求提供，否则省略。无默认值，必须填供应商确认可用的作者模型。', 'SVG only. JSON uses nested svg.model; multipart uses svg[model]. Supply it when upstream author selection requires it, otherwise omit it. No default; use a provider-confirmed author model.'),
        param('svg.reasoning.effort', 'string / null', optional, '仅 SVG；none/minimal/low/medium/high/xhigh/max，依作者模型支持。省略或 JSON null 不指定档位；multipart 为 svg[reasoning][effort]。', 'SVG only; none/minimal/low/medium/high/xhigh/max, subject to author support. Omission or JSON null leaves effort unspecified; multipart uses svg[reasoning][effort].'),
      ],
      adjustments: [
        { format: 'GIF', change: l('在 OpenAI 主示例中换成已开放的 2.6 模型，设 output_format=gif、size=512x512 或 auto，删除 resolution/aspect_ratio；按需设两项帧参数。', 'In an OpenAI main example, choose an enabled 2.6 model, set output_format=gif and size=512x512 or auto, remove resolution/aspect_ratio, and optionally set the two frame controls.') },
        { format: 'SVG', change: l('换成已开放的 2.6 模型，设 output_format=svg、n=1，删除帧参数；仅上游要求时补 svg.model。作者模型独立选择，不自动复用顶层图片模型。', 'Choose an enabled 2.6 model, set output_format=svg and n=1, remove frame controls, and provide svg.model only when required. The author is selected independently; the top-level image model is not reused automatically.') },
      ],
      notes: [
        l('JSON 帧值必须为整数；multipart 使用整数文本。在 OpenAI 异步入口，非 GIF 带帧字段、非 SVG 带 svg 对象（包括空对象）或未开放模型使用扩展参数，会在建任务前返回 400。svg:null 视为省略；帧值要用默认值则省略字段，不传空值或 null。', 'Frame values must be JSON integers or integer text in multipart. OpenAI async endpoints reject frame fields on non-GIF output, svg objects (including empty objects) on non-SVG output, and extension options on unsupported models before task creation with 400. svg:null counts as omitted. Omit frame fields for defaults; do not send empty frame values or null.'),
        l('GIF 的 size 是单帧尺寸，但帧数、尺寸和 n 共同受上游拼图/像素预算限制。帧范围合法不代表组合可生成；超限时减小这些值。结果为一个完整 GIF 文件，保留全部帧。', 'GIF size describes one frame, but frame count, dimensions and n share the upstream sheet/pixel budget. Valid individual values do not guarantee a valid combination. Reduce them if needed. Each result is one complete GIF with all frames.'),
        l('上游启用 SVG 作者选择时，需要可用作者模型和相应权限；当前真实 SVG 生成与编辑尚未验收通过。编辑是否支持以账号上游为准，不因接口能透传而保证成功。', 'When upstream SVG author selection is enabled, a usable author model and permissions are required. Live SVG generation and editing have not passed verification. Editing support depends on the account upstream; forwarding does not guarantee success.'),
      ],
      topicUrl,
      topicLabel: l('格式限制与实测记录', 'Format limits and verification records'),
    },
    query: {
      title: l('受理与查询', 'Acceptance and polling'),
      acceptExample,
      path: `${v1}/images/tasks_async/{task_id}`,
      summary: l('上面两类主入口共用一次受理结构与查询协议。HTTP 200 只表示查询成功，必须读取 status；请求需带原 API Key。', 'Both main scenarios share one acceptance and polling protocol. HTTP 200 only means the query succeeded; inspect status and use the original API key.'),
      statuses: [
        { status: 'queued', meaning: l('已受理，等待执行或分配账号。', 'Accepted, waiting for execution or account assignment.') },
        { status: 'processing', meaning: l('上游生成、存储或计费处理中，继续轮询。', 'Generation, storage or billing is in progress; keep polling.') },
        { status: 'succeeded', meaning: l('成功终态，读取 data[].url。', 'Successful terminal state; read data[].url.') },
        { status: 'failed', meaning: l('失败终态，读取 error_code 和 fail_reason。', 'Failed terminal state; read error_code and fail_reason.') },
      ],
      successExample: json({ status: 'succeeded', task_id: 'asyncimg_0123456789abcdef', data: [{ url: 'https://cdn.example.com/images/results/output-1.png' }] }),
      failedExample: json({ status: 'failed', task_id: 'asyncimg_0123456789abcdef', error_code: 604, fail_reason: l('上游拒绝了请求参数，请按返回提示修改请求。', 'The upstream rejected the input; correct the request using the returned details.') }),
    },
    limits: {
      title: l('限制与重试', 'Limits and retries'),
      bullets: [
        l('参考图全局默认最多 8 张，配置硬上限 16；已知 Gemini 模型另取模型能力与全局上限中的较小值（Flash 3、Pro 14）。上游可有更严格限制，请按 fail_reason 调整。', 'The global reference default is 8 with a hard configuration maximum of 16. Known Gemini models use the smaller model/global limit (Flash 3, Pro 14). Upstreams may be stricter; follow fail_reason.'),
        l('参考图总量默认 64MiB/80MP；单图下载默认 32MiB，以运行配置为准。超限会失败；数量等部分限制在入队前检查，字节/像素检查也可能在处理参考图时发生。', 'Reference totals default to 64 MiB/80 MP; single-image downloads default to 32 MiB, subject to runtime settings. Overruns fail: counts may be checked before enqueueing, while byte/pixel checks may occur during reference processing.'),
        l('服务端仅对已识别的可恢复失败进行有预算的重试；客户端继续查询同一 task_id，不把重试写成固定次数。', 'The server retries recognized recoverable failures within its budgets. Clients keep polling the same task_id; there is no universal fixed retry count.'),
        l('执行结果未知时不要自动重提；上游已产图后的下载、存储或结算失败不会通过重新生图补救。', 'Do not automatically resubmit unknown execution outcomes. Download, storage or billing failures after generation do not trigger another generation.'),
      ],
    },
    errors: {
      title: l('错误说明', 'Errors'),
      intro: l('提交与查询处理错误通常使用 error 对象；鉴权等中间件也可能返回顶层 code/message。已受理任务失败的查询仍为 HTTP 200。error_code 是应用层对照码，不是 HTTP 6xx；展示原始脱敏 fail_reason，不用它盲目触发重提。', 'Submit/query handler errors normally use an error object; authentication middleware may return top-level code/message. Failed accepted tasks still poll with HTTP 200. error_code is an application code, not HTTP 6xx. Show sanitized fail_reason without blindly resubmitting.'),
      rows: [
        { status: '400', meaning: l('参数、尺寸、格式、帧值、SVG 选项或参考图不合法。', 'Invalid parameters, dimensions, format, frame values, SVG options or references.') },
        { status: '401 / 403', meaning: l('Key 无效，或平台/功能/权限不满足。', 'Invalid API key or unmet platform/feature/permission requirements.') },
        { status: '404', meaning: l('查询任务不存在或所属 Key 不符；旧式协议路径勿混用。', 'Task missing or wrong owning API key; do not mix legacy protocol paths.') },
        { status: '409 / 413', meaning: l('幂等冲突/上传额度不足，或请求体/参考图超限。', 'Idempotency conflict/upload quota, or oversized body/references.') },
        { status: '429 / 503', meaning: l('上传限频或存储/异步依赖暂不可用；遵守响应中的 Retry-After。', 'Upload rate limit or unavailable storage/async dependencies; honor Retry-After when returned.') },
      ],
      applicationCodes: [
        { status: '601–604', meaning: l('内容拦截、参考图获取、账号容量、请求输入。', 'Content restrictions, reference retrieval, account capacity, request input.') },
        { status: '605–610', meaning: l('限流、上游不可用、无效输出、超时/未知、存储/结算、未分类错误。', 'Rate limit, upstream unavailable, invalid output, timeout/unknown, storage/billing, unclassified failure.') },
        { status: '611–613', meaning: l('上游参考图数量限制、参考图缺失/不可用、提示词或图片无法处理；按 fail_reason 修正。', 'Upstream reference-count limit, missing/unusable references, unprocessable prompt/images; follow fail_reason.') },
      ],
    },
    compatibility: {
      title: l('兼容与辅助接口', 'Compatibility and helper endpoints'),
      intro: l('新接入优先使用上面的两类主示例。已有客户端、文件上传和统计查询参考以下补充。', 'Use the two main scenarios for new integrations. Existing clients, file uploads and statistics can use the details below.'),
      groups: [
        {
          title: l('OpenAI multipart 与历史参考图字段', 'OpenAI multipart and legacy reference fields'),
          notes: [
            l('上传文件时使用 multipart/form-data，字段名 image；其余标量字段同 JSON，整数用文本表示。两种参考方式选一种即可。', 'For file input use multipart/form-data with image; scalar fields match JSON and integers use text. Choose one reference-input style.'),
            l('仍兼容 images[].image_url，不支持 images[].file_id。上传/参考图仅 PNG/JPEG/WebP，不因输出支持 GIF/SVG 而扩展。', 'Legacy images[].image_url is accepted; images[].file_id is not. Upload/reference formats remain PNG/JPEG/WebP regardless of GIF/SVG output support.'),
            l('SVG multipart 字段对应 svg[model]、svg[reasoning][effort]；仅在已开放模型、SVG 输出及上游要求的条件下使用。', 'SVG multipart uses svg[model] and svg[reasoning][effort], only for enabled models, SVG output and the applicable upstream requirements.'),
          ],
          code: `curl -X POST '${v1}/images/edits_oa' \\\n  -H 'Authorization: Bearer YOUR_API_KEY' \\\n  -H 'Idempotency-Key: image-edit-001' \\\n  -F 'model=gpt-image-2' \\\n  -F 'prompt=${editPrompt}' \\\n  -F 'image=@reference.png' \\\n  -F 'resolution=1K' -F 'aspect_ratio=1:1' -F 'output_format=png'`,
          codeLabel: 'cURL',
        },
        {
          title: l('Gemini BB 与查询别名', 'Gemini BB and polling alias'),
          notes: [
            l('POST /v1/chat/completions_gm 为既有 BB 入口。messages 仅接受 role:user，content 为非空文字或 text/image_url 数组；不使用 prompt/image_urls 这套字段。', 'POST /v1/chat/completions_gm is the existing BB endpoint. messages accepts role:user only, with non-empty text or text/image_url parts; it does not use prompt/image_urls.'),
            l('规格使用 extra_body.google.image_config.image_size 与 aspect_ratio；stream 必须 false 或省略。受理含兼容字段，查询仍走 /v1/images/tasks_async/{task_id}。', 'Set extra_body.google.image_config.image_size and aspect_ratio; stream must be false or omitted. Acceptance adds compatibility fields; polling still uses /v1/images/tasks_async/{task_id}.'),
            l('SC 保留 GET /v1/tasks_sc/{task_id} 别名，返回与统一查询相同的 queued/processing/succeeded/failed。', 'SC retains GET /v1/tasks_sc/{task_id} as an alias with the same queued/processing/succeeded/failed states.'),
          ],
        },
        {
          title: l('旧式 OpenAI 异步任务', 'Legacy OpenAI async tasks'),
          notes: [l('POST /v1/images/generations/async、/v1/images/edits/async 属于另一套旧协议，查询为 GET /v1/images/tasks/{task_id}。它们接受当前 OpenAI 格式选项，但保持原有受理/查询结构，不能与 tasks_async 混用。', 'POST /v1/images/generations/async and /v1/images/edits/async use a separate legacy protocol, polled at GET /v1/images/tasks/{task_id}. They accept current OpenAI format options but retain their original response schema; do not mix them with tasks_async.')],
        },
        {
          title: l('Gemini 参考图上传', 'Gemini reference upload'),
          notes: [
            l('需要可用的 Gemini 平台计费组，并开启图片生成与异步生图；仅有 OpenAI 平台配置的 Key 不能直接使用此上传入口。', 'Requires a usable Gemini billing group with image generation and async generation enabled. An OpenAI-only API key cannot directly use this upload endpoint.'),
            l('POST /v1/uploads/images_sc 使用 multipart 字段 file，返回 HTTP 200；取响应 url 放入同 Key 的 image_urls。仅图生图需要参考，文生图不用先上传。', 'POST /v1/uploads/images_sc accepts multipart file and returns HTTP 200. Use the returned url in image_urls with the same API key. Text-to-image requires no upload.'),
            l('默认每 Key 每滚动分钟 20 次上传尝试、活动输入额度 1GiB、输入保留 24 小时；以运行设置为准。过期结果不能复用旧幂等键创建新文件。', 'Defaults: 20 upload attempts per API key per rolling minute, 1 GiB active inputs and 24-hour retention, subject to runtime settings. Do not reuse an expired result’s idempotency key to create a new file.'),
          ],
        },
        {
          title: l('当日统计', 'Daily statistics'),
          notes: [
            l('GET /v1/images/tasks_async/stats，使用 Authorization: Bearer。按服务器时区汇总当前 Key 所属用户的全部异步任务，不仅是当前 Key。', 'GET /v1/images/tasks_async/stats uses Authorization: Bearer. It summarizes all async tasks of the key owner in the server timezone, not only this key.'),
            l('返回 date、timezone、balance、today_requests、success_count、failure_count、success_rate；success_rate=成功/(成功+失败)×100，分母为0时返回0。', 'Returns date, timezone, balance, today_requests, success_count, failure_count and success_rate. Rate is success/(success+failure)×100, or 0 when the denominator is zero.'),
            l('today_requests 包含活动、成功、失败任务，不含 expired；failure_count 包含 storage_failed/billing_failed，可能与仍在重试的公共查询状态不同。', 'today_requests includes active, successful and failed tasks, excluding expired tasks. failure_count includes storage_failed/billing_failed and can differ from a public state that is still retrying.'),
          ],
        },
      ],
    },
    oss: {
      title: l('结果下载', 'Result downloads'),
      bullets: [
        l('仅 status=succeeded 时使用 data[].url，按实际 MIME/扩展名保存；PNG/JPEG/WebP 保持静态图片，GIF/SVG 沿用相同查询结构。', 'Use data[].url only for status=succeeded and save the actual MIME/extension. Static PNG/JPEG/WebP and GIF/SVG share the polling schema.'),
        l('私有存储默认签名 URL 有效 3600 秒，配置公开地址时可返回稳定链接；签名有效期与文件保留期不同，请及时下载。', 'Private storage defaults to 3600-second signed URLs; a configured public base can return stable links. URL expiry and file retention are separate; download promptly.'),
        l('管理员可按小时或天配置生成结果保留期，按间隔或每日定时清理，也可手动清理指定时间以前的结果。生成结果不保证永久保存，公开链接也可能因文件清理失效，请在成功后及时下载或转存。', 'Administrators can set result retention in hours or days, schedule cleanup at intervals or daily, and manually remove results older than a cutoff. Generated files are not permanent; public links can also stop working after cleanup. Download or archive results promptly after success.'),
        l('任务记录仍可查询时，部分结果被清理后 status 仍为 succeeded，data 只返回剩余结果 URL；全部清理后返回 status: "succeeded" 和 data: []。不要因此自动重新提交生成请求。任务记录另有独立保留期。', 'While the task record remains queryable, status stays succeeded after cleanup. Partial cleanup leaves only the remaining result URLs in data; complete cleanup returns status: "succeeded" and data: []. Do not automatically resubmit generation because of this. Task records have a separate retention period.'),
        l('图库或广场的有效引用会保护底层文件，但不保证旧任务结果行或旧任务 URL 继续保留。', 'Valid library or plaza references protect the underlying file, but do not guarantee retention of old task result entries or task URLs.'),
        l('本系统生成结果正在被本系统活动图生图任务引用时受保护；已清理或正在清理的结果 URL 不可再用于新任务的参考图。外部 URL 仍按原规则验证，不承诺识别或保护未知的外部 CDN 别名。', 'Generated results are protected while referenced by active image-to-image tasks in this system. Result URLs that have been cleaned up or are being deleted cannot be used as reference images for new tasks. External URLs follow the existing validation rules; unknown external CDN aliases are not guaranteed to be recognized or protected.'),
        l('GIF 全部帧累计像素受下载上限约束；SVG 只接受安全静态内容，拒绝脚本、事件、外链与活动内容。输入格式限制保持不变。', 'GIF total decoded frame pixels are bounded. SVG accepts only safe static content and rejects scripts, events, external resources and active content. Input-format restrictions stay unchanged.'),
      ],
      topicUrl,
      topicLabel: l('格式与验证专题（含历史实测）', 'Format and verification notes (including historical probes)'),
    },
    labels: {
      params: l('参数', 'Parameter'), type: l('类型', 'Type'), required: l('必填', 'Required'),
      description: l('说明', 'Description'), baseUrl: l('基础地址', 'Base URL'),
      copy: l('复制', 'Copy'), copied: l('已复制', 'Copied'),
      requestBody: l('请求体', 'Request body'), acceptResponse: l('受理响应 · HTTP 202', 'Acceptance · HTTP 202'),
      statusTable: l('状态说明', 'Task states'), notes: l('补充说明', 'Notes'),
      adjustments: l('在主示例上调整', 'Adjust the main examples'),
    },
  }
}
