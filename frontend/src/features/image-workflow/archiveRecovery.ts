export type PendingImageArchiveKind = 'file' | 'url' | 'task'

export interface PendingImageArchive {
  id: string
  userId: number
  kind: PendingImageArchiveKind
  title: string
  createdAt: number
  expiresAt: number
  errorMessage?: string
  file?: Blob
  fileName?: string
  remoteUrl?: string
  previewUrl?: string
  metadata?: Record<string, unknown>
  taskId?: string
  resultIndex?: number
}

const DB_NAME = 'sub2api-image-workflow'
const DB_VERSION = 2
const STORE_NAME = 'archive-recovery'
const SUBMISSION_STORE = 'plaza-submission-blobs'
const MAX_RECORDS = 20
const MAX_FILE_BYTES = 200 * 1024 * 1024
const RECOVERY_TTL_MS = 24 * 60 * 60 * 1000
const CHANGE_EVENT = 'sub2api:image-archive-recovery-changed'

const memoryFallback = new Map<string, PendingImageArchive>()
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
      if (!database.objectStoreNames.contains(STORE_NAME)) {
        const store = database.createObjectStore(STORE_NAME, { keyPath: 'id' })
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
      reject(request.error || new Error('Could not open the image archive recovery database'))
    }
    request.onblocked = () => {
      databasePromise = null
      reject(new Error('The image archive recovery database is blocked'))
    }
  })
  return databasePromise
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve()
    transaction.onerror = () => reject(transaction.error || new Error('Image archive recovery transaction failed'))
    transaction.onabort = () => reject(transaction.error || new Error('Image archive recovery transaction was aborted'))
  })
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result)
    request.onerror = () => reject(request.error || new Error('Image archive recovery request failed'))
  })
}

function notifyChanged(): void {
  if (typeof window !== 'undefined') window.dispatchEvent(new Event(CHANGE_EVENT))
}

function normalizeRecord(record: Omit<PendingImageArchive, 'createdAt' | 'expiresAt'> & Partial<Pick<PendingImageArchive, 'createdAt' | 'expiresAt'>>): PendingImageArchive {
  const createdAt = record.createdAt || Date.now()
  return {
    ...record,
    createdAt,
    expiresAt: record.expiresAt || createdAt + RECOVERY_TTL_MS,
  }
}

function overflowRecordIDs(records: PendingImageArchive[]): string[] {
  let keptRecords = 0
  let keptBytes = 0
  const overflow: string[] = []
  for (const record of [...records].sort((left, right) => right.createdAt - left.createdAt)) {
    const bytes = record.file?.size || 0
    if (keptRecords >= MAX_RECORDS || (bytes > 0 && keptBytes + bytes > MAX_FILE_BYTES)) {
      overflow.push(record.id)
      continue
    }
    keptRecords += 1
    keptBytes += bytes
  }
  return overflow
}

export async function savePendingImageArchive(
  record: Omit<PendingImageArchive, 'createdAt' | 'expiresAt'> & Partial<Pick<PendingImageArchive, 'createdAt' | 'expiresAt'>>,
): Promise<void> {
  const normalized = normalizeRecord(record)
  if (!hasIndexedDB()) {
    memoryFallback.set(normalized.id, normalized)
    overflowRecordIDs([...memoryFallback.values()]).forEach((id) => memoryFallback.delete(id))
    notifyChanged()
    return
  }

  const database = await openDatabase()
  const transaction = database.transaction(STORE_NAME, 'readwrite')
  const store = transaction.objectStore(STORE_NAME)
  store.put(normalized)
  const records = await requestResult(store.index('createdAt').getAll()) as PendingImageArchive[]
  overflowRecordIDs(records).forEach((id) => store.delete(id))
  await transactionDone(transaction)
  notifyChanged()
}

export async function listPendingImageArchives(userId: number): Promise<PendingImageArchive[]> {
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
  const transaction = database.transaction(STORE_NAME, 'readwrite')
  const store = transaction.objectStore(STORE_NAME)
  const records = await requestResult(store.getAll()) as PendingImageArchive[]
  records.filter((item) => item.expiresAt <= now).forEach((item) => store.delete(item.id))
  await transactionDone(transaction)
  return records
    .filter((item) => item.userId === userId && item.expiresAt > now)
    .sort((left, right) => right.createdAt - left.createdAt)
}

export async function removePendingImageArchive(id: string): Promise<void> {
  memoryFallback.delete(id)
  if (hasIndexedDB()) {
    const database = await openDatabase()
    const transaction = database.transaction(STORE_NAME, 'readwrite')
    transaction.objectStore(STORE_NAME).delete(id)
    await transactionDone(transaction)
  }
  notifyChanged()
}

export function onPendingImageArchivesChanged(callback: () => void): () => void {
  if (typeof window === 'undefined') return () => undefined
  window.addEventListener(CHANGE_EVENT, callback)
  return () => window.removeEventListener(CHANGE_EVENT, callback)
}
