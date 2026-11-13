//go:build unit

package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type asyncResolutionGuardStub struct {
	observations []service.ImageResolutionObservation
}

func (g *asyncResolutionGuardStub) Observe(_ context.Context, o service.ImageResolutionObservation) error {
	g.observations = append(g.observations, o)
	return nil
}
func (*asyncResolutionGuardStub) IsCooling(context.Context, int64, string, string) (bool, error) {
	return false, nil
}

func TestAsyncImageResolutionExpectationSurvivesResultRecoverySnapshot(t *testing.T) {
	expectation := service.ImageResolutionExpectation{RequestedModel: "client-alias", UpstreamModel: "gpt-image-2", SizeTier: "4K", ExpectedWidth: 2880, ExpectedHeight: 2880, ExpectedPixels: 2880 * 2880, RuleVersion: 1}
	capture := &AsyncImageUsageCapture{}
	capture.setOpenAI(&service.OpenAIRecordUsageInput{Account: &service.Account{ID: 4, Name: "quality-channel", Platform: service.PlatformOpenAI}, Result: &service.OpenAIForwardResult{ImageResolutionExpectation: &expectation}})
	task := &service.AsyncImageTask{TaskID: "task-quality", Platform: service.PlatformOpenAI, ImageCount: 1, SubmittedAt: time.Now().UTC()}
	envelope, err := buildAsyncImageResultEnvelope(task, []byte(`{"data":[{"url":"https://generated.example/image.png"}]}`), capture, nil)
	require.NoError(t, err)
	encoded, err := json.Marshal(envelope)
	require.NoError(t, err)
	var recovered asyncImageResultEnvelope
	require.NoError(t, json.Unmarshal(encoded, &recovered))
	require.Equal(t, &expectation, recovered.OpenAI.Result.ImageResolutionExpectation)
	// Finalization has no request body: only the stored immutable target and
	// validated final dimensions are needed, so this cannot regenerate an image.
	recoveredCapture := &AsyncImageUsageCapture{}
	recoveredCapture.setOpenAI(recovered.OpenAI)
	g := &asyncResolutionGuardStub{}
	h := &DurableAsyncImageHandler{}
	h.SetImageResolutionGuard(g)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requestID := "upstream-original"
	for i := 0; i < 2; i++ {
		h.observeAsyncImageResolution(ctx, task, recoveredCapture, []asyncImageCapturedOutput{{Width: 1024, Height: 1024}}, 4, &requestID)
	}
	require.Len(t, g.observations, 2)
	require.Equal(t, g.observations[0].ID, g.observations[1].ID)
	require.Equal(t, "async-image:task-quality", g.observations[0].ID)
	require.Equal(t, &expectation, &g.observations[0].Expectation)
	require.Equal(t, []service.ImageResolutionOutput{{Width: 1024, Height: 1024}}, g.observations[0].Outputs)
	require.Equal(t, "quality-channel", g.observations[0].AccountName)
}

func TestAsyncImageResolutionOldSnapshotDoesNotInventTarget(t *testing.T) {
	g := &asyncResolutionGuardStub{}
	h := &DurableAsyncImageHandler{}
	h.SetImageResolutionGuard(g)
	capture := &AsyncImageUsageCapture{}
	capture.setOpenAI(&service.OpenAIRecordUsageInput{Result: &service.OpenAIForwardResult{ImageSize: "4K"}})
	h.observeAsyncImageResolution(context.Background(), &service.AsyncImageTask{TaskID: "old"}, capture, []asyncImageCapturedOutput{{Width: 1024, Height: 1024}}, 4, nil)
	require.Empty(t, g.observations)
}
