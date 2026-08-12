export interface PlazaSubmissionBlob {
  id: string
  userId: number
  requestId?: string
  title: string
  file: Blob
  fileName?: string
  contentType: string
  byteSize: number
  checksumSha256: string
  previewUrl?: string
  metadata?: Record<string, unknown>
  createdAt: number
  expiresAt: number
}

const DB_NAME = 'sub2api-image-workflow'
const DB_VERSION = 2
const ARCHIVE_STORE = 'archive-recovery'
const SUBMISSION_STORE = 'plaza-submission-blobs'
const MAX_RECORDS = 40
const MAX_FILE_BYTES = 400 * 1024 * 1024
const SUBMISSION_TTL_MS = 90 * 24 * 60 * 60 * 1000
const CHANGE_EVENT = 'sub2api:plaza-submission-blob-changed'

const memoryFallback = new Map<string, PlazaSubmissionBlob>()
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
      if (!database.objectStoreNames.contains(ARCHIVE_STORE)) {
        const store = database.createObjectStore(ARCHIVE_STORE, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt', { unique: false })
        store.createIndex('userId', 'userId', { unique: false })
      }
      if (!database.objectStoreNames.contains(SUBMISSION_STORE)) {
        const store = database.createObjectStore(SUBMISSION_STORE, { keyPath: 'id' })
        store.createIndex('createdAt', 'createdAt', { unique: false })
        store.createIndex('userId', 'userId', { unique: false })
        store.createIndex('requestId', 'requestId', { unique: false })
      }
    }
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => {
      databasePromise = null
      reject(request.error || new Error('Could not open plaza submission blob database'))
    }
    request.onblocked = () => {
      databasePromise = null
      reject(new Error('The plaza submission blob database is blocked'))
    }
  })
  return databasePromise
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error || new Error('Plaza submission blob transaction failed'))
    transaction.onabort = () => reject(transaction.error || new Error('Plaza submission blob transaction was aborted'))
  })
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('Plaza submission blob request failed'))
  })
}

function notifyChanged(): void {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(CHANGE_EVENT))
}

function overflowRecordIDs(records: PlazaSubmissionBlob[]): string[] {
  let keptRecords = 0
  let keptBytes = 0
  const overflow: string[] = []
  for (const record of [...records].sort((left, right) => right.createdAt - left.createdAt)) {
    const bytes = record.byteSize || record.file?.size || 0
    if (keptRecords >= MAX_RECORDS || (bytes > 0 && keptBytes + bytes > MAX_FILE_BYTES)) {
      overflow.push(record.id)
      continue
    }
    keptRecords += 1
    keptBytes += bytes
  }
  return overflow
}

export async function sha256HexOfBlob(blob: Blob): Promise<string> {
  const buffer = await blob.arrayBuffer()
  const digest = await crypto.subtle.digest('SHA-256', buffer)
  return Array.from(new Uint8Array(digest))
    .map((byte) => byte.toString(16).padStart(2, '0'))
    .join('')
}

export async function savePlazaSubmissionBlob(
  record: Omit<PlazaSubmissionBlob, 'createdAt' | 'expiresAt' | 'byteSize' | 'checksumSha256' | 'contentType'>
    & Partial<Pick<PlazaSubmissionBlob, 'createdAt' | 'expiresAt' | 'byteSize' | 'checksumSha256' | 'contentType'>>,
): Promise<PlazaSubmissionBlob> {
  const contentType = record.contentType || record.file.type || 'application/octet-stream'
  const byteSize = record.byteSize ?? record.file.size
  const checksumSha256 = record.checksumSha256 || await sha256HexOfBlob(record.file)
  const createdAt = record.createdAt || Date.now()
  const normalized: PlazaSubmissionBlob = {
    ...record,
    contentType,
    byteSize,
    checksumSha256,
    createdAt,
    expiresAt: record.expiresAt || createdAt + SUBMISSION_TTL_MS,
  }
  if (!hasIndexedDB()) {
    memoryFallback.set(normalized.id, normalized)
    overflowRecordIDs([...memoryFallback.values()]).forEach((id) => memoryFallback.delete(id))
    notifyChanged()
    return normalized
  }
  const database = await openDatabase()
  const transaction = database.transaction(SUBMISSION_STORE, 'readwrite')
  const store = transaction.objectStore(SUBMISSION_STORE)
  store.put(normalized)
  const records = await requestResult(store.index('createdAt').getAll()) as PlazaSubmissionBlob[]
  overflowRecordIDs(records).forEach((id) => store.delete(id))
  await transactionDone(transaction)
  notifyChanged()
  return normalized
}

/** Bind requestId without rehashing or rewriting overflow scans of every image blob. */
export async function bindPlazaSubmissionRequestId(id: string, requestId: string): Promise<void> {
  if (!id || !requestId) return
  if (!hasIndexedDB()) {
    const item = memoryFallback.get(id)
    if (item) {
      memoryFallback.set(id, { ...item, requestId })
      notifyChanged()
    }
    return
  }
  const database = await openDatabase()
  const transaction = database.transaction(SUBMISSION_STORE, 'readwrite')
  const store = transaction.objectStore(SUBMISSION_STORE)
  const existing = await requestResult(store.get(id)) as PlazaSubmissionBlob | undefined
  if (existing) {
    existing.requestId = requestId
    store.put(existing)
  }
  await transactionDone(transaction)
  notifyChanged()
}

export async function getPlazaSubmissionBlob(id: string): Promise<PlazaSubmissionBlob | null> {
  if (!id) return null
  if (!hasIndexedDB()) {
    const item = memoryFallback.get(id)
    if (!item || item.expiresAt <= Date.now()) {
      memoryFallback.delete(id)
      return null
    }
    return item
  }
  const database = await openDatabase()
  const transaction = database.transaction(SUBMISSION_STORE, 'readonly')
  const item = await requestResult(transaction.objectStore(SUBMISSION_STORE).get(id)) as PlazaSubmissionBlob | undefined
  await transactionDone(transaction)
  if (!item || item.expiresAt <= Date.now()) return null
  return item
}

export async function listPlazaSubmissionBlobs(userId: number): Promise<PlazaSubmissionBlob[]> {
  if (userId <= 0) return []
  const now = Date.now()
  if (!hasIndexedDB()) {
    for (const [id, item] of memoryFallback) {
      if (item.expiresAt <= now) memoryFallback.delete(id)
    }
    return [...memoryFallback.values()]
      .filter((item) => item.userId === userId)
      .sort((left, right) => right.createdAt - left.createdAt)
  }
  const database = await openDatabase()
  const transaction = database.transaction(SUBMISSION_STORE, 'readwrite')
  const store = transaction.objectStore(SUBMISSION_STORE)
  const records = await requestResult(store.getAll()) as PlazaSubmissionBlob[]
  records.filter((item) => item.expiresAt <= now).forEach((item) => store.delete(item.id))
  await transactionDone(transaction)
  return records
    .filter((item) => item.userId === userId && item.expiresAt > now)
    .sort((left, right) => right.createdAt - left.createdAt)
}

export async function removePlazaSubmissionBlob(id: string): Promise<void> {
  memoryFallback.delete(id)
  if (hasIndexedDB()) {
    const database = await openDatabase()
    const transaction = database.transaction(SUBMISSION_STORE, 'readwrite')
    transaction.objectStore(SUBMISSION_STORE).delete(id)
    await transactionDone(transaction)
  }
  notifyChanged()
}

export function onPlazaSubmissionBlobsChanged(callback: () => void): () => void {
  if (typeof window === 'undefined') return () => undefined
  window.addEventListener(CHANGE_EVENT, callback)
  return () => window.removeEventListener(CHANGE_EVENT, callback)
}
