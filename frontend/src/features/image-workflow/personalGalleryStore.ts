/**
 * Browser-local personal gallery for realtime image workbench results.
 * Retention per user: 100 records, 30 days, and 200 MiB.
 */

export interface PersonalGalleryRecord {
  id: string
  userId: number
  title: string
  file: Blob
  fileName?: string
  contentType: string
  byteSize: number
  checksumSha256: string
  platform?: string
  model?: string
  prompt?: string
  requestedSize?: string
  aspectRatio?: string
  quality?: string
  outputFormat?: string
  apiKeyId?: number
  groupId?: number | null
  requestId?: string
  metadata?: Record<string, unknown>
  createdAt: number
  expiresAt: number
}

export const PERSONAL_GALLERY_MAX_RECORDS = 100
export const PERSONAL_GALLERY_TTL_MS = 30 * 24 * 60 * 60 * 1000
export const PERSONAL_GALLERY_MAX_BYTES = 200 * 1024 * 1024
export const PERSONAL_GALLERY_CHANGE_EVENT = 'sub2api:personal-gallery-changed'

const DB_NAME = 'sub2api-personal-gallery'
const DB_VERSION = 2
const STORE = 'items'

const memoryFallback = new Map<string, PersonalGalleryRecord>()
let databasePromise: Promise<IDBDatabase> | null = null

function hasIndexedDB(): boolean {
  return typeof window !== 'undefined' && 'indexedDB' in window
}

function openDatabase(): Promise<IDBDatabase> {
  if (databasePromise) return databasePromise
  databasePromise = new Promise((resolve, reject) => {
    const request = window.indexedDB.open(DB_NAME, DB_VERSION)
    request.onupgradeneeded = () => {
      const database = request.result
      let store: IDBObjectStore
      if (!database.objectStoreNames.contains(STORE)) {
        store = database.createObjectStore(STORE, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt', { unique: false })
        store.createIndex('userId', 'userId', { unique: false })
        store.createIndex('expiresAt', 'expiresAt', { unique: false })
      } else {
        store = request.transaction!.objectStore(STORE)
        if (!store.indexNames.contains('createdAt')) store.createIndex('createdAt', 'createdAt', { unique: false })
        if (!store.indexNames.contains('userId')) store.createIndex('userId', 'userId', { unique: false })
        if (!store.indexNames.contains('expiresAt')) store.createIndex('expiresAt', 'expiresAt', { unique: false })
      }
      const cursorRequest = store.openCursor()
      cursorRequest.onsuccess = () => {
        const cursor = cursorRequest.result
        if (!cursor) return
        const value = cursor.value as PersonalGalleryRecord & { previewUrl?: string }
        if ('previewUrl' in value) {
          delete value.previewUrl
          cursor.update(value)
        }
        cursor.continue()
      }
    }
    request.onsuccess = () => {
      request.result.onversionchange = () => {
        request.result.close()
        databasePromise = null
      }
      resolve(request.result)
    }
    request.onerror = () => {
      databasePromise = null
      reject(request.error || new Error('Could not open personal gallery database'))
    }
    request.onblocked = () => {
      databasePromise = null
      reject(new Error('The personal gallery database is blocked'))
    }
  })
  return databasePromise
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error || new Error('Personal gallery transaction failed'))
    transaction.onabort = () => reject(transaction.error || new Error('Personal gallery transaction was aborted'))
  })
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('Personal gallery request failed'))
  })
}

function notifyChanged(): void {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(PERSONAL_GALLERY_CHANGE_EVENT))
}

/** Pure cleanup helper, exported for unit tests. */
export function selectPersonalGalleryOverflowIDs(
  records: Array<Pick<PersonalGalleryRecord, 'id' | 'userId' | 'createdAt' | 'expiresAt' | 'byteSize'>>,
  userId: number,
  now = Date.now(),
  maxRecords = PERSONAL_GALLERY_MAX_RECORDS,
  maxBytes = PERSONAL_GALLERY_MAX_BYTES,
): string[] {
  const owned = records.filter((item) => item.userId === userId)
  const expired = owned.filter((item) => item.expiresAt <= now).map((item) => item.id)
  const expiredSet = new Set(expired)
  const alive = owned
    .filter((item) => !expiredSet.has(item.id))
    .sort((left, right) => right.createdAt - left.createdAt)
  const overflow: string[] = []
  let retainedCount = alive.length
  let retainedBytes = alive.reduce((total, item) => total + Math.max(0, item.byteSize), 0)
  for (let index = alive.length - 1; index >= 0; index -= 1) {
    if (retainedCount <= maxRecords && retainedBytes <= maxBytes) break
    const item = alive[index]
    if (!item) continue
    overflow.push(item.id)
    retainedCount -= 1
    retainedBytes -= Math.max(0, item.byteSize)
  }
  return [...expired, ...overflow]
}

export async function sha256HexOfBlob(blob: Blob): Promise<string> {
  const buffer = await blob.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buffer)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

function applyCleanup(
  store: IDBObjectStore | Map<string, PersonalGalleryRecord>,
  records: PersonalGalleryRecord[],
  userId: number,
  now = Date.now(),
): void {
  for (const id of selectPersonalGalleryOverflowIDs(records, userId, now)) {
    if (store instanceof Map) store.delete(id)
    else store.delete(id)
  }
}

export async function savePersonalGalleryItem(
  record: Omit<PersonalGalleryRecord, 'createdAt' | 'expiresAt' | 'byteSize' | 'checksumSha256' | 'contentType'>
    & Partial<Pick<PersonalGalleryRecord, 'createdAt' | 'expiresAt' | 'byteSize' | 'checksumSha256' | 'contentType'>>,
): Promise<PersonalGalleryRecord> {
  const contentType = record.contentType || record.file.type || 'application/octet-stream'
  const byteSize = record.file.size
  const checksumSha256 = record.checksumSha256 || await sha256HexOfBlob(record.file)
  const createdAt = record.createdAt || Date.now()
  const normalized: PersonalGalleryRecord = {
    ...record,
    contentType,
    byteSize,
    checksumSha256,
    createdAt,
    expiresAt: record.expiresAt || createdAt + PERSONAL_GALLERY_TTL_MS,
  }
  delete (normalized as PersonalGalleryRecord & { previewUrl?: string }).previewUrl

  if (!hasIndexedDB()) {
    memoryFallback.set(normalized.id, normalized)
    applyCleanup(memoryFallback, [...memoryFallback.values()], normalized.userId)
    notifyChanged()
    return normalized
  }

  const database = await openDatabase()
  const transaction = database.transaction(STORE, 'readwrite')
  const store = transaction.objectStore(STORE)
  store.put(normalized)
  const records = await readPersonalGalleryRecordsForUser(store, normalized.userId)
  applyCleanup(store, records, normalized.userId)
  await transactionDone(transaction)
  notifyChanged()
  return normalized
}

export async function updatePersonalGalleryItem(
  id: string,
  patch: Partial<Pick<PersonalGalleryRecord, 'title' | 'prompt' | 'requestId'>>,
): Promise<PersonalGalleryRecord | null> {
  if (!id) return null
  if (!hasIndexedDB()) {
    const existing = memoryFallback.get(id)
    if (!existing) return null
    const next = { ...existing, ...patch }
    delete (next as PersonalGalleryRecord & { previewUrl?: string }).previewUrl
    memoryFallback.set(id, next)
    notifyChanged()
    return next
  }
  const database = await openDatabase()
  const transaction = database.transaction(STORE, 'readwrite')
  const store = transaction.objectStore(STORE)
  const existing = await requestResult(store.get(id)) as PersonalGalleryRecord | undefined
  if (!existing) {
    await transactionDone(transaction)
    return null
  }
  const next = { ...existing, ...patch }
  delete (next as PersonalGalleryRecord & { previewUrl?: string }).previewUrl
  store.put(next)
  await transactionDone(transaction)
  notifyChanged()
  return next
}

export async function bindPersonalGalleryRequestId(id: string, requestId: string): Promise<void> {
  if (!id || !requestId) return
  await updatePersonalGalleryItem(id, { requestId })
}

export async function getPersonalGalleryItem(id: string): Promise<PersonalGalleryRecord | null> {
  if (!id) return null
  const now = Date.now()
  if (!hasIndexedDB()) {
    const item = memoryFallback.get(id)
    if (!item || item.expiresAt <= now) {
      memoryFallback.delete(id)
      return null
    }
    return item
  }
  const database = await openDatabase()
  const transaction = database.transaction(STORE, 'readonly')
  const item = await requestResult(transaction.objectStore(STORE).get(id)) as PersonalGalleryRecord | undefined
  await transactionDone(transaction)
  if (!item || item.expiresAt <= now) return null
  return item
}

export async function listPersonalGalleryItems(userId: number): Promise<PersonalGalleryRecord[]> {
  if (userId <= 0) return []
  const now = Date.now()
  if (!hasIndexedDB()) {
    const before = [...memoryFallback.values()].filter((item) => item.userId === userId).length
    applyCleanup(memoryFallback, [...memoryFallback.values()], userId, now)
    const after = [...memoryFallback.values()].filter((item) => item.userId === userId).length
    if (after !== before) notifyChanged()
    return [...memoryFallback.values()]
      .filter((item) => item.userId === userId && item.expiresAt > now)
      .sort((left, right) => right.createdAt - left.createdAt)
  }
  const database = await openDatabase()
  const transaction = database.transaction(STORE, 'readwrite')
  const store = transaction.objectStore(STORE)
  const records = await readPersonalGalleryRecordsForUser(store, userId)
  const overflow = selectPersonalGalleryOverflowIDs(records, userId, now)
  overflow.forEach((id) => store.delete(id))
  await transactionDone(transaction)
  if (overflow.length) notifyChanged()
  const expiredSet = new Set(overflow)
  return records
    .filter((item) => item.userId === userId && item.expiresAt > now && !expiredSet.has(item.id))
    .sort((left, right) => right.createdAt - left.createdAt)
}

function readPersonalGalleryRecordsForUser(store: IDBObjectStore, userId: number): Promise<PersonalGalleryRecord[]> {
  return new Promise((resolve, reject) => {
    const records: PersonalGalleryRecord[] = []
    const request = store.index('userId').openCursor(IDBKeyRange.only(userId))
    request.onsuccess = () => {
      const cursor = request.result
      if (!cursor) {
        resolve(records)
        return
      }
      records.push(cursor.value as PersonalGalleryRecord)
      cursor.continue()
    }
    request.onerror = () => reject(request.error || new Error('Could not scan personal gallery records'))
  })
}

export async function removePersonalGalleryItem(id: string): Promise<void> {
  memoryFallback.delete(id)
  if (hasIndexedDB()) {
    const database = await openDatabase()
    const transaction = database.transaction(STORE, 'readwrite')
    transaction.objectStore(STORE).delete(id)
    await transactionDone(transaction)
  }
  notifyChanged()
}

export function onPersonalGalleryChanged(callback: () => void): () => void {
  if (typeof window === 'undefined') return () => undefined
  window.addEventListener(PERSONAL_GALLERY_CHANGE_EVENT, callback)
  return () => window.removeEventListener(PERSONAL_GALLERY_CHANGE_EVENT, callback)
}

/** Reset in-memory fallback (tests only). */
export function __resetPersonalGalleryMemoryForTests(): void {
  memoryFallback.clear()
}
