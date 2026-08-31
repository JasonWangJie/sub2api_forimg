export interface ApiParam {
  name: string
  required: boolean
  type: string
  desc: string
}

export interface ApiEndpointBlock {
  id: string
  title: string
  method: 'POST' | 'GET'
  path: string
  summary: string
  contentType?: string
  params: ApiParam[]
  bodyExample: string
  bodyLabel?: string
  responseLabel?: string
  notes?: string[]
}

export interface ApiDocSection {
  id: string
  title: string
  intro?: string
  endpoints?: ApiEndpointBlock[]
  extraHtml?: string
}

export function getAsyncImageApiDoc(locale: string, apiRoot: string) {
  const root = apiRoot.replace(/\/+$/, '')
  const v1 = /\/v1$/i.test(root) ? root : `${root}/v1`
  const isEn = locale.toLowerCase().startsWith('en')

  if (isEn) {
    return {
      title: 'Async Image API',
      subtitle:
        'Submit durable text-to-image or image-to-image jobs, poll a task id, and retrieve results from the configured image storage.',
      baseUrl: root,
      toc: [
        { id: 'overview', label: 'Overview', level: 1 },
        { id: 'auth', label: 'Auth', level: 1 },
        { id: 'openai', label: 'OpenAI image groups', level: 1 },
        { id: 'openai-t2i', label: 'Text-to-image', level: 2 },
        { id: 'openai-i2i', label: 'Image-to-image', level: 2 },
        { id: 'gemini-bb', label: 'Gemini BB', level: 1 },
        { id: 'gemini-bb-t2i', label: 'Text-to-image', level: 2 },
        { id: 'gemini-bb-i2i', label: 'Image-to-image', level: 2 },
        { id: 'gemini-sc', label: 'Gemini SC', level: 1 },
        { id: 'gemini-sc-t2i', label: 'Text-to-image', level: 2 },
        { id: 'gemini-sc-i2i', label: 'Image-to-image', level: 2 },
        { id: 'sc-upload', label: 'Reference upload', level: 2 },
        { id: 'query', label: 'Query status', level: 1 },
        { id: 'limits', label: 'Limits and retries', level: 1 },
        { id: 'errors', label: 'Errors', level: 1 },
        { id: 'oss', label: 'OSS links', level: 1 },
      ],
      overview: {
        bullets: [
          'Only OpenAI / Gemini image groups with async image generation enabled.',
          'All new submit endpoints return HTTP 202 + task_id + query_url; SC keeps /v1/tasks_sc/{task_id} as a query alias.',
          'Poll with the same API key. A 200 query response is not success by itself; use status.',
          'Result URL lifetime depends on storage settings: private storage returns a signed URL (default 3600 seconds), while a configured public base URL can return a stable URL.',
          'Use Idempotency-Key and resend the identical request bytes when retrying a submission.',
        ],
      },
      auth: {
        header: 'Authorization: Bearer YOUR_API_KEY',
        contentType: 'Content-Type: application/json (multipart/form-data for uploads)',
        idempotency: 'Optional Idempotency-Key (≤255 bytes) to avoid duplicate billing on retries.',
      },
      openaiT2I: {
        id: 'openai-t2i',
        title: 'OpenAI · Text-to-image',
        method: 'POST' as const,
        path: `${v1}/images/generations_oa`,
        summary: 'Use generations_oa for text-to-image. If usable reference inputs are included, the parser promotes the request to image-to-image; use edits_oa when you want an explicit edit endpoint.',
        contentType: 'application/json',
        params: [
          { name: 'model', required: true, type: 'string', desc: 'Image model available for the group.' },
          { name: 'prompt', required: true, type: 'string', desc: 'Text description of the image.' },
          { name: 'n', required: false, type: 'number', desc: 'Number of images (model-dependent).' },
          { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K preferred.' },
          { name: 'aspect_ratio', required: false, type: 'string', desc: 'auto, 1:1, 2:3, 3:2, 4:5, 5:4, 4:3, 3:4, 16:9, 9:16, 21:9, 9:21, 2:1, 1:2.' },
          { name: 'size', required: false, type: 'string', desc: 'Compat: ratio (9:16), WxH (1024x1024), auto, or 2K as resolution.' },
          { name: 'quality', required: false, type: 'string', desc: 'e.g. high / medium / low (model-dependent).' },
          { name: 'background', required: false, type: 'string', desc: 'Background option if the model supports it.' },
          { name: 'output_format', required: false, type: 'string', desc: 'png / jpeg / webp when supported.' },
          { name: 'stream', required: false, type: 'boolean', desc: 'Must be false or omitted.' },
        ],
        bodyExample: `{
  "model": "gpt-image-2",
  "prompt": "a cat on the beach, photorealistic",
  "n": 1,
  "resolution": "1K",
  "aspect_ratio": "3:2",
  "quality": "high"
}`,
        acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
      },
      openaiI2I: {
        id: 'openai-i2i',
        title: 'OpenAI · Image-to-image',
        method: 'POST' as const,
        path: `${v1}/images/edits_oa`,
        summary: 'Explicit OpenAI image edit endpoint. Provide JSON image_urls or a multipart image file; generations_oa also promotes to edit mode when references are present.',
        contentType: 'application/json  or  multipart/form-data',
        params: [
          { name: 'model', required: true, type: 'string', desc: 'Image model available for the group.' },
          { name: 'prompt', required: true, type: 'string', desc: 'Edit instruction.' },
          { name: 'image_urls', required: false, type: 'string[]', desc: 'JSON mode: at least one non-empty HTTPS URL (image-to-image). Mutually exclusive with multipart image.' },
          { name: 'image', required: false, type: 'file', desc: 'Multipart mode: form field name image (image-to-image). Mutually exclusive with image_urls.' },
          { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K.' },
          { name: 'aspect_ratio', required: false, type: 'string', desc: 'Same as text-to-image; auto maps to upstream size=auto.' },
          { name: 'mask.image_url', required: false, type: 'string', desc: 'Optional mask URL for masked edits.' },
        ],
        bodyExample: `{
  "model": "gpt-image-2",
  "prompt": "keep subject, change background to night",
  "image_urls": [
    "https://cdn.example.com/reference.png"
  ],
  "resolution": "1K",
  "aspect_ratio": "1:1"
}`,
        acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
        notes: [
          'Image-to-image requires either JSON image_urls or multipart image (choose one mode).',
          'JSON uses image_urls as a string array of HTTPS URLs.',
          'The legacy images[].image_url array is also accepted; images[].file_id is not supported.',
          'Multipart: -F model=... -F prompt=... -F image=@file.png',
          'Reference formats: PNG / JPG / WEBP.',
        ],
      },
      geminiBBT2I: {
        id: 'gemini-bb-t2i',
        title: 'Gemini BB · Text-to-image',
        method: 'POST' as const,
        path: `${v1}/chat/completions_gm`,
        summary: 'Chat Completions-compatible Gemini image request. Omit image parts for text-to-image; stream must be false or omitted.',
        contentType: 'application/json',
        params: [
          { name: 'model', required: true, type: 'string', desc: 'Gemini image model mapped for the group.' },
          { name: 'messages', required: true, type: 'array', desc: 'At least one user message with a non-empty text prompt.' },
          { name: 'stream', required: false, type: 'boolean', desc: 'Must be false or omitted.' },
          { name: 'extra_body.google.image_config.image_size', required: false, type: 'string', desc: '1K / 2K / 4K; controlled 0.5K is available only for configured models.' },
          { name: 'extra_body.google.image_config.aspect_ratio', required: false, type: 'string', desc: 'auto, 1:1, 2:3, 3:2, 4:5, 5:4, 4:3, 3:4, 16:9, 9:16, 21:9, or 9:21.' },
        ],
        bodyExample: `{
  "model": "gemini-3-pro-image-preview",
  "stream": false,
  "messages": [
    { "role": "user", "content": "modern living room, nordic style, soft daylight" }
  ],
  "extra_body": {
    "google": {
      "image_config": { "image_size": "2K", "aspect_ratio": "16:9" }
    }
  }
}`,
        acceptExample: `{
  "id": "asyncimg_0123456789abcdef",
  "task_id": "asyncimg_0123456789abcdef",
  "object": "image.task",
  "status": "queued",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
        notes: [
          'Only role: user is accepted. Content can be a non-empty string or a non-empty array of text / image_url parts.',
          'HTTPS references are passed to Gemini as fileData; data:image URIs are validated locally and converted to inlineData.',
        ],
      },
      geminiBBI2I: {
        id: 'gemini-bb-i2i',
        title: 'Gemini BB · Image-to-image',
        method: 'POST' as const,
        path: `${v1}/chat/completions_gm`,
        summary: 'Use the same Chat Completions endpoint with image_url parts followed or preceded by text.',
        contentType: 'application/json',
        params: [
          { name: 'model', required: true, type: 'string', desc: 'Gemini image model mapped for the group.' },
          { name: 'messages', required: true, type: 'array', desc: 'User content array containing text and one or more image_url parts.' },
          { name: 'stream', required: false, type: 'boolean', desc: 'Must be false or omitted.' },
          { name: 'extra_body.google.image_config.image_size', required: false, type: 'string', desc: '1K / 2K / 4K; controlled 0.5K is available only for configured models.' },
          { name: 'extra_body.google.image_config.aspect_ratio', required: false, type: 'string', desc: 'Supported Gemini aspect ratio or auto.' },
        ],
        bodyExample: `{
  "model": "gemini-3-pro-image-preview",
  "stream": false,
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "image_url", "image_url": { "url": "https://cdn.example.com/reference.png" } },
        { "type": "text", "text": "keep the composition, change the scene to night" }
      ]
    }
  ],
  "extra_body": {
    "google": { "image_config": { "image_size": "4K", "aspect_ratio": "auto" } }
  }
}`,
        acceptExample: `{
  "id": "asyncimg_0123456789abcdef",
  "task_id": "asyncimg_0123456789abcdef",
  "object": "image.task",
  "status": "queued",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
        notes: [
          'Reference URLs must be absolute HTTPS URLs; data:image URIs are also supported in BB content parts.',
          'The order of image_url parts is preserved for prompts that refer to image 1, image 2, and so on.',
        ],
      },
      geminiT2I: {
        id: 'gemini-sc-t2i',
        title: 'Gemini SC · Text-to-image',
        method: 'POST' as const,
        path: `${v1}/images/generations_sc`,
        summary: 'Simple JSON body. Omit image_urls for text-to-image. Accept: HTTP 202 with the same task_id/query_url body as OpenAI async.',
        contentType: 'application/json',
        params: [
          { name: 'model', required: true, type: 'string', desc: 'Gemini image model mapped for the group.' },
          { name: 'prompt', required: true, type: 'string', desc: 'Text description of the image.' },
          { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K.' },
          { name: 'size', required: false, type: 'string', desc: 'Aspect ratio (auto, 1:1, 2:3, 3:2, 4:5, 5:4, 4:3, 3:4, 16:9, 9:16, 21:9, 9:21) or pixel dimensions (1080x1350 maps to 4:5). Or tier 2K when resolution is empty.' },
          { name: 'aspect_ratio', required: false, type: 'string', desc: 'Same ratios as size, including auto (omits upstream ratio).' },
        ],
        bodyExample: `{
  "model": "gemini-3-pro-image-preview",
  "prompt": "modern living room, nordic style, soft daylight",
  "resolution": "2K",
  "size": "16:9"
}`,
        acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
        notes: [
          'Query with GET /v1/images/tasks_async/{task_id} using the same API key (shared with OpenAI).',
        ],
      },
      geminiI2I: {
        id: 'gemini-sc-i2i',
        title: 'Gemini SC · Image-to-image',
        method: 'POST' as const,
        path: `${v1}/images/generations_sc`,
        summary: 'Same path as text-to-image. Pass one or more reference URLs in image_urls. Accept: HTTP 202.',
        contentType: 'application/json',
        params: [
          { name: 'model', required: true, type: 'string', desc: 'Gemini image model.' },
          { name: 'prompt', required: true, type: 'string', desc: 'Edit instruction (can refer to 图1 / 图2 order).' },
          { name: 'image_urls', required: true, type: 'string[]', desc: 'HTTPS reference image URLs (PNG / JPG / WEBP).' },
          { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K.' },
          { name: 'size', required: false, type: 'string', desc: 'Aspect ratio (e.g. 3:2) or pixel dimensions (e.g. 1080x1350, mapped to 4:5).' },
          { name: 'aspect_ratio', required: false, type: 'string', desc: 'Optional; size is preferred by many clients. auto omits upstream ratio.' },
        ],
        bodyExample: `{
  "image_urls": [
    "https://cdn.example.com/ref-1.jpg",
    "https://cdn.example.com/ref-2.jpg"
  ],
  "model": "gemini-3-pro-image-preview",
  "prompt": "clean matte gallery tone, keep all native textures of bamboo, wood, ceramic and fabric, soft warm light matching reference image 2",
  "resolution": "4K"
}`,
        acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
        notes: [
          'image_urls order is 图1, 图2, … in the prompt.',
          'size accepts a supported ratio or pixel dimensions; dimensions are reduced to a supported Gemini ratio. Omit size / aspect_ratio to use the upstream default ratio.',
          'Query: GET /v1/images/tasks_async/{id}; /v1/tasks_sc/{id} remains a compatibility alias with the same response body.',
        ],
      },
      scUpload: {
        id: 'sc-upload',
        title: 'Gemini SC · Upload a reference image',
        method: 'POST' as const,
        path: `${v1}/uploads/images_sc`,
        summary: 'Upload a reference image when a safe, publicly reachable HTTPS URL is not available, then pass the returned URL in image_urls.',
        contentType: 'multipart/form-data',
        bodyLabel: 'cURL',
        responseLabel: 'Accept response (200)',
        params: [
          { name: 'file', required: true, type: 'file', desc: 'PNG / JPG / WEBP image. The server validates MIME, bytes, pixels, and full decoding.' },
        ],
        bodyExample: `curl -X POST '${v1}/uploads/images_sc' \\
  -H 'Authorization: Bearer YOUR_API_KEY' \\
  -H 'Idempotency-Key: upload-reference-001' \\
  -F 'file=@reference.png'`,
        acceptExample: `{
  "url": "https://storage.example.com/images/inputs/1/asyncimg_0123456789abcdef.png",
  "filename": "reference.png",
  "content_type": "image/png",
  "bytes": 204800,
  "created_at": 1784548800
}`,
        notes: [
          'Upload is optional for text-to-image and is only a Gemini SC input helper.',
          'The default limit is 20 upload attempts per API key per rolling minute and 1 GiB of live input bytes per key.',
          'The default effective image payload limit is 32 MiB; the configured hard maximum is 64 MiB. Input retention defaults to 24 hours.',
          'Use a new Idempotency-Key after an expired or unavailable upload result; do not silently reuse the old key.',
        ],
      },
      query: {
        id: 'query',
        title: 'Query task status',
        method: 'GET' as const,
        path: `${v1}/images/tasks_async/{task_id}`,
        summary: 'OpenAI and Gemini share this path. Poll with the same API key, honor Retry-After: 3, and check status rather than HTTP 200 alone.',
        aliasPath: `${v1}/tasks_sc/{task_id}`,
        statuses: [
          { status: 'queued', meaning: 'Accepted, waiting for worker' },
          { status: 'processing', meaning: 'Upstream / upload / billing in progress' },
          { status: 'succeeded', meaning: 'Done — read data[].url (signed or public URL according to storage settings)' },
          { status: 'failed', meaning: 'Terminal failure — read error_code and fail_reason' },
        ],
        queuedExample: `{
  "status": "queued",
  "task_id": "asyncimg_0123456789abcdef"
}`,
        successExample: `{
  "status": "succeeded",
  "task_id": "asyncimg_0123456789abcdef",
  "data": [
    { "url": "https://oss.example.com/images/results/output-1.png" }
  ]
}`,
        failedExample: `{
  "status": "failed",
  "task_id": "asyncimg_0123456789abcdef",
  "error_code": 601,
  "fail_reason": "Upstream image generation failed (HTTP 400): content policy or third-party similarity protection rejected the prompt"
}`,
        notes: [
          'Failed task queries still return HTTP 200. error_code is an application code, not an HTTP 6xx status.',
          'error_code: 601 policy/similarity, 602 reference fetch, 603 account capacity, 604 invalid input, 605 rate limit, 606 upstream unavailable, 607 invalid image output, 608 timeout/unknown, 609 storage or billing post-processing, 610 unclassified upstream (fallback).',
          'Display fail_reason as returned. Upstream failures preserve the sanitized upstream message (subject to the existing length limit), including content-policy and reference-image errors.',
        ],
      },
      oss: {
        title: 'OSS result links',
        bullets: [
          'Only use data[].url when status is succeeded.',
          'Private storage returns a signed URL whose lifetime is controlled by async_image.signed_url_expiry_seconds (default 3600 seconds). A configured public base URL can return a stable public URL.',
          'Do not share API keys; poll only with the submitting key.',
        ],
      },
      limits: {
        title: 'Limits and retry rules',
        bullets: [
          'The global reference-image guardrail applies to OpenAI and Gemini. For known Gemini models the effective limit is the smaller of the model capability and async_image.max_reference_images; the global default is 8 and the hard configuration maximum is 16.',
          'Known Gemini Flash Image models allow up to 3 references; Pro Image models allow up to 14. Unknown model names use the global guardrail.',
          'Reference aggregate defaults are 64 MiB and 80 MP, with hard maxima of 256 MiB and 320 MP. Single-image download defaults to 32 MiB and is capped at 64 MiB.',
          '0.5K is rejected unless the model is enabled in async_image.gemini_half_k_models. Pixel sizes are normalized to a supported Gemini aspect ratio.',
          'Retry submission with the same Idempotency-Key and identical bytes. Do not automatically resubmit a task reported as execution_unknown.',
        ],
      },
      errors: {
        title: 'Common errors',
        rows: [
          { status: '400', meaning: 'Invalid JSON/multipart, missing model or prompt, stream=true, invalid dimensions, upload failure, or a known-model reference count overrun.' },
          { status: '401', meaning: 'Invalid API key.' },
          { status: '403', meaning: 'Platform mismatch, ordinary image generation disabled, or async image generation disabled.' },
          { status: '404', meaning: 'Task not found, query dialect mismatch, or a different API key was used.' },
          { status: '409', meaning: 'Idempotency conflict/in progress, an unavailable result tombstone, or SC input byte quota exhaustion.' },
          { status: '413', meaning: 'Request body, upload, or reference image exceeds a configured limit.' },
          { status: '429', meaning: 'SC upload rolling rate limit; honor Retry-After: 60.' },
          { status: '503', meaning: 'Storage, encryption, runtime configuration, or PostgreSQL upload admission is unavailable.' },
        ],
      },
      labels: {
        required: 'Required',
        optional: 'Optional',
        params: 'Parameters',
        requestBody: 'Request body',
        acceptResponse: 'Accept response (202)',
        statusTable: 'Status values',
        notes: 'Notes',
        baseUrl: 'Base URL',
        copy: 'Copy',
        copied: 'Copied',
      },
    }
  }

  return {
    title: '异步生图 API 文档',
    subtitle:
      '支持持久化文生图 / 图生图 API：提交任务后轮询任务号，结果由当前图片存储配置提供。',
    baseUrl: root,
    toc: [
      { id: 'overview', label: '概览', level: 1 },
      { id: 'auth', label: '鉴权', level: 1 },
      { id: 'openai', label: 'OpenAI 系列生图分组', level: 1 },
      { id: 'openai-t2i', label: '文生图', level: 2 },
      { id: 'openai-i2i', label: '图生图', level: 2 },
      { id: 'gemini-bb', label: 'Gemini BB', level: 1 },
      { id: 'gemini-bb-t2i', label: '文生图', level: 2 },
      { id: 'gemini-bb-i2i', label: '图生图', level: 2 },
      { id: 'gemini-sc', label: 'Gemini SC', level: 1 },
      { id: 'gemini-sc-t2i', label: '文生图', level: 2 },
      { id: 'gemini-sc-i2i', label: '图生图', level: 2 },
      { id: 'sc-upload', label: '参考图上传', level: 2 },
      { id: 'query', label: '任务状态查询', level: 1 },
      { id: 'limits', label: '限制与重试', level: 1 },
      { id: 'errors', label: '错误响应', level: 1 },
      { id: 'oss', label: 'OSS 链接说明', level: 1 },
    ],
    overview: {
      bullets: [
        '仅「已开启异步生图」的 OpenAI / Gemini 系列的生图分组可用。',
        '所有新提交接口均返回 HTTP 202 + task_id + query_url；SC 额外保留 /v1/tasks_sc/{task_id} 查询别名。',
        '查询必须使用提交任务的同一把 API Key；HTTP 200 只表示查询成功，不代表生图成功，必须读取 status。',
        '结果 URL 有效期由存储配置决定：私有存储默认返回 3600 秒签名 URL；配置公开地址时可返回稳定公开 URL。',
        '网络重试请使用 Idempotency-Key，并原样重发相同请求字节，避免重复生成和计费。',
      ],
    },
    auth: {
      header: 'Authorization: Bearer 你的_API_Key',
      contentType: 'Content-Type: application/json（上传接口使用 multipart/form-data）',
      idempotency: '可选 Idempotency-Key（≤255 字节），避免网络重试导致重复计费。',
    },
    openaiT2I: {
      id: 'openai-t2i',
      title: 'OpenAI · 文生图',
      method: 'POST' as const,
      path: `${v1}/images/generations_oa`,
      summary: '文生图使用 generations_oa；如果请求带有可用参考图，解析器会自动提升为图生图。需要明确编辑语义时使用 edits_oa。',
      contentType: 'application/json',
      params: [
        { name: 'model', required: true, type: 'string', desc: '分组可用的图片模型名。' },
        { name: 'prompt', required: true, type: 'string', desc: '画面描述提示词。' },
        { name: 'n', required: false, type: 'number', desc: '生成张数（受模型能力限制）。' },
        { name: 'resolution', required: false, type: 'string', desc: '推荐：1K / 2K / 4K。' },
        { name: 'aspect_ratio', required: false, type: 'string', desc: 'auto、1:1、2:3、3:2、4:5、5:4、4:3、3:4、16:9、9:16、21:9、9:21、2:1、1:2。' },
        { name: 'size', required: false, type: 'string', desc: '可写：比例（如 9:16）、WxH（如 1024x1024）、auto，或档位（如 2K）。' },
        { name: 'quality', required: false, type: 'string', desc: '如 high / medium / low（视模型支持）。' },
        { name: 'background', required: false, type: 'string', desc: '背景相关参数（视模型支持）。' },
        { name: 'output_format', required: false, type: 'string', desc: '输出格式：png / jpeg / webp（视模型支持）。' },
        { name: 'stream', required: false, type: 'boolean', desc: '必须为 false 或省略，异步接口不支持流式。' },
      ],
      bodyExample: `{
  "model": "gpt-image-2",
  "prompt": "一只在沙滩上的猫，写实风格",
  "n": 1,
  "resolution": "1K",
  "aspect_ratio": "3:2",
  "quality": "high"
}`,
      acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
    },
    openaiI2I: {
      id: 'openai-i2i',
      title: 'OpenAI · 图生图',
      method: 'POST' as const,
      path: `${v1}/images/edits_oa`,
      summary: '明确的 OpenAI 图片编辑入口。JSON 传 image_urls，或 multipart 上传 image；generations_oa 带参考图时也会自动进入编辑模式。',
      contentType: 'application/json  或  multipart/form-data',
      params: [
        { name: 'model', required: true, type: 'string', desc: '分组可用的图片模型名。' },
        { name: 'prompt', required: true, type: 'string', desc: '改图指令。' },
        { name: 'image_urls', required: false, type: 'string[]', desc: 'JSON 模式：至少一个非空 HTTPS URL（图生图）。与 multipart 的 image 二选一。' },
        { name: 'image', required: false, type: 'file', desc: 'multipart 模式：表单文件字段名 image（图生图）。与 image_urls 二选一。' },
        { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K。' },
        { name: 'aspect_ratio', required: false, type: 'string', desc: '同文生图；auto 时上游 size 为 auto。' },
        { name: 'mask.image_url', required: false, type: 'string', desc: '可选遮罩图 URL（局部编辑）。' },
      ],
      bodyExample: `{
  "model": "gpt-image-2",
  "prompt": "保留主体，把背景换成夜景",
  "image_urls": [
    "https://cdn.example.com/reference.png"
  ],
  "resolution": "1K",
  "aspect_ratio": "1:1"
}`,
      acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
      notes: [
        '图生图需二选一：JSON 传 image_urls，或 multipart 上传 image。',
        'JSON 使用 image_urls（HTTPS URL 字符串数组）。',
        '仍兼容旧的 images[].image_url 对象数组；不支持 images[].file_id。',
        'multipart 示例：-F model=... -F prompt=... -F image=@reference.png',
        '参考图格式建议：PNG / JPG / WEBP。',
      ],
    },
    geminiBBT2I: {
      id: 'gemini-bb-t2i',
      title: 'Gemini BB · 文生图',
      method: 'POST' as const,
      path: `${v1}/chat/completions_gm`,
      summary: '兼容 Chat Completions 的 Gemini 图片请求。文生图不传图片部分；stream 必须为 false 或省略。',
      contentType: 'application/json',
      params: [
        { name: 'model', required: true, type: 'string', desc: '分组映射的 Gemini 图片模型。' },
        { name: 'messages', required: true, type: 'array', desc: '至少一条 user 消息，且必须包含非空文本提示。' },
        { name: 'stream', required: false, type: 'boolean', desc: '必须为 false 或省略。' },
        { name: 'extra_body.google.image_config.image_size', required: false, type: 'string', desc: '1K / 2K / 4K；仅配置允许的模型可使用 0.5K。' },
        { name: 'extra_body.google.image_config.aspect_ratio', required: false, type: 'string', desc: 'auto、1:1、2:3、3:2、4:5、5:4、4:3、3:4、16:9、9:16、21:9 或 9:21。' },
      ],
      bodyExample: `{
  "model": "gemini-3-pro-image-preview",
  "stream": false,
  "messages": [
    { "role": "user", "content": "现代客厅，北欧风，自然光" }
  ],
  "extra_body": {
    "google": {
      "image_config": { "image_size": "2K", "aspect_ratio": "16:9" }
    }
  }
}`,
      acceptExample: `{
  "id": "asyncimg_0123456789abcdef",
  "task_id": "asyncimg_0123456789abcdef",
  "object": "image.task",
  "status": "queued",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
      notes: [
        '只接受 role: user；content 可以是非空字符串，或仅包含 text / image_url 的非空数组。',
        'HTTPS 参考图透传为 Gemini fileData；data:image URI 会在本地校验并转换为 inlineData。',
      ],
    },
    geminiBBI2I: {
      id: 'gemini-bb-i2i',
      title: 'Gemini BB · 图生图',
      method: 'POST' as const,
      path: `${v1}/chat/completions_gm`,
      summary: '同一 Chat Completions 入口，在消息 content 数组中加入 image_url 与文本提示。',
      contentType: 'application/json',
      params: [
        { name: 'model', required: true, type: 'string', desc: '分组映射的 Gemini 图片模型。' },
        { name: 'messages', required: true, type: 'array', desc: 'user 消息的 content 数组，包含 text 和一张或多张 image_url。' },
        { name: 'stream', required: false, type: 'boolean', desc: '必须为 false 或省略。' },
        { name: 'extra_body.google.image_config.image_size', required: false, type: 'string', desc: '1K / 2K / 4K；仅配置允许的模型可使用 0.5K。' },
        { name: 'extra_body.google.image_config.aspect_ratio', required: false, type: 'string', desc: 'Gemini 支持的比例或 auto。' },
      ],
      bodyExample: `{
  "model": "gemini-3-pro-image-preview",
  "stream": false,
  "messages": [
    {
      "role": "user",
      "content": [
        { "type": "image_url", "image_url": { "url": "https://cdn.example.com/reference.png" } },
        { "type": "text", "text": "保留构图，把场景改成夜景" }
      ]
    }
  ],
  "extra_body": {
    "google": { "image_config": { "image_size": "4K", "aspect_ratio": "auto" } }
  }
}`,
      acceptExample: `{
  "id": "asyncimg_0123456789abcdef",
  "task_id": "asyncimg_0123456789abcdef",
  "object": "image.task",
  "status": "queued",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
      notes: [
        '参考图必须是绝对 HTTPS URL；BB content part 也支持 data:image URI。',
        'image_url 顺序会保留，可在提示词中按图1、图2 顺序引用。',
      ],
    },
    geminiT2I: {
      id: 'gemini-sc-t2i',
      title: 'Gemini SC · 文生图',
      method: 'POST' as const,
      path: `${v1}/images/generations_sc`,
      summary: '简洁 JSON 请求体。文生图不传 image_urls。受理响应 HTTP 202，与 OpenAI 异步返回同一任务体。',
      contentType: 'application/json',
      params: [
        { name: 'model', required: true, type: 'string', desc: '分组映射的 Gemini 图片模型。' },
        { name: 'prompt', required: true, type: 'string', desc: '图片描述提示词。' },
        { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K。' },
        { name: 'size', required: false, type: 'string', desc: '比例（auto、1:1、2:3、3:2、4:5、5:4、4:3、3:4、16:9、9:16、21:9、9:21）或像素尺寸（如 1080x1350，会映射为 4:5）；未传 resolution 时也可写 2K 表示清晰度。' },
        { name: 'aspect_ratio', required: false, type: 'string', desc: '与 size 同系列比例，含 auto（省略上游比例，由模型决定）。' },
      ],
      bodyExample: `{
  "model": "gemini-3-pro-image-preview",
  "prompt": "现代客厅，北欧风，自然光",
  "resolution": "2K",
  "size": "16:9"
}`,
      acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
      notes: [
        '查询请使用 GET /v1/images/tasks_async/{task_id}（与 OpenAI 共用同一路径与响应格式）。',
      ],
    },
    geminiI2I: {
      id: 'gemini-sc-i2i',
      title: 'Gemini SC · 图生图',
      method: 'POST' as const,
      path: `${v1}/images/generations_sc`,
      summary: '与文生图同一路径。通过 image_urls 传入一张或多张参考图。受理响应 HTTP 202。',
      contentType: 'application/json',
      params: [
        { name: 'model', required: true, type: 'string', desc: 'Gemini 图片模型。' },
        { name: 'prompt', required: true, type: 'string', desc: '编辑说明（可按图1、图2 顺序引用参考图）。' },
        { name: 'image_urls', required: true, type: 'string[]', desc: '参考图公网 HTTPS URL（PNG / JPG / WEBP）。' },
        { name: 'resolution', required: false, type: 'string', desc: '1K / 2K / 4K。' },
        { name: 'size', required: false, type: 'string', desc: '宽高比（如 3:2）或像素尺寸（如 1080x1350，会映射为 4:5）。' },
        { name: 'aspect_ratio', required: false, type: 'string', desc: '可选；多数客户端优先用 size。可用 auto（省略上游比例）。' },
      ],
      bodyExample: `{
  "image_urls": [
    "https://cdn.example.com/ref-1.jpg",
    "https://cdn.example.com/ref-2.jpg"
  ],
  "model": "gemini-3-pro-image-preview",
  "prompt": "画面通透干净，哑光高级展厅室内色调，完整保留竹编、实木、陶瓷、布艺全部原生肌理，光影柔和温润，视觉氛围与参考图二商业茶室柔和质感完全统一。",
  "resolution": "4K"
}`,
      acceptExample: `{
  "task_id": "asyncimg_0123456789abcdef",
  "query_url": "${v1}/images/tasks_async/asyncimg_0123456789abcdef"
}`,
      notes: [
        'image_urls 顺序对应提示词中的图1、图2…',
        'size 可传受支持的比例或像素尺寸；像素尺寸会约分为 Gemini 支持的比例。不传 size / aspect_ratio 则使用上游默认比例。',
        '查询：GET /v1/images/tasks_async/{id}；/v1/tasks_sc/{id} 仍是返回同一结构的兼容别名。',
      ],
    },
    scUpload: {
      id: 'sc-upload',
      title: 'Gemini SC · 上传参考图',
      method: 'POST' as const,
      path: `${v1}/uploads/images_sc`,
      summary: '当参考图没有安全可访问的公网 HTTPS URL 时先上传，再把返回 URL 放入 image_urls。',
      contentType: 'multipart/form-data',
      bodyLabel: 'cURL',
      responseLabel: '受理响应（200）',
      params: [
        { name: 'file', required: true, type: 'file', desc: 'PNG / JPG / WEBP 图片；服务端会校验 MIME、字节、像素和完整解码。' },
      ],
      bodyExample: `curl -X POST '${v1}/uploads/images_sc' \\
  -H 'Authorization: Bearer 你的_API_Key' \\
  -H 'Idempotency-Key: upload-reference-001' \\
  -F 'file=@reference.png'`,
      acceptExample: `{
  "url": "https://storage.example.com/images/inputs/1/asyncimg_0123456789abcdef.png",
  "filename": "reference.png",
  "content_type": "image/png",
  "bytes": 204800,
  "created_at": 1784548800
}`,
      notes: [
        '上传不是文生图必需步骤，仅用于 Gemini SC 参考图输入。',
        '默认每个 API Key 每滚动分钟最多 20 次上传尝试，活动输入字节额度默认 1 GiB。',
        '默认有效图片负载上限为 32 MiB，配置硬上限为 64 MiB；输入默认保留 24 小时。',
        '上传结果过期或不可用后必须换新的 Idempotency-Key，不能使用旧键静默创建新对象。',
      ],
    },
    query: {
      id: 'query',
      title: '任务状态查询',
      method: 'GET' as const,
      path: `${v1}/images/tasks_async/{task_id}`,
      summary: 'OpenAI 与 Gemini 共用此路径。请使用同一把 API Key，遵守 Retry-After: 3，并以 status 而不是 HTTP 200 判断结果。',
      aliasPath: `${v1}/tasks_sc/{task_id}`,
      statuses: [
        { status: 'queued', meaning: '已受理，等待执行' },
        { status: 'processing', meaning: '上游生成 / 上传 OSS / 计费确认中' },
        { status: 'succeeded', meaning: '成功，读取 data[].url（签名或公开 URL 的有效期按存储配置）' },
        { status: 'failed', meaning: '失败终态，读取 error_code 和 fail_reason' },
      ],
      queuedExample: `{
  "status": "queued",
  "task_id": "asyncimg_0123456789abcdef"
}`,
      successExample: `{
  "status": "succeeded",
  "task_id": "asyncimg_0123456789abcdef",
  "data": [
    { "url": "https://oss.example.com/images/results/output-1.png" }
  ]
}`,
      failedExample: `{
  "status": "failed",
  "task_id": "asyncimg_0123456789abcdef",
  "error_code": 601,
  "fail_reason": "上游生图失败（HTTP 400）：非常抱歉，生成的图片可能违反了关于与第三方内容相似性的防护限制。如果你认为此判断有误，请重试或修改提示语。"
}`,
      notes: [
        '失败查询仍返回 HTTP 200；error_code 是应用层对照码，不是 HTTP 6xx 状态码。',
        'error_code：601 内容安全/第三方相似性，602 参考图拉取，603 账号容量，604 请求输入，605 上游限流，606 上游不可用，607 图片输出解析，608 超时/未知，609 存储或计费后处理，610 未分类上游错误（容错码）。',
        '请原样展示返回的 fail_reason；上游失败会在现有脱敏和长度限制内保留上游提示，包括内容政策和参考图错误。',
      ],
    },
    oss: {
      title: 'OSS 链接说明',
      bullets: [
        '仅当 status 为 succeeded 时使用 data[].url。',
        '私有存储返回签名 URL，有效期由 async_image.signed_url_expiry_seconds 控制（默认 3600 秒）；配置公开地址时可返回稳定公开 URL。',
        '请勿泄露 API Key；查询必须使用提交任务的同一把 Key。',
      ],
    },
    limits: {
      title: '限制与重试规则',
      bullets: [
        'OpenAI 与 Gemini 都受 async_image.max_reference_images 全局参考图保护；已知 Gemini 模型再取模型能力与全局上限中的较小值。全局默认 8 张，配置硬上限 16 张。',
        '已知 Gemini Flash Image 模型最多 3 张，Pro Image 模型最多 14 张；未知模型继续使用全局保护。',
        '参考图总量默认 64 MiB / 80 MP，硬上限 256 MiB / 320 MP；单图下载默认 32 MiB，硬上限 64 MiB。',
        '0.5K 仅对 async_image.gemini_half_k_models 中允许的模型开放；像素尺寸会规范化为 Gemini 支持的宽高比。',
        '提交重试必须使用相同 Idempotency-Key 和完全一致的请求字节；任务进入 execution_unknown 后禁止自动重调上游。',
      ],
    },
    errors: {
      title: '常见错误响应',
      rows: [
        { status: '400', meaning: 'JSON/multipart 无效、缺少模型或提示、stream=true、尺寸非法、上传失败，或已知模型参考图数量超限。' },
        { status: '401', meaning: 'API Key 无效。' },
        { status: '403', meaning: '平台不匹配、普通生图关闭或异步生图关闭。' },
        { status: '404', meaning: '任务不存在、查询方言不匹配，或使用了不同的 API Key。' },
        { status: '409', meaning: '幂等键冲突/处理中、结果墓碑不可重放，或 SC 输入字节额度不足。' },
        { status: '413', meaning: '请求体、上传或参考图超过配置上限。' },
        { status: '429', meaning: 'SC 上传滚动限频；请遵守 Retry-After: 60。' },
        { status: '503', meaning: '对象存储、加密、异步运行配置或 PostgreSQL 上传 admission 不可用。' },
      ],
    },
    labels: {
      required: '必填',
      optional: '可选',
      params: '参数说明',
      requestBody: '请求体示例',
      acceptResponse: '受理响应（202）',
      statusTable: '状态说明',
      notes: '补充说明',
      baseUrl: '基础地址',
      copy: '复制',
      copied: '已复制',
    },
  }
}
