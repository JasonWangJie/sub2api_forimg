/**
 * IndexedDB-backed gallery for image workbench / plaza.
 * Keeps generated images locally until a server-side gallery exists.
 */

export interface GalleryImageRecord {
  id: string
  prompt: string
  title: string
  model: string
  size: string
  quality: string
  format: string
  n: number
  background?: string
  /** @deprecated use background */
  sampling?: string
  style: string
  apiKeyId: number
  apiKeyName: string
  imageDataUrl: string
  public: boolean
  source: 'workbench' | 'plaza'
  createdAt: number
}

const DB_NAME = 'sub2api-image-gallery'
const DB_VERSION = 1
const STORE = 'images'
const MAX_RECORDS = 120

function openDb(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, DB_VERSION)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(STORE)) {
        const store = db.createObjectStore(STORE, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt', { unique: false })
        store.createIndex('public', 'public', { unique: false })
      }
    }
    req.onsuccess = () => resolve(req.result)
    req.onerror = () => reject(req.error || new Error('Failed to open image gallery DB'))
  })
}

function txDone(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve()
    tx.onerror = () => reject(tx.error || new Error('Image gallery transaction failed'))
    tx.onabort = () => reject(tx.error || new Error('Image gallery transaction aborted'))
  })
}

export async function listGalleryImages(opts?: {
  publicOnly?: boolean
  query?: string
}): Promise<GalleryImageRecord[]> {
  const db = await openDb()
  const tx = db.transaction(STORE, 'readonly')
  const store = tx.objectStore(STORE)
  const req = store.index('createdAt').getAll()
  const rows = await new Promise<GalleryImageRecord[]>((resolve, reject) => {
    req.onsuccess = () => resolve((req.result as GalleryImageRecord[]) || [])
    req.onerror = () => reject(req.error)
  })
  await txDone(tx)
  db.close()

  let list = rows.slice().sort((a, b) => b.createdAt - a.createdAt)
  if (opts?.publicOnly) {
    list = list.filter((item) => item.public)
  }
  const q = (opts?.query || '').trim().toLowerCase()
  if (q) {
    list = list.filter((item) => {
      const hay = `${item.title} ${item.prompt} ${item.model} ${item.source}`.toLowerCase()
      return hay.includes(q) || new Date(item.createdAt).toLocaleString().toLowerCase().includes(q)
    })
  }
  return list
}

export async function getGalleryImage(id: string): Promise<GalleryImageRecord | null> {
  const db = await openDb()
  const tx = db.transaction(STORE, 'readonly')
  const store = tx.objectStore(STORE)
  const req = store.get(id)
  const row = await new Promise<GalleryImageRecord | null>((resolve, reject) => {
    req.onsuccess = () => resolve((req.result as GalleryImageRecord) || null)
    req.onerror = () => reject(req.error)
  })
  await txDone(tx)
  db.close()
  return row
}

export async function saveGalleryImage(
  record: Omit<GalleryImageRecord, 'id' | 'createdAt'> & { id?: string; createdAt?: number }
): Promise<GalleryImageRecord> {
  const item: GalleryImageRecord = {
    ...record,
    id: record.id || `img_${Date.now()}_${Math.random().toString(36).slice(2, 9)}`,
    createdAt: record.createdAt || Date.now()
  }

  const db = await openDb()
  const tx = db.transaction(STORE, 'readwrite')
  const store = tx.objectStore(STORE)
  store.put(item)

  // Cap storage: drop oldest beyond MAX_RECORDS
  const allReq = store.index('createdAt').getAllKeys()
  const keys = await new Promise<IDBValidKey[]>((resolve, reject) => {
    allReq.onsuccess = () => resolve((allReq.result as IDBValidKey[]) || [])
    allReq.onerror = () => reject(allReq.error)
  })
  if (keys.length > MAX_RECORDS) {
    const overflow = keys.length - MAX_RECORDS
    for (let i = 0; i < overflow; i++) {
      store.delete(keys[i])
    }
  }

  await txDone(tx)
  db.close()
  return item
}

export async function updateGalleryImage(
  id: string,
  patch: Partial<Pick<GalleryImageRecord, 'public' | 'title' | 'prompt'>>
): Promise<GalleryImageRecord | null> {
  const existing = await getGalleryImage(id)
  if (!existing) return null
  const next = { ...existing, ...patch }
  await saveGalleryImage(next)
  return next
}

export async function deleteGalleryImage(id: string): Promise<void> {
  const db = await openDb()
  const tx = db.transaction(STORE, 'readwrite')
  tx.objectStore(STORE).delete(id)
  await txDone(tx)
  db.close()
}

export function truncatePrompt(prompt: string, max = 42): string {
  const text = (prompt || '').trim().replace(/\s+/g, ' ')
  if (text.length <= max) return text
  return `${text.slice(0, max)}…`
}

export function downloadDataUrl(dataUrl: string, filename: string) {
  const link = document.createElement('a')
  link.href = dataUrl
  link.download = filename
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
}
