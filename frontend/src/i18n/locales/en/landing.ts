export default {
  batchImageGuide: {
    title: 'Batch Image Generation',
    description: 'Submit multiple prompts in one job and download the generated images when complete'
  },
  imageWorkbench: {
    title: 'Image Workbench',
    description: 'Pick a key and model, craft a prompt, and generate images in one place',
    selectKey: 'Select Key',
    selectKeyPlaceholder: 'Select an image-capable API key',
    detectedImageKeys: 'Detected {n} image-capable key(s)',
    imageEnabledBadge: 'Image enabled',
    usableForImage: 'Image ready',
    apiKeyLabel: 'API Key',
    price2kLabel: '2K Price',
    statusLabel: 'Status',
    ungrouped: 'Ungrouped',
    noImageKeyHint: 'No image-capable keys found. Enable image generation on a group first.',
    keyNotAllowImage: 'This key’s group does not allow image generation',
    noKeyHint: 'Create an active API key first',
    noKeys: 'No keys available',
    model: 'Model',
    reference: 'Reference Image',
    uploadTitle: 'Upload reference',
    uploadHint: 'PNG/JPG/WEBP. Uploading switches to the image edits API.',
    clearRef: 'Clear',
    referenceLimit: 'Up to {n} reference images',
    promptParams: 'Prompt & Parameters',
    promptPlaceholder: 'Describe the image you want, e.g. neon rain alley in a cyberpunk night…',
    endpointGenerations: 'No reference image — calling {url}',
    endpointEdits: 'Reference image uploaded — calling {url}',
    size: 'Size',
    count: 'Images',
    quality: 'Quality',
    format: 'Output Format',
    background: 'Background',
    backgroundTransparent: 'Transparent',
    backgroundOpaque: 'Opaque',
    style: 'Style',
    styleVivid: 'Vivid',
    styleNatural: 'Natural',
    auto: 'Auto',
    qualityHigh: 'High',
    qualityMedium: 'Medium',
    qualityLow: 'Low',
    syncPlaza: 'Sync to Image Plaza',
    start: 'Generate',
    generating: 'Generating…',
    clearPrompt: 'Clear Prompt',
    reset: 'Reset',
    resultPreview: 'Result Preview',
    emptyPreview: 'Generated images will appear here',
    history: 'History',
    emptyHistory: 'No generations yet',
    download: 'Download',
    publish: 'Publish',
    unpublish: 'Unpublish',
    view: 'View',
    oneClick: 'Reuse',
    detail: 'Details',
    unlimited: 'Unlimited',
    loadKeysFailed: 'Failed to load API keys',
    invalidImage: 'Only PNG / JPG / WEBP are supported',
    emptyResult: 'Upstream returned no image',
    generateSuccess: 'Image generated',
    generateFailed: 'Image generation failed',
    published: 'Published to Image Plaza',
    unpublished: 'Unpublished',
    publishFailed: 'Failed to sync to Image Plaza',
    reused: 'Parameters loaded',
    sizes: {
      square1k: '1K Square · 1024 × 1024',
      landscape1k: '1K Landscape · 1536 × 1024',
      portrait1k: '1K Portrait · 1024 × 1536',
      square2k: '2K Square · 2048 × 2048',
      landscape2k: '2K Landscape · 2048 × 1152',
      portrait2k: '2K Portrait · 1152 × 2048',
      square4k: '4K Square · 4096 × 4096',
      landscape4k: '4K Landscape · 4096 × 2304',
      portrait4k: '4K Portrait · 2304 × 4096'
    }
  },
  imagePlaza: {
    title: 'Image Plaza',
    description: 'Browse publicly shared images from all users — search, download, or reuse',
    searchPlaceholder: 'Search prompt, tags, time',
    selectAllPage: 'Select all on this page',
    sharedHint: 'Global feed · admins can remove posts; members can report',
    total: '{n} images',
    empty: 'No public images yet — generate one and sync to the plaza',
    goWorkbench: 'Open Image Workbench',
    oneClickSame: 'Same Style',
    reuse: 'Open in Workbench',
    bulkDelete: 'Delete selected ({n})',
    loadFailed: 'Failed to load Image Plaza'
  },
  // Home Page
  home: {
    viewOnGithub: 'View on GitHub',
    viewDocs: 'View Documentation',
    docs: 'User Manual',
    switchToLight: 'Switch to Light Mode',
    switchToDark: 'Switch to Dark Mode',
    dashboard: 'Dashboard',
    login: 'Login',
    getStarted: 'Get Started',
    goToDashboard: 'Go to Dashboard',
    signalLive: 'Routing Live · Realtime Dispatch',
    openWorkbench: 'Open Image Workbench',
    authBrandSub: 'API Gateway',
    authChannel: 'Auth Channel',
    authHome: 'Home',
    authSecureSession: 'Secure Session',
    authPanelCode: '// auth.gateway',
    // User-focused value proposition
    heroSubtitle: 'One Key, All AI Models',
    heroDescription: 'No need to manage multiple subscriptions. Access Claude, GPT, Gemini and more with a single API key',
    tags: {
      subscriptionToApi: 'Subscription to API',
      stickySession: 'Session Persistence',
      realtimeBilling: 'Pay As You Go'
    },
    // Pain points section
    painPoints: {
      title: 'Sound Familiar?',
      items: {
        expensive: {
          title: 'High Subscription Costs',
          desc: 'Paying for multiple AI subscriptions that add up every month'
        },
        complex: {
          title: 'Account Chaos',
          desc: 'Managing scattered accounts and API keys across different platforms'
        },
        unstable: {
          title: 'Service Interruptions',
          desc: 'Single accounts hitting rate limits and disrupting your workflow'
        },
        noControl: {
          title: 'No Usage Control',
          desc: "Can't track where your money goes or limit team member usage"
        }
      }
    },
    // Solutions section
    solutions: {
      title: 'We Solve These Problems',
      subtitle: 'Three simple steps to stress-free AI access'
    },
    features: {
      unifiedGateway: 'One-Click Access',
      unifiedGatewayDesc: 'Get a single API key to call all connected AI models. No separate applications needed.',
      multiAccount: 'Always Reliable',
      multiAccountDesc: 'Smart routing across multiple upstream accounts with automatic failover. Say goodbye to errors.',
      balanceQuota: 'Pay What You Use',
      balanceQuotaDesc: 'Usage-based billing with quota limits. Full visibility into team consumption.'
    },
    // Comparison section
    comparison: {
      title: 'Why Choose Us?',
      headers: {
        feature: 'Comparison',
        official: 'Official Subscriptions',
        us: 'Our Platform'
      },
      items: {
        pricing: {
          feature: 'Pricing',
          official: 'Fixed monthly fee, pay even if unused',
          us: 'Pay only for what you use'
        },
        models: {
          feature: 'Model Selection',
          official: 'Single provider only',
          us: 'Switch between models freely'
        },
        management: {
          feature: 'Account Management',
          official: 'Manage each service separately',
          us: 'Unified key, one dashboard'
        },
        stability: {
          feature: 'Stability',
          official: 'Single account rate limits',
          us: 'Multi-account pool, auto-failover'
        },
        control: {
          feature: 'Usage Control',
          official: 'Not available',
          us: 'Quotas & detailed analytics'
        }
      }
    },
    providers: {
      title: 'Supported AI Models',
      description: 'One API, Multiple Choices',
      supported: 'Supported',
      soon: 'Soon',
      claude: 'Claude',
      gemini: 'Gemini',
      antigravity: 'Antigravity',
      more: 'More'
    },
    // CTA section
    cta: {
      title: 'Ready to Get Started?',
      description: 'Sign up now and get free trial credits to experience seamless AI access',
      button: 'Sign Up Free'
    },
    footer: {
      allRightsReserved: 'All rights reserved.',
      channels: 'Quick Links',
      tagline: 'Unified ingress, smart routing, usage billing — one stable relay for every model.'
    }
  },

  // Key Usage Query Page
  keyUsage: {
    title: 'API Key Usage',
    subtitle: 'Enter your API Key to view real-time spending and usage status',
    placeholder: 'sk-ant-mirror-xxxxxxxxxxxx',
    query: 'Query',
    querying: 'Querying...',
    privacyNote: 'Your Key is processed locally in the browser and will not be stored',
    dateRange: 'Date Range:',
    dateRangeToday: 'Today',
    dateRange7d: '7 Days',
    dateRange30d: '30 Days',
    dateRange90d: '90 Days',
    dateRangeCustom: 'Custom',
    apply: 'Apply',
    used: 'Used',
    detailInfo: 'Detail Information',
    tokenStats: 'Token Statistics',
    dailyDetail: 'Daily Detail',
    modelStats: 'Model Usage Statistics',
    // Table headers
    date: 'Date',
    model: 'Model',
    requests: 'Requests',
    inputTokens: 'Input Tokens',
    outputTokens: 'Output Tokens',
    cacheCreationTokens: 'Cache Creation',
    cacheReadTokens: 'Cache Read',
    cacheWriteTokens: 'Cache Write',
    totalTokens: 'Total Tokens',
    cost: 'Cost',
    // Status
    quotaMode: 'Key Quota Mode',
    walletBalance: 'Wallet Balance',
    // Ring card titles
    totalQuota: 'Total Quota',
    limit5h: '5-Hour Limit',
    limitDaily: 'Daily Limit',
    limit7d: '7-Day Limit',
    limitWeekly: 'Weekly Limit',
    limitMonthly: 'Monthly Limit',
    // Detail rows
    remainingQuota: 'Remaining Quota',
    expiresAt: 'Expires At',
    todayExpires: '(expires today)',
    daysLeft: '({days} days)',
    usedQuota: 'Used Quota',
    resetNow: 'Resetting soon',
    subscriptionType: 'Subscription Type',
    subscriptionExpires: 'Subscription Expires',
    // Usage stat cells
    todayRequests: 'Today Requests',
    todayInputTokens: 'Today Input',
    todayOutputTokens: 'Today Output',
    todayTokens: 'Today Tokens',
    todayCacheCreation: 'Today Cache Creation',
    todayCacheRead: 'Today Cache Read',
    todayCost: 'Today Cost',
    rpmTpm: 'RPM / TPM',
    totalRequests: 'Total Requests',
    totalInputTokens: 'Total Input',
    totalOutputTokens: 'Total Output',
    totalTokensLabel: 'Total Tokens',
    totalCacheCreation: 'Total Cache Creation',
    totalCacheRead: 'Total Cache Read',
    totalCost: 'Total Cost',
    avgDuration: 'Avg Duration',
    // Messages
    enterApiKey: 'Please enter an API Key',
    querySuccess: 'Query successful',
    queryFailed: 'Query failed',
    queryFailedRetry: 'Query failed, please try again later',
    noDailyUsage: 'No daily usage data',
  },

  // Setup Wizard
  setup: {
    title: 'Sub2API Setup',
    description: 'Configure your Sub2API instance',
    database: {
      title: 'Database Configuration',
      description: 'Connect to your PostgreSQL database',
      host: 'Host',
      port: 'Port',
      username: 'Username',
      password: 'Password',
      databaseName: 'Database Name',
      sslMode: 'SSL Mode',
      passwordPlaceholder: 'Password',
      ssl: {
        disable: 'Disable',
        require: 'Require',
        verifyCa: 'Verify CA',
        verifyFull: 'Verify Full'
      }
    },
    redis: {
      title: 'Redis Configuration',
      description: 'Connect to your Redis server',
      host: 'Host',
      port: 'Port',
      username: 'Username (optional)',
      password: 'Password (optional)',
      database: 'Database',
      usernamePlaceholder: 'Leave empty for default user',
      passwordPlaceholder: 'Password',
      enableTls: 'Enable TLS',
      enableTlsHint: 'Use TLS when connecting to Redis (public CA certs)'
    },
    admin: {
      title: 'Admin Account',
      description: 'Create your administrator account',
      email: 'Email',
      password: 'Password',
      confirmPassword: 'Confirm Password',
      passwordPlaceholder: 'Min 8 characters',
      confirmPasswordPlaceholder: 'Confirm password',
      passwordMismatch: 'Passwords do not match'
    },
    ready: {
      title: 'Ready to Install',
      description: 'Review your configuration and complete setup',
      database: 'Database',
      redis: 'Redis',
      adminEmail: 'Admin Email'
    },
    status: {
      testing: 'Testing...',
      success: 'Connection Successful',
      testConnection: 'Test Connection',
      installing: 'Installing...',
      completeInstallation: 'Complete Installation',
      completed: 'Installation completed!',
      redirecting: 'Redirecting to login page...',
      restarting: 'Service is restarting, please wait...',
      timeout: 'Service restart is taking longer than expected. Please refresh the page manually.'
    }
  },

  // Common
}
