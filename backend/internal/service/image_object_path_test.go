package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageObjectDatePartition(t *testing.T) {
	t.Parallel()
	got := ImageObjectDatePartition(time.Date(2026, 7, 22, 15, 4, 5, 0, time.UTC))
	require.Equal(t, "2026/07/22", got)
}
