import { onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'

export interface UseInViewOptions {
  /** CSS margin around the root. Default '240px 0px'. */
  rootMargin?: string
  /** Intersection threshold. Default 0.01. */
  threshold?: number | number[]
  /** Explicit scroll root; when omitted, nearest overflow ancestor is used. */
  root?: Element | null
  /** Stop observing after first intersection. Default true. */
  once?: boolean
}

function findScrollParent(el: HTMLElement | null): Element | null {
  let node = el?.parentElement || null
  while (node && node !== document.documentElement) {
    const style = window.getComputedStyle(node)
    const overflowY = style.overflowY
    if (
      (overflowY === 'auto' || overflowY === 'scroll' || overflowY === 'overlay')
      && node.scrollHeight > node.clientHeight + 1
    ) {
      return node
    }
    node = node.parentElement
  }
  return null
}

/**
 * Observe when an element enters the viewport (or a scrollable ancestor).
 * Useful for media grids inside overflow containers where native img lazy
 * loading is unreliable.
 */
export function useInView(options: UseInViewOptions = {}) {
  const target = ref<HTMLElement | null>(null)
  const inView = ref(false)
  let observer: IntersectionObserver | null = null

  function disconnect() {
    observer?.disconnect()
    observer = null
  }

  function observe() {
    disconnect()
    const el = target.value
    if (!el || typeof IntersectionObserver === 'undefined') {
      inView.value = true
      return
    }
    if (inView.value && options.once !== false) return

    const root = options.root === undefined ? findScrollParent(el) : options.root
    observer = new IntersectionObserver(
      (entries) => {
        if (!entries.some((entry) => entry.isIntersecting)) return
        inView.value = true
        if (options.once !== false) disconnect()
      },
      {
        root,
        rootMargin: options.rootMargin ?? '240px 0px',
        threshold: options.threshold ?? 0.01,
      },
    )
    observer.observe(el)
  }

  onMounted(observe)
  watch(target, () => {
    if (options.once !== false && inView.value) return
    observe()
  })
  onBeforeUnmount(disconnect)

  return {
    target: target as Ref<HTMLElement | null>,
    inView: inView as Ref<boolean>,
    reconnect: observe,
  }
}
