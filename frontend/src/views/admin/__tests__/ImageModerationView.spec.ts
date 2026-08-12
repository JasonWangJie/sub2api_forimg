import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  createCleanupJob: vi.fn(),
  getAdminImageLibraryStats: vi.fn(),
  getImageLibraryMigrationState: vi.fn(),
  listAdminImageLibrary: vi.fn(),
  listAdminPlazaSubmissionRequests: vi.fn(),
  listAdminPublications: vi.fn(),
  listAdminReports: vi.fn(),
  listCleanupJobs: vi.fn(),
  previewCleanup: vi.fn(),
  resolveAdminReport: vi.fn(),
  resolveImageLibraryViewURL: vi.fn(),
  reviewPlazaSubmissionRequest: vi.fn(),
  reviewPublication: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/imageLibrary', () => ({
  createCleanupJob: mocks.createCleanupJob,
  getAdminImageLibraryStats: mocks.getAdminImageLibraryStats,
  getImageLibraryMigrationState: mocks.getImageLibraryMigrationState,
  listAdminImageLibrary: mocks.listAdminImageLibrary,
  listAdminPlazaSubmissionRequests: mocks.listAdminPlazaSubmissionRequests,
  listAdminPublications: mocks.listAdminPublications,
  listAdminReports: mocks.listAdminReports,
  listCleanupJobs: mocks.listCleanupJobs,
  previewCleanup: mocks.previewCleanup,
  resolveAdminReport: mocks.resolveAdminReport,
  resolveImageLibraryViewURL: mocks.resolveImageLibraryViewURL,
  reviewPlazaSubmissionRequest: mocks.reviewPlazaSubmissionRequest,
  reviewPublication: mocks.reviewPublication,
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key,
    }),
  }
})

import ImageModerationView from '../ImageModerationView.vue'

const pendingPublications = [
  { id: 'imgpub_1', title: 'First', platform: 'gemini', model: 'model-a', status: 'pending_review', image_url: 'https://cdn.example/1.png', submitted_at: '2026-07-21T00:00:00Z' },
  { id: 'imgpub_2', title: 'Second', platform: 'openai', model: 'model-b', status: 'pending_review', image_url: 'https://cdn.example/2.png', submitted_at: '2026-07-21T00:00:00Z' },
]

function mountView() {
  return mount(ImageModerationView, {
    global: {
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        Icon: { template: '<span />' },
      },
    },
  })
}

describe('ImageModerationView', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.listAdminPlazaSubmissionRequests.mockResolvedValue({ items: [], next_cursor: null })
    mocks.listAdminPublications.mockResolvedValue({ items: pendingPublications, next_cursor: null })
    mocks.listAdminReports.mockResolvedValue({ items: [], next_cursor: null })
    mocks.listAdminImageLibrary.mockResolvedValue({ items: [], next_cursor: null })
    mocks.getAdminImageLibraryStats.mockResolvedValue({ object_count: 0, total_bytes: 0, private_count: 0, pending_review_count: 0, published_count: 0 })
    mocks.listCleanupJobs.mockResolvedValue({ items: [], next_cursor: null })
    mocks.getImageLibraryMigrationState.mockResolvedValue({ status: 'succeeded', migrated_count: 0, quarantined_count: 0 })
    mocks.previewCleanup.mockResolvedValue({ matched_items: 3, matched_bytes: 2048 })
    mocks.reviewPublication.mockResolvedValue({})
    mocks.reviewPlazaSubmissionRequest.mockResolvedValue({})
    vi.spyOn(window, 'confirm').mockReturnValue(true)
  })

  it('bulk approves only explicitly selected pending submissions', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.findAll('.moderation-tabs button')[1].trigger('click')
    await flushPromises()

    const rowCheckboxes = wrapper.findAll('tbody .selection-cell input')
    await rowCheckboxes[0].trigger('change')
    await rowCheckboxes[1].trigger('change')
    await wrapper.get('[data-testid="bulk-approve"]').trigger('click')
    await flushPromises()

    expect(mocks.reviewPublication).toHaveBeenCalledTimes(2)
    expect(mocks.reviewPublication).toHaveBeenCalledWith('imgpub_1', 'approve', '')
    expect(mocks.reviewPublication).toHaveBeenCalledWith('imgpub_2', 'approve', '')
    wrapper.unmount()
  })

  it('passes all supported library filters and the user cleanup scope to existing APIs', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.findAll('.moderation-tabs button')[3].trigger('click')
    await flushPromises()
    await wrapper.get('input[placeholder="imageWorkflow.admin.searchLibrary"]').setValue('model-x')
    await wrapper.get('.moderation-id-filter input').setValue('42')
    const selects = wrapper.findAll('.moderation-toolbar--library select')
    await selects[0].setValue('gemini')
    await selects[1].setValue('async_task')
    await selects[2].setValue('private')
    await selects[3].setValue('pending_review')
    await wrapper.get('[data-testid="library-filter-apply"]').trigger('click')
    await flushPromises()

    expect(mocks.listAdminImageLibrary).toHaveBeenLastCalledWith(expect.objectContaining({
      q: 'model-x',
      user_id: 42,
      platform: 'gemini',
      source: 'async_task',
      visibility: 'private',
      publication_status: 'pending_review',
    }))

    await wrapper.findAll('.moderation-tabs button')[4].trigger('click')
    await flushPromises()
    await wrapper.get('.cleanup-form select').setValue('user')
    await wrapper.get('.cleanup-form input[type="number"]').setValue('42')
    await wrapper.get('[data-testid="cleanup-preview"]').trigger('submit')
    await flushPromises()

    expect(mocks.previewCleanup).toHaveBeenCalledWith({ scope: 'user', user_id: 42 })
    wrapper.unmount()
  })
})
