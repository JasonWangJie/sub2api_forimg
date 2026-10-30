import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AsyncImageTasksView from '../AsyncImageTasksView.vue'
import AsyncImageReliabilitySummary from '../AsyncImageReliabilitySummary.vue'

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
const BaseDialogStub = {
  props: ['show', 'title'],
  emits: ['close'],
  template: '<div v-if="show" data-test="detail-dialog"><slot /></div>',
}
const DataTableStub = {
  props: ['data'],
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div><slot v-if="!data.length" name="empty" /></div>',
}

function mountView(admin = true) {
  return mount(AsyncImageTasksView, {
    props: { admin },
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        AutoRefreshButton: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: ConfirmDialogStub,
        DataTable: DataTableStub,
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
    mocks.get.mockResolvedValue({
      id: 'asyncimg_queued',
      task_id: 'asyncimg_queued',
      platform: 'openai',
      protocol: 'bb',
      request_type: 'image_to_image',
      model: 'gpt-image-1',
      status: 'queued',
      prompt_summary: 'Use the product reference',
      reference_image_urls: ['https://cdn.example/reference.png?token=abc'],
      created_at: '2026-09-09T00:00:00Z',
      results: [],
      events: [],
    })
  })

  it('submits only terminable task IDs from the loaded admin page after confirmation', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.findComponent(AsyncImageReliabilitySummary).exists()).toBe(true)

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

  it('does not expose admin actions or reliability metrics in the user task center', async () => {
    const wrapper = mountView(false)
    await flushPromises()

    expect(wrapper.find('[data-test="batch-terminate-current-page"]').exists()).toBe(false)
    expect(wrapper.findComponent(AsyncImageReliabilitySummary).exists()).toBe(false)
  })

  it.each([
    { name: 'one successful request', status: 'succeeded', statuses: ['selected', 'invocation_started', 'succeeded'], calls: '1' },
    { name: 'a genuine same-account retry', status: 'succeeded', statuses: ['selected', 'invocation_started', 'failed', 'invocation_started', 'succeeded'], calls: '2' },
    { name: 'legacy audits without HTTP-start markers', status: 'succeeded', statuses: ['selected', 'succeeded'], calls: '-' },
    { name: 'an empty queued task', status: 'queued', statuses: [], calls: '0' },
    { name: 'an executed legacy task without audits', status: 'succeeded', statuses: [], calls: '-' },
    { name: 'a requeued legacy task with account audits', status: 'queued', statuses: ['selected', 'failed'], calls: '-' },
  ])('distinguishes audit records, accounts, and generation requests for $name', async ({ status, statuses, calls }) => {
    mocks.get.mockResolvedValue({
      id: 'asyncimg_queued',
      task_id: 'asyncimg_queued',
      platform: 'openai',
      request_type: 'text_to_image',
      model: 'gpt-image-2',
      status,
      account_attempt_count: statuses.length ? 1 : 0,
      attempted_account_ids: statuses.length ? [26] : [],
      account_attempts: statuses.map((attemptStatus, index) => ({
        account_id: 26,
        status: attemptStatus,
        attempted_at: `2026-10-30T05:31:${String(index).padStart(2, '0')}Z`,
      })),
      created_at: '2026-10-30T05:30:00Z',
      results: [],
      events: [],
    })

    const wrapper = mountView(true)
    await flushPromises()
    await wrapper.findAll('[data-test="view-task"]')[0].trigger('click')
    await flushPromises()

    const detail = wrapper.get('[data-test="detail-dialog"]')
    function detailValue(label: string): string | undefined {
      return detail.findAll('dt').find((node) => node.text() === `asyncImageTasks.detail.${label}`)
        ?.element.parentElement?.querySelector('dd')?.textContent || undefined
    }
    expect(detailValue('accountAttemptCount')).toBe(statuses.length ? '1' : '0')
    expect(detailValue('generationRequestCount')).toBe(calls)
    const history = detail.find('details summary')
    if (statuses.length) {
      expect(history.text()).toContain(`asyncImageTasks.detail.accountAttemptHistory:${statuses.length}`)
      expect(detail.findAll('tbody tr')).toHaveLength(statuses.length)
    } else {
      expect(history.exists()).toBe(false)
    }
    const startedBadge = detail.findAll('tbody span').find((node) => node.text() === 'asyncImageTasks.accountAttemptStatus.invocation_started')
    if (statuses.includes('invocation_started')) {
      expect(startedBadge?.classes()).toContain('text-amber-700')
      expect(startedBadge?.classes()).not.toContain('text-rose-700')
    }
    wrapper.unmount()
  })

  it('keeps result-fetch timeline labels and uses progress or success colors', async () => {
    mocks.get.mockResolvedValue({
      id: 'asyncimg_queued',
      task_id: 'asyncimg_queued',
      platform: 'openai',
      request_type: 'text_to_image',
      model: 'gpt-image-2',
      status: 'succeeded',
      created_at: '2026-10-30T05:30:00Z',
      results: [],
      events: ['result_fetch_pending', 'result_fetch_attempt', 'result_fetch_completed'].map((status, index) => ({
        id: index,
        status,
        created_at: `2026-10-30T05:31:0${index}Z`,
      })),
    })
    const wrapper = mountView(true)
    await flushPromises()
    await wrapper.findAll('[data-test="view-task"]')[0].trigger('click')
    await flushPromises()

    const timeline = wrapper.get('[data-test="detail-dialog"] ol')
    expect(timeline.text()).toContain('asyncImageTasks.status.result_fetch_pending')
    expect(timeline.text()).not.toContain('asyncImageTasks.status.upstream_succeeded')
    const events = timeline.findAll('li')
    expect(events[0].find('.rounded-full').classes()).toContain('bg-amber-500')
    expect(events[1].find('.rounded-full').classes()).toContain('bg-amber-500')
    expect(events[2].find('.rounded-full').classes()).toContain('bg-emerald-500')
    wrapper.unmount()
  })

  it('shows persisted reference image URLs only in the admin detail, copies on link click, and opens via the icon', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })

    const wrapper = mountView(true)
    await flushPromises()

    await wrapper.findAll('[data-test="view-task"]')[0].trigger('click')
    await flushPromises()

    const link = wrapper.get('[data-test="reference-image-link"]')
    expect(link.text()).toContain('https://cdn.example/reference.png?token=abc')
    expect(link.element.tagName).toBe('BUTTON')

    await link.trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('https://cdn.example/reference.png?token=abc')
    expect(mocks.showSuccess).toHaveBeenCalledWith('common.copied')

    await wrapper.get('[data-test="copy-all-reference-images"]').trigger('click')
    await flushPromises()
    expect(writeText).toHaveBeenCalledWith('https://cdn.example/reference.png?token=abc')

    const openLink = wrapper.get('[data-test="open-reference-image"]')
    expect(openLink.attributes('href')).toBe('https://cdn.example/reference.png?token=abc')
    expect(openLink.attributes('target')).toBe('_blank')
    expect(openLink.attributes('rel')).toBe('noopener noreferrer')
  })

  it('copies all reference image URLs one per line', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined)
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText },
    })
    mocks.get.mockResolvedValue({
      id: 'asyncimg_queued',
      task_id: 'asyncimg_queued',
      platform: 'openai',
      request_type: 'image_to_image',
      model: 'gpt-image-1',
      status: 'queued',
      created_at: '2026-09-09T00:00:00Z',
      reference_image_urls: [
        'https://cdn.example/a.png?token=1',
        'https://cdn.example/b.png?token=2',
      ],
      results: [],
      events: [],
    })

    const wrapper = mountView(true)
    await flushPromises()
    await wrapper.findAll('[data-test="view-task"]')[0].trigger('click')
    await flushPromises()

    await wrapper.get('[data-test="copy-all-reference-images"]').trigger('click')
    await flushPromises()

    expect(writeText).toHaveBeenCalledWith(
      'https://cdn.example/a.png?token=1\nhttps://cdn.example/b.png?token=2',
    )
    expect(mocks.showSuccess).toHaveBeenCalledWith('common.copied')
  })

  it('does not render reference image URLs in the user detail', async () => {
    const wrapper = mountView(false)
    await flushPromises()

    await wrapper.findAll('[data-test="view-task"]')[0].trigger('click')
    await flushPromises()

    expect(wrapper.find('[data-test="reference-image-link"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="copy-all-reference-images"]').exists()).toBe(false)
  })
})
