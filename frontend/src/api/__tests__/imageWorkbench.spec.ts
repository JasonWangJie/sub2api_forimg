import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  editImage,
  generateGeminiImage,
  generateImage,
  prepareGeminiAsyncSubmission,
  prepareOpenAIAsyncSubmission,
  resultToDataUrl,
} from '../imageWorkbench'

function jsonResponse(body: unknown) {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })
}

function blobBytes(blob: Blob): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(new Uint8Array(reader.result as ArrayBuffer))
    reader.onerror = () => reject(reader.error)
    reader.readAsArrayBuffer(blob)
  })
}

afterEach(() => vi.restoreAllMocks())

describe('image workbench async submissions', () => {
  it('reuses the exact OpenAI multipart bytes and Idempotency-Key after an unknown response', async () => {
    const calls: Array<{ url: string; init: RequestInit }> = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (input, init = {}) => {
      calls.push({ url: String(input), init })
      return jsonResponse({ task_id: 'asyncimg_openai', status: 'queued' })
    })
    const prepared = await prepareOpenAIAsyncSubmission('sk-test', {
      model: 'gpt-image-1',
      prompt: 'replace the background',
      imageFiles: [new File([new Uint8Array([1, 2, 3])], 'source.png', { type: 'image/png' })],
      size: '1024x1024',
      output_format: 'webp',
    }, 'same-operation-key')

    await prepared.send()
    await prepared.send()

    expect(calls).toHaveLength(2)
    expect(calls[0].url).toContain('/v1/images/generations_oa')
    const firstHeaders = calls[0].init.headers as Record<string, string>
    const secondHeaders = calls[1].init.headers as Record<string, string>
    expect(firstHeaders['Idempotency-Key']).toBe('same-operation-key')
    expect(secondHeaders).toEqual(firstHeaders)
    const firstBytes = await blobBytes(calls[0].init.body as Blob)
    const secondBytes = await blobBytes(calls[1].init.body as Blob)
    expect(secondBytes).toEqual(firstBytes)
    expect(new TextDecoder().decode(firstBytes)).toContain('name="output_format"\r\n\r\nwebp')
  })

  it('sends output_format with realtime OpenAI multipart edits', async () => {
    let submitted: FormData | undefined
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init = {}) => {
      submitted = init.body as FormData
      return jsonResponse({ data: [{ b64_json: 'cGF5bG9hZA==' }], output_format: 'webp' })
    })

    const result = await editImage('sk-test', {
      model: 'gpt-image-1',
      prompt: 'replace the background',
      imageFile: new File([new Uint8Array([1, 2, 3])], 'source.png', { type: 'image/png' }),
      output_format: 'webp',
    })

    expect(submitted?.get('output_format')).toBe('webp')
    expect(result.data[0].output_format).toBe('webp')
    expect(resultToDataUrl(result.data[0], 'png')).toBe('data:image/webp;base64,cGF5bG9hZA==')
  })

  it('uses actual image bytes before requested or response format metadata', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({
      data: [{ b64_json: 'iVBORw0KGgoAAAANSUhEUg==' }],
      output_format: 'webp',
    }))

    const result = await generateImage('sk-test', {
      model: 'gpt-image-1',
      prompt: 'test',
      output_format: 'webp',
    })

    expect(resultToDataUrl(result.data[0], 'webp')).toContain('data:image/png;base64,')
  })

  it('preserves Gemini inlineData MIME metadata', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({
      candidates: [{
        content: {
          parts: [{ inlineData: { mimeType: 'image/webp', data: 'cGF5bG9hZA==' } }],
        },
      }],
    }))

    const result = await generateGeminiImage('sk-test', {
      model: 'gemini-image',
      prompt: 'test',
    })

    expect(result.data[0].mime_type).toBe('image/webp')
    expect(resultToDataUrl(result.data[0], 'png')).toBe('data:image/webp;base64,cGF5bG9hZA==')
  })

  it('keeps Gemini SC retries on the same endpoint, body, and idempotency key', async () => {
    const calls: RequestInit[] = []
    vi.spyOn(globalThis, 'fetch').mockImplementation(async (_input, init = {}) => {
      calls.push(init)
      return jsonResponse({ task_id: 'asyncimg_gemini', query_url: '/v1/images/tasks_async/asyncimg_gemini', status: 'queued' })
    })
    const prepared = prepareGeminiAsyncSubmission('sk-gemini', {
      model: 'gemini-image',
      prompt: 'night scene',
      resolution: '2K',
      aspect_ratio: '16:9',
      image_urls: ['https://storage.example/input.png'],
    }, 'gemini-same-key')

    await prepared.send()
    await prepared.send()

    expect(calls[0].body).toBe(calls[1].body)
    expect(JSON.parse(String(calls[0].body))).toMatchObject({
      model: 'gemini-image',
      prompt: 'night scene',
      resolution: '2K',
      size: '16:9',
      image_urls: ['https://storage.example/input.png'],
    })
    expect((calls[0].headers as Record<string, string>)['Idempotency-Key']).toBe('gemini-same-key')
  })
})
