import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

const mocks = vi.hoisted(() => ({
  listPersonalGalleryItems: vi.fn(),
  getPersonalGalleryItem: vi.fn(),
  updatePersonalGalleryItem: vi.fn(),
  removePersonalGalleryItem: vi.fn(),
  bindPersonalGalleryRequestId: vi.fn(),
  onPersonalGalleryChanged: vi.fn(),
  listMyPlazaSubmissionRequests: vi.fn(),
  createPlazaSubmissionRequest: vi.fn(),
  syncPlazaSubmissionRequest: vi.fn(),
  withdrawPlazaSubmissionRequest: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
}))

vi.mock('@/api/imageLibrary', () => ({
  createPlazaSubmissionRequest: mocks.createPlazaSubmissionRequest,
  listMyPlazaSubmissionRequests: mocks.listMyPlazaSubmissionRequests,
  syncPlazaSubmissionRequest: mocks.syncPlazaSubmissionRequest,
  withdrawPlazaSubmissionRequest: mocks.withdrawPlazaSubmissionRequest,
}))

vi.mock('../personalGalleryStore', () => ({
  listPersonalGalleryItems: mocks.listPersonalGalleryItems,
  getPersonalGalleryItem: mocks.getPersonalGalleryItem,
  updatePersonalGalleryItem: mocks.updatePersonalGalleryItem,
  removePersonalGalleryItem: mocks.removePersonalGalleryItem,
  bindPersonalGalleryRequestId: mocks.bindPersonalGalleryRequestId,
  onPersonalGalleryChanged: mocks.onPersonalGalleryChanged,
}))

vi.mock('@/stores', () => ({
  useAppStore: () => ({ showSuccess: mocks.showSuccess, showError: mocks.showError }),
  useAuthStore: () => ({ user: { id: 19 } }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return { ...actual, useI18n: () => ({ t: (key: string) => key }) }
})

import ImageLibraryPanel from '../ImageLibraryPanel.vue'

const item = {
  id: 'img_1',
  userId: 19,
  title: 'Old private title',
  file: new Blob(['png'], { type: 'image/png' }),
  contentType: 'image/png',
  byteSize: 3,
  checksumSha256: 'abc',
  platform: 'openai',
  model: 'gpt-image-1',
  prompt: 'a cat',
  createdAt: Date.now(),
  expiresAt: Date.now() + 60_000,
}

function mountPanel() {
  return mount(ImageLibraryPanel, {
    global: {
      stubs: {
        Icon: { template: '<span />' },
        RouterLink: { template: '<a><slot /></a>' },
        LazyImage: { template: '<div class="library-item__lazy"><slot /><slot name="placeholder" /></div>' },
        ImageLightbox: { template: '<div />' },
      },
    },
  })
}

describe('ImageLibraryPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.listPersonalGalleryItems.mockResolvedValue([{ ...item }])
    mocks.getPersonalGalleryItem.mockResolvedValue({ ...item })
    mocks.listMyPlazaSubmissionRequests.mockResolvedValue({ items: [] })
    mocks.onPersonalGalleryChanged.mockReturnValue(vi.fn())
  })

  it('updates the private title in the local gallery', async () => {
    mocks.updatePersonalGalleryItem.mockResolvedValue({ ...item, title: 'New private title' })
    const wrapper = mountPanel()
    await flushPromises()

    await wrapper.get('[aria-label="imageWorkflow.library.editPrivateTitle"]').trigger('click')
    const input = wrapper.get('input[aria-label="imageWorkflow.library.privateTitle"]')
    await input.setValue('  New private title  ')
    await input.trigger('keydown', { key: 'Enter' })
    await flushPromises()

    expect(mocks.updatePersonalGalleryItem).toHaveBeenCalledWith('img_1', { title: 'New private title' })
    expect(wrapper.text()).toContain('New private title')
    expect(mocks.showSuccess).toHaveBeenCalledWith('imageWorkflow.library.titleUpdated')
    wrapper.unmount()
  })

  it('loads local personal gallery items instead of the server library', async () => {
    const wrapper = mountPanel()
    await flushPromises()
    expect(mocks.listPersonalGalleryItems).toHaveBeenCalledWith(19)
    expect(wrapper.find('.library-item__lazy').exists()).toBe(true)
    wrapper.unmount()
  })
})
