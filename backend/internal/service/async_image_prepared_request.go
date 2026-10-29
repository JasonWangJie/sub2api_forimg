package service

import "context"

// AsyncImagePreparedRequestRepository stores only caller-encrypted local input
// snapshots. It must not advance the version owned by the invoking worker.
type AsyncImagePreparedRequestRepository interface {
	SaveAsyncImagePreparedRequest(ctx context.Context, taskID string, expectedVersion int64, encryptedPayload []byte) error
}

const AsyncImagePreparedRequestMaxBytes = 64 << 20
