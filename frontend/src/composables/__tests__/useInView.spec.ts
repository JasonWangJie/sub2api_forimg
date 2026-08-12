import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import { mount } from '@vue/test-utils'
import { useInView } from '../useInView'

class MockIntersectionObserver {
  static instances: MockIntersectionObserver[] = []
  callback: IntersectionObserverCallback
  observed: Element[] = []

  constructor(callback: IntersectionObserverCallback) {
    this.callback = callback
    MockIntersectionObserver.instances.push(this)
  }

  observe(el: Element) {
    this.observed.push(el)
  }

  disconnect() {
    this.observed = []
  }

  unobserve() {}

  trigger(isIntersecting: boolean) {
    this.callback(
      this.observed.map((target) => ({
        isIntersecting,
        target,
        intersectionRatio: isIntersecting ? 1 : 0,
      })) as IntersectionObserverEntry[],
      this as unknown as IntersectionObserver,
    )
  }
}

describe('useInView', () => {
  beforeEach(() => {
    MockIntersectionObserver.instances = []
    vi.stubGlobal('IntersectionObserver', MockIntersectionObserver)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('marks inView when the observed element intersects', async () => {
    let api: ReturnType<typeof useInView> | null = null
    const Comp = defineComponent({
      setup() {
        api = useInView({ rootMargin: '0px' })
        return { target: api.target, inView: api.inView }
      },
      template: '<div ref="target">tile</div>',
    })
    mount(Comp)
    await nextTick()
    expect(api?.inView.value).toBe(false)
    const observer = MockIntersectionObserver.instances.at(-1)
    expect(observer).toBeTruthy()
    observer?.trigger(true)
    expect(api?.inView.value).toBe(true)
  })
})
