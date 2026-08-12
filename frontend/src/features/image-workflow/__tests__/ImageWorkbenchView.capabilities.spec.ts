import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/views/user/ImageWorkbenchView.vue'),
  'utf8',
)

describe('ImageWorkbenchView capability-driven controls', () => {
  it('does not restore client-owned platform option defaults', () => {
    expect(source).not.toContain('const DEFAULTS')
    expect(source).not.toContain("['1024x1024', '1536x1024', '1024x1536']")
    expect(source).not.toContain("['1K', '2K', '4K']")
    expect(source).toContain('size: selectedCapabilityOption(sizeOptions.value, form.size)')
    expect(source).toContain('output_format: selectedCapabilityOption(formatOptions.value, form.format)')
  })

  it('renders only the coarse phases exposed by the public task query', () => {
    expect(source).toContain("{ key: 'queued'")
    expect(source).toContain("{ key: 'processing'")
    expect(source).toContain("{ key: 'succeeded'")
    expect(source).not.toContain("{ key: 'uploading'")
    expect(source).not.toContain("{ key: 'billing'")
  })

  it('supports continue-adding after async submit without waiting for completion', () => {
    expect(source).toContain('function continueAdding')
    expect(source).toContain("imageWorkflow.workbench.continueAdding")
    expect(source).toContain('asyncTask.active = false')
    expect(source).toContain('Keep polling after "继续添加"')
  })

  it('keeps realtime results local until explicit publish', () => {
    expect(source).toContain("archiveStatus: 'local'")
    expect(source).toContain('localOnly: true')
    expect(source).toContain('function publishLocalResult')
    expect(source).toContain('createPlazaSubmissionRequest')
    expect(source).toContain('savePersonalGalleryItem')
    expect(source).toContain('gallerySaveFailed')
    expect(source).not.toContain('previewUrl: url')
    expect(source).not.toContain('await Promise.allSettled(results.value.map((result) => archiveResult(result, current)))')
  })

  it('caches and aborts capability loads when switching keys', () => {
    expect(source).toContain('capabilityCache')
    expect(source).toContain('capabilityAbort')
    expect(source).toContain('prefetchWorkbenchCapabilities')
    expect(source).toContain('include_last_used_ip: false')
    expect(source).toContain('readCapabilityCache')
  })
})
