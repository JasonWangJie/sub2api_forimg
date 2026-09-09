import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AsyncImageTasksView from '../AsyncImageTasksView.vue'

const mocks = vi.hoisted(() => ({
  list: vi.fn(),
  get: vi.fn(),
  resume: vi.fn(),
  terminate: vi.fn(),
  batchTerminate: vi.fn(),
  resolveView: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showWarning: vi.fn(),
}))

vi.mock('../api', () => ({
  default: {
    user: { list: mocks.list, get: mocks.get },
    admin: {
      list: mocks.list,
      get: mocks.get,
      resume: mocks.resume,
      terminate: mocks.terminate,
      batchTerminate: mocks.batchTerminate,
    },
    resolveView: mocks.resolveView,
  },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showSuccess: mocks.showSuccess,
    showError: mocks.showError,
    showWarning: mocks.showWarning,
  }),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => params?.count === undefined ? key : `${key}:${params.count}`,
      te: () => true,
    }),
  }
})

vi.mock('@/composables/useAutoRefresh', async () => {
  const { ref } = await vi.importActual<typeof import('vue')>('vue')
  return {
    useAutoRefresh: () => ({
      enabled: ref(false),
      intervalSeconds: ref(10),
      countdown: ref(10),
      intervals: [5, 10, 30, 60],
      setEnabled: vi.fn(),
      setInterval: vi.fn(),
      resetCountdown: vi.fn(),
      start: vi.fn(),
    }),
  }
})

const AppLayoutStub = { template: '<div><slot /></div>' }
const TablePageLayoutStub = {
  template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>',
}
const ConfirmDialogStub = {
  props: ['show', 'title', 'message', 'confirmText', 'cancelText', 'danger'],
  emits: ['confirm', 'cancel'],
  template: '<div v-if="show" data-test="confirm"><button data-test="confirm-action" @click="$emit(\'confirm\')">confirm</button></div>',
}

function mountView(admin = true) {
  return mount(AsyncImageTasksView, {
    props: { admin },
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        AutoRefreshButton: true,
        BaseDialog: true,
        ConfirmDialog: ConfirmDialogStub,
        DataTable: true,
        ImageLightbox: true,
        LoadingSpinner: true,
        Pagination: true,
        Select: true,
        Icon: true,
      },
    },
  })
}

describe('AsyncImageTasksView current-page termination', () => {
  beforeEach(() => {
    Object.values(mocks).forEach((mock) => mock.mockReset())
    mocks.list.mockResolvedValue({
      items: [
        { id: 'asyncimg_queued', task_id: 'asyncimg_queued', platform: 'openai', request_type: 'text_to_image', model: 'gpt-image-1', status: 'queued', created_at: '2026-09-09T00:00:00Z' },
        { id: 'asyncimg_done', task_id: 'asyncimg_done', platform: 'gemini', request_type: 'text_to_image', model: 'gemini-image', status: 'succeeded', created_at: '2026-09-09T00:01:00Z' },
        { id: 'asyncimg_unknown', task_id: 'asyncimg_unknown', platform: 'openai', request_type: 'image_to_image', model: 'gpt-image-1', status: 'execution_unknown', created_at: '2026-09-09T00:02:00Z' },
      ],
      total: 3,
      page: 1,
      page_size: 20,
      pages: 1,
      stats: { active: 1, completed: 1, failed: 1, success_rate: 50, average_duration_ms: 1000 },
    })
    mocks.batchTerminate.mockResolvedValue({
      requested: 2,
      terminated: 1,
      skipped: 1,
      failed: 0,
      items: [
        { task_id: 'asyncimg_queued', status: 'terminated' },
        { task_id: 'asyncimg_unknown', status: 'skipped' },
      ],
    })
  })

  it('submits only terminable task IDs from the loaded admin page after confirmation', async () => {
    const wrapper = mountView()
    await flushPromises()

    const action = wrapper.get('[data-test="batch-terminate-current-page"]')
    expect(action.text()).toContain(':2')
    await action.trigger('click')
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(true)

    await wrapper.get('[data-test="confirm-action"]').trigger('click')
    await flushPromises()

    expect(mocks.batchTerminate).toHaveBeenCalledWith(['asyncimg_queued', 'asyncimg_unknown'])
    expect(mocks.batchTerminate).not.toHaveBeenCalledWith(expect.arrayContaining(['asyncimg_done']))
    expect(mocks.showWarning).toHaveBeenCalledOnce()
    expect(mocks.list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-test="confirm"]').exists()).toBe(false)
  })

  it('does not expose the batch action in the user task center', async () => {
    const wrapper = mountView(false)
    await flushPromises()

    expect(wrapper.find('[data-test="batch-terminate-current-page"]').exists()).toBe(false)
  })
})
