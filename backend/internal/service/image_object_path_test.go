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

func TestAsyncImageResultObjectKeyUsesDayDirectoryAndStableDistinctNames(t *testing.T) {
	t.Parallel()
	when := time.Date(2026, 9, 7, 15, 30, 45, 0, time.FixedZone("CST", 8*60*60))

	first := AsyncImageResultObjectKey("images/", when, "task-1", 0, "image/png")
	second := AsyncImageResultObjectKey("images/", when, "task-1", 1, "image/png")
	retry := AsyncImageResultObjectKey("images/", when, "task-1", 0, "image/png")

	require.Regexp(t, `^images/results/2026/09/07/20260907073045[0-9a-f-]{36}\.png$`, first)
	require.Regexp(t, `^images/results/2026/09/07/20260907073045[0-9a-f-]{36}\.png$`, second)
	require.NotEqual(t, first, second)
	require.Equal(t, first, retry)
	require.NotContains(t, first, "/task-1/")
}
