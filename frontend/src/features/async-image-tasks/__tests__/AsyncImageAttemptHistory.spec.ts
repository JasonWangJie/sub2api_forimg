import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { baseCompile } from '@intlify/message-compiler'
import AsyncImageAttemptHistory from '../AsyncImageAttemptHistory.vue'
import zh from '@/i18n/locales/zh/asyncImageTasks'
import en from '@/i18n/locales/en/asyncImageTasks'
import type { AsyncImageAccountAttempt } from '../types'

// Vitest aliases vue-i18n to its runtime build; precompile these locale fixtures.
function compileMessages(node: unknown): unknown {
  if (typeof node === 'string') return new Function(`return (${baseCompile(node, { mode: 'arrow' }).code})`)()
  if (node && typeof node === 'object') return Object.fromEntries(Object.entries(node).map(([key, value]) => [key, compileMessages(value)]))
  return node
}
const messages = compileMessages({ zh, en }) as { zh: typeof zh; en: typeof en }

function stage(status: string, second: number, attempt_id?: string, account_id = 2): AsyncImageAccountAttempt {
  return { account_id, account_name: `Channel ${account_id}`, attempt_id, event_id: `event-${second}`, status, attempted_at: `2026-10-31T10:00:${String(second).padStart(2, '0')}Z` }
}
function render(attempts: AsyncImageAccountAttempt[], locale = 'zh') {
  return mount(AsyncImageAttemptHistory, {
    props: { attempts },
    global: { plugins: [createI18n({ legacy: false, locale, messages })], stubs: { Icon: true } },
  })
}

describe('image call attempt disclosure', () => {
  it.each([
    { locale: 'zh', success: '上游成功', stages: '3 条阶段记录' },
    { locale: 'en', success: 'Upstream succeeded', stages: '3 stage records' },
  ])('shows one success row and a native collapsed stage disclosure in $locale', async ({ locale, success, stages }) => {
    const wrapper = render([stage('selected', 0, 'a'), stage('invocation_started', 1, 'a'), stage('succeeded', 4, 'a')], locale)
    expect(wrapper.findAll('[data-test="call-attempt"]')).toHaveLength(1)
    expect(wrapper.find('[data-test="ungrouped-attempts"]').exists()).toBe(false)
    const summary = wrapper.get('summary')
    expect(summary.text()).toContain(success)
    expect(summary.text()).toContain(stages)
    expect(summary.text()).toContain('3.0 s')
    const disclosure = wrapper.get('details').element as HTMLDetailsElement
    expect(disclosure.open).toBe(false)
    await summary.trigger('click')
    expect(disclosure.open).toBe(true)
    expect(wrapper.findAll('[data-test="attempt-stage"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('event-4')
    await summary.trigger('click')
    expect(disclosure.open).toBe(false)
  })

  it('shows failure reasons immediately and distinguishes retrying the same channel from switching', () => {
    const failed = { ...stage('failed', 2, 'a'), status_code: 503, provider_error_code: 'UNAVAILABLE', error: 'Capacity exhausted', failure_reason: 'capacity_rejected' }
    const wrapper = render([stage('invocation_started', 1, 'a'), failed, stage('invocation_started', 3, 'b'), stage('failed', 4, 'b'), stage('invocation_started', 5, 'c', 3), stage('succeeded', 6, 'c', 3)])
    const attempts = wrapper.findAll('[data-test="call-attempt"]')
    expect(attempts).toHaveLength(3)
    expect(attempts[0].get('summary').text()).toContain('HTTP 503')
    expect(attempts[0].get('summary').text()).toContain('UNAVAILABLE')
    expect(attempts[0].get('summary').text()).toContain('Capacity exhausted')
    expect(attempts[0].get('[data-test="call-next"]').text()).toContain('同渠道重试')
    expect(attempts[1].get('[data-test="call-next"]').text()).toContain('切换到 Channel 3')
    expect(wrapper.text()).toContain('capacity_rejected')
  })

  it('shows selection failure and missing records without claiming generation or billing success', () => {
    const wrapper = render([stage('selected', 0, 'a'), stage('failed', 1, 'a'), stage('selected', 2, 'b'), stage('invocation_started', 3, 'c')])
    const outcomes = wrapper.findAll('[data-test="call-outcome"]')
    expect(outcomes.map(node => node.text())).toEqual(['发送前失败', '未记录发送', '缺少结束记录'])
    expect(outcomes[2].classes()).toContain('text-gray-600')
    expect(wrapper.findAll('summary').every(node => !node.text().includes('计费'))).toBe(true)
  })

  it('keeps success plus a later processing failure in one call and separates that diagnostic', () => {
    const wrapper = render([stage('invocation_started', 0, 'a'), stage('succeeded', 2, 'a'), { ...stage('failed', 20, 'a'), error: 'Result download failed', status_code: 502 }])
    expect(wrapper.findAll('[data-test="call-attempt"]')).toHaveLength(1)
    expect(wrapper.get('[data-test="call-outcome"]').text()).toBe('上游成功')
    expect(wrapper.get('summary').text()).toContain('2.0 s')
    expect(wrapper.get('[data-test="post-success-failure"]').text()).toContain('上游成功后的处理诊断')
    expect(wrapper.get('[data-test="post-success-failure"]').text()).toContain('HTTP 502')
    expect(wrapper.find('[data-test="call-failure"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-test="attempt-stage"]')).toHaveLength(3)
  })

  it('keeps legacy and conflicting raw records without combining them into calls', () => {
    const records = [stage('selected', 0), stage('invocation_started', 1), stage('succeeded', 2), stage('selected', 3, 'conflict'), stage('failed', 4, 'conflict', 3)]
    const wrapper = render(records)
    expect(wrapper.findAll('[data-test="call-attempt"]')).toHaveLength(0)
    expect(wrapper.get('[data-test="ungrouped-attempts"] summary').text()).toContain('5 条')
    expect(wrapper.findAll('[data-test="attempt-stage"]')).toHaveLength(5)
    expect(wrapper.text()).toContain('无法可靠归组')
    expect(wrapper.text()).toContain('event-4')
  })
})
