import { defineConfig, loadEnv, Plugin } from 'vite'
import vue from '@vitejs/plugin-vue'
import checker from 'vite-plugin-checker'
import { resolve } from 'path'

function escapeHtml(value: string): string {
  return value.replace(/[&<>"']/g, (character) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;',
  })[character] || character)
}

function isSafeImageUrl(value: string): boolean {
  const trimmed = value.trim()
  if ((trimmed.startsWith('/') && !trimmed.startsWith('//')) || /^data:image\//i.test(trimmed)) {
    return true
  }
  try {
    const parsed = new URL(trimmed)
    return parsed.protocol === 'http:' || parsed.protocol === 'https:'
  } catch {
    return false
  }
}

interface PublicSEOConfig {
  site_name?: string
  site_logo?: string
  site_subtitle?: string
  seo_indexing_enabled?: boolean
  seo_site_url?: string
  seo_title?: string
  seo_keywords?: string[]
  seo_description?: string
  seo_social_image_url?: string
  seo_verification_tags?: string
}

function safeSiteRootUrl(value?: string): string {
  if (!value?.trim()) return ''
  try {
    const parsed = new URL(value.trim())
    if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) return ''
    if ((parsed.pathname && parsed.pathname !== '/') || parsed.search || parsed.hash) return ''
    parsed.pathname = '/'
    return parsed.toString()
  } catch {
    return ''
  }
}

function injectSEO(html: string, config: PublicSEOConfig): string {
  const siteName = config.site_name?.trim() || 'Sub2API'
  const title = config.seo_title?.trim() || `${siteName} - AI API Gateway`
  const description = config.seo_description?.trim() || config.site_subtitle?.trim() || 'Subscription to API Conversion Platform'
  const siteUrl = safeSiteRootUrl(config.seo_site_url)
  const socialImage = config.seo_social_image_url?.trim() && isSafeImageUrl(config.seo_social_image_url)
    ? config.seo_social_image_url.trim()
    : ''
  const directive = config.seo_indexing_enabled === false
    ? 'noindex, nofollow'
    : 'index, follow, max-image-preview:large, max-snippet:-1, max-video-preview:-1'
  const lines = [
    '<!-- SEO_SETTINGS_START -->',
    `    <title>${escapeHtml(title)}</title>`,
    `    <meta name="description" content="${escapeHtml(description)}" />`,
  ]
  if (config.seo_keywords?.length) {
    lines.push(`    <meta name="keywords" content="${escapeHtml(config.seo_keywords.join(', '))}" />`)
  }
  for (const name of ['robots', 'googlebot', 'bingbot']) {
    lines.push(`    <meta name="${name}" content="${directive}" />`)
  }
  lines.push(`    <meta name="application-name" content="${escapeHtml(siteName)}" />`)
  if (siteUrl) lines.push(`    <link rel="canonical" href="${escapeHtml(siteUrl)}" />`)
  lines.push('    <meta property="og:type" content="website" />')
  lines.push(`    <meta property="og:site_name" content="${escapeHtml(siteName)}" />`)
  if (siteUrl) lines.push(`    <meta property="og:url" content="${escapeHtml(siteUrl)}" />`)
  lines.push(`    <meta property="og:title" content="${escapeHtml(title)}" />`)
  lines.push(`    <meta property="og:description" content="${escapeHtml(description)}" />`)
  if (socialImage) lines.push(`    <meta property="og:image" content="${escapeHtml(socialImage)}" />`)
  lines.push(`    <meta name="twitter:card" content="${socialImage ? 'summary_large_image' : 'summary'}" />`)
  lines.push(`    <meta name="twitter:title" content="${escapeHtml(title)}" />`)
  lines.push(`    <meta name="twitter:description" content="${escapeHtml(description)}" />`)
  if (socialImage) lines.push(`    <meta name="twitter:image" content="${escapeHtml(socialImage)}" />`)
  if (siteUrl) {
    const structuredData = JSON.stringify({
      '@context': 'https://schema.org',
      '@type': 'WebSite',
      name: siteName,
      url: siteUrl,
      description,
    }).replace(/</g, '\\u003c')
    lines.push(`    <script type="application/ld+json">${structuredData}</script>`)
  }
  for (const tag of config.seo_verification_tags?.split('\n') ?? []) {
    const trimmed = tag.trim()
    if (/^<meta name="[A-Za-z0-9._:-]+" content="[^"<>]*" \/>$/.test(trimmed)) {
      lines.push(`    ${trimmed}`)
    }
  }
  lines.push('    <!-- SEO_SETTINGS_END -->')
  return html.replace(
    /<!-- SEO_SETTINGS_START -->[\s\S]*?<!-- SEO_SETTINGS_END -->/,
    lines.join('\n'),
  )
}

function injectBranding(html: string, config: PublicSEOConfig): string {
  let brandedHtml = injectSEO(html, config)

  const siteLogo = config.site_logo?.trim()
  if (siteLogo && isSafeImageUrl(siteLogo)) {
    brandedHtml = brandedHtml.replace(
      /<link\s+rel=["']icon["'][^>]*>/i,
      `<link rel="icon" href="${escapeHtml(siteLogo)}" />`,
    )
  }
  return brandedHtml
}

/**
 * Vite 插件：开发模式下注入公开配置到 index.html
 * 与生产模式的后端注入行为保持一致，消除闪烁
 */
function injectPublicSettings(backendUrl: string): Plugin {
  return {
    name: 'inject-public-settings',
    apply: 'serve',
    transformIndexHtml: {
      order: 'pre',
      async handler(html) {
        try {
          const response = await fetch(`${backendUrl}/api/v1/settings/public`, {
            signal: AbortSignal.timeout(2000)
          })
          if (response.ok) {
            const data = await response.json()
            if (data.code === 0 && data.data) {
              const script = `<script>window.__APP_CONFIG__=${JSON.stringify(data.data)};</script>`
              return injectBranding(html, data.data).replace('</head>', `${script}\n</head>`)
            }
          }
        } catch (e) {
          console.warn('[vite] 无法获取公开配置，将回退到 API 调用:', (e as Error).message)
        }
        return html
      }
    }
  }
}

export default defineConfig(({ mode }) => {
  // 加载环境变量
  const env = loadEnv(mode, process.cwd(), '')
  const backendUrl = env.VITE_DEV_PROXY_TARGET || 'http://localhost:8080'
  const devPort = Number(env.VITE_DEV_PORT || 3000)

  return {
    plugins: [
      vue(),
      checker({
        vueTsc: true
      }),
      injectPublicSettings(backendUrl)
    ],
  resolve: {
    alias: {
      '@': resolve(__dirname, 'src'),
      // 使用 vue-i18n 运行时版本，避免 CSP unsafe-eval 问题
      'vue-i18n': 'vue-i18n/dist/vue-i18n.runtime.esm-bundler.js'
    }
  },
  define: {
    // 启用 vue-i18n JIT 编译，在 CSP 环境下处理消息插值
    // JIT 编译器生成 AST 对象而非 JS 代码，无需 unsafe-eval
    __INTLIFY_JIT_COMPILATION__: true
  },
  build: {
    outDir: '../backend/internal/web/dist',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        /**
         * 手动分包配置
         * 分离第三方库并按功能合并应用代码，避免循环依赖
         */
        manualChunks(id: string) {
          if (id.includes('node_modules')) {
            // Vue 核心库
            if (
              id.includes('/vue/') ||
              id.includes('/vue-router/') ||
              id.includes('/pinia/') ||
              id.includes('/@vue/')
            ) {
              return 'vendor-vue'
            }

            // UI 工具库（较大，单独分离）
            if (id.includes('/@vueuse/') || id.includes('/xlsx/')) {
              return 'vendor-ui'
            }

            // 图表库
            if (id.includes('/chart.js/') || id.includes('/vue-chartjs/')) {
              return 'vendor-chart'
            }

            // 国际化
            if (id.includes('/vue-i18n/') || id.includes('/@intlify/')) {
              return 'vendor-i18n'
            }

            // Stripe 仅在支付流程中按需加载，避免进入首页公共依赖。
            if (id.includes('/@stripe/stripe-js/')) {
              return 'vendor-stripe'
            }

            // 其他小型第三方库合并
            return 'vendor-misc'
          }

          // 应用代码：按入口点自动分包，不手动干预
          // 这样可以避免循环依赖，同时保持合理的 chunk 数量
        }
      }
    }
  },
    server: {
      host: '0.0.0.0',
      port: devPort,
      proxy: {
        '/api': {
          target: backendUrl,
          changeOrigin: true
        },
        '/v1': {
          target: backendUrl,
          changeOrigin: true
        },
        '/setup': {
          target: backendUrl,
          changeOrigin: true
        }
      }
    }
  }
})
