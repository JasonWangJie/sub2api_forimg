package service

import (
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestResolveAsyncImageBillingGroupID_MappingWins(t *testing.T) {
	primaryID := int64(10)
	key := &APIKey{
		GroupID: &primaryID,
		Group: &Group{
			ID: 10, Platform: PlatformGemini, Status: StatusActive,
			AllowImageGeneration: true, AllowAsyncImageGeneration: true,
		},
		ImagePlatformGroups: map[string]int64{
			PlatformOpenAI: 20,
			PlatformGemini: 11,
		},
	}
	gid, ok := ResolveAsyncImageBillingGroupID(key, PlatformOpenAI)
	require.True(t, ok)
	require.Equal(t, int64(20), gid)

	gid, ok = ResolveAsyncImageBillingGroupID(key, PlatformGemini)
	require.True(t, ok)
	require.Equal(t, int64(11), gid)
}

func TestResolveAsyncImageBillingGroupID_PrimaryFallback(t *testing.T) {
	primaryID := int64(10)
	key := &APIKey{
		GroupID: &primaryID,
		Group: &Group{
			ID: 10, Platform: PlatformOpenAI, Status: StatusActive,
			AllowImageGeneration: true, AllowAsyncImageGeneration: true,
		},
		ImagePlatformGroups: map[string]int64{},
	}
	gid, ok := ResolveAsyncImageBillingGroupID(key, PlatformOpenAI)
	require.True(t, ok)
	require.Equal(t, int64(10), gid)

	_, ok = ResolveAsyncImageBillingGroupID(key, PlatformGemini)
	require.False(t, ok)
}

func TestValidateAsyncImageBillingGroup(t *testing.T) {
	err := ValidateAsyncImageBillingGroup(nil, PlatformGemini)
	require.Error(t, err)
	var appErr *infraerrors.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, int32(http.StatusForbidden), appErr.Code)

	group := &Group{
		ID: 1, Platform: PlatformGemini, Status: StatusActive,
		AllowImageGeneration: false, AllowAsyncImageGeneration: true,
	}
	err = ValidateAsyncImageBillingGroup(group, PlatformGemini)
	require.Error(t, err)

	group.AllowImageGeneration = true
	group.AllowAsyncImageGeneration = false
	err = ValidateAsyncImageBillingGroup(group, PlatformGemini)
	require.Error(t, err)

	group.AllowAsyncImageGeneration = true
	group.Platform = PlatformOpenAI
	err = ValidateAsyncImageBillingGroup(group, PlatformGemini)
	require.Error(t, err)

	group.Platform = PlatformGemini
	require.NoError(t, ValidateAsyncImageBillingGroup(group, PlatformGemini))
}

func TestNormalizeImagePlatformGroups(t *testing.T) {
	out, err := NormalizeImagePlatformGroups(map[string]int64{
		"Gemini": 1,
		"openai": 2,
		"grok":   3,
	})
	require.Error(t, err)
	require.Nil(t, out)

	out, err = NormalizeImagePlatformGroups(map[string]int64{
		"Gemini": 1,
		"openai": 0,
	})
	require.NoError(t, err)
	require.Equal(t, map[string]int64{PlatformGemini: 1}, out)
}
