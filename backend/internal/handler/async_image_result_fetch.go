package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const asyncImageResultFetchMaxAttempts = 3 // initial GET plus two retries
const asyncImageResultMaxImages = 16
const asyncImageResultMaxBytes int64 = 128 << 20

var errAsyncImageResultLimit = errors.New("generated image result exceeds recovery resource limits")

type asyncImageResolvedOutputsKey struct{}
type asyncImageResultFinalizeAttemptKey struct{}
type asyncImageResultSource struct {
	URL  string
	Data []byte
	MIME string
}
type asyncImageResultEnvelope struct {
	Version               int
	Images                []asyncImageResultSource
	Gemini                *service.RecordUsageInput
	OpenAI                *service.OpenAIRecordUsageInput
	HasAPIKeyQuotaUpdater bool
}

// Only billing attributes survive. Never copy account credentials, API keys,
// password hashes, TOTP secrets, or proxy authentication into the envelope.
func asyncImageBillingAccount(a *service.Account) *service.Account {
	if a == nil {
		return nil
	}
	return &service.Account{ID: a.ID, Platform: a.Platform, Type: a.Type, RateMultiplier: a.RateMultiplier, ParentAccountID: a.ParentAccountID, QuotaDimension: a.QuotaDimension, Extra: map[string]any{
		"quota_limit": a.GetQuotaLimit(), "quota_daily_limit": a.GetQuotaDailyLimit(), "quota_weekly_limit": a.GetQuotaWeeklyLimit(),
		"openai_long_context_billing_enabled": a.IsOpenAILongContextBillingEnabled(),
	}}
}
func asyncImageBillingGroup(g *service.Group) *service.Group {
	if g == nil {
		return nil
	}
	c := *g
	c.Name, c.Description, c.Section, c.DuplicateOperationID = "", "", "", ""
	c.AccountGroups = nil // nested accounts can contain credentials
	return &c
}
func asyncImageBillingSubscription(s *service.UserSubscription) *service.UserSubscription {
	if s == nil {
		return nil
	}
	c := *s
	c.User = asyncImageBillingUser(s.User)
	c.Group = asyncImageBillingGroup(s.Group)
	c.AssignedByUser = nil
	c.Notes = ""
	return &c
}
func asyncImageBillingUser(u *service.User) *service.User {
	if u == nil {
		return nil
	}
	return &service.User{ID: u.ID, GroupRates: u.GroupRates, Balance: u.Balance}
}
func asyncImageBillingKey(k *service.APIKey) *service.APIKey {
	if k == nil {
		return nil
	}
	c := *k
	c.Key = ""
	c.Name = ""
	c.User = asyncImageBillingUser(k.User)
	c.Group = asyncImageBillingGroup(k.Group)
	c.IPWhitelist = nil
	c.IPBlacklist = nil
	c.CompiledIPWhitelist = nil
	c.CompiledIPBlacklist = nil
	c.LastUsedIP = nil
	return &c
}

func asyncImageResultImageLimit(task *service.AsyncImageTask) int {
	if task.Platform == service.PlatformOpenAI && task.ImageCount > 0 {
		return min(task.ImageCount, asyncImageResultMaxImages)
	}
	return asyncImageResultMaxImages
}
func asyncImageResultByteLimit(cfg service.AsyncImageRuntimeConfig) int64 {
	if cfg.DownloadMaxBytes > 0 {
		return min(cfg.DownloadMaxBytes, 64<<20)
	}
	return 32 << 20
}
func validateAsyncImageResultEnvelope(envelope *asyncImageResultEnvelope, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) error {
	if len(envelope.Images) > asyncImageResultImageLimit(task) {
		return errAsyncImageResultLimit
	}
	var total int64
	for _, im := range envelope.Images {
		n := int64(len(im.Data))
		if strings.HasPrefix(im.URL, "data:") {
			n += int64(base64.StdEncoding.DecodedLen(len(im.URL)))
		} else if len(im.URL) > 64<<10 {
			return errAsyncImageResultLimit
		}
		if n > asyncImageResultByteLimit(cfg) {
			return errAsyncImageResultLimit
		}
		total += n
		if total > asyncImageResultMaxBytes {
			return errAsyncImageResultLimit
		}
	}
	return nil
}
func buildAsyncImageResultEnvelope(task *service.AsyncImageTask, body []byte, usage *AsyncImageUsageCapture, gemini *service.GeminiImageResponseCapture, configs ...service.AsyncImageRuntimeConfig) (*asyncImageResultEnvelope, error) {
	var cfg service.AsyncImageRuntimeConfig
	if len(configs) > 0 {
		cfg = configs[0]
	}
	e := &asyncImageResultEnvelope{Version: 1}
	if task.Platform == service.PlatformGemini {
		if gemini == nil {
			return nil, errors.New("missing Gemini image capture")
		}
		if err := gemini.Error(); err != nil {
			return nil, err
		}
		images := gemini.Images()
		if len(images) > asyncImageResultImageLimit(task) {
			return nil, errAsyncImageResultLimit
		}
		for _, im := range images {
			e.Images = append(e.Images, asyncImageResultSource{URL: im.SourceURL, Data: im.Data, MIME: im.MIMEType})
		}
		if u := usage.Gemini(); u != nil {
			c := *u
			c.APIKey = asyncImageBillingKey(u.APIKey)
			c.User = asyncImageBillingUser(u.User)
			c.Account = asyncImageBillingAccount(u.Account)
			c.Subscription = asyncImageBillingSubscription(u.Subscription)
			e.HasAPIKeyQuotaUpdater = u.APIKeyService != nil
			c.APIKeyService = nil
			if c.PricingAt.IsZero() {
				c.PricingAt = time.Now().UTC()
			}
			e.Gemini = &c
		}
	} else {
		var v struct {
			Data []struct {
				URL string `json:"url"`
				B64 string `json:"b64_json"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, err
		}
		if len(v.Data) > asyncImageResultImageLimit(task) {
			return nil, errAsyncImageResultLimit
		}
		var decodedBytes int64
		for _, im := range v.Data {
			if strings.TrimSpace(im.B64) != "" {
				n := int64(base64.StdEncoding.DecodedLen(len(strings.TrimSpace(im.B64))))
				decodedBytes += n
				if n > asyncImageResultByteLimit(cfg) || decodedBytes > asyncImageResultMaxBytes {
					return nil, errAsyncImageResultLimit
				}
				b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(im.B64))
				if err != nil {
					return nil, err
				}
				e.Images = append(e.Images, asyncImageResultSource{Data: b})
			} else if strings.TrimSpace(im.URL) != "" {
				e.Images = append(e.Images, asyncImageResultSource{URL: strings.TrimSpace(im.URL)})
			}
		}
		if u := usage.OpenAI(); u != nil {
			c := *u
			c.APIKey = asyncImageBillingKey(u.APIKey)
			c.User = asyncImageBillingUser(u.User)
			c.Account = asyncImageBillingAccount(u.Account)
			c.Subscription = asyncImageBillingSubscription(u.Subscription)
			e.HasAPIKeyQuotaUpdater = u.APIKeyService != nil
			c.APIKeyService = nil
			if u.Result != nil {
				result := *u.Result
				result.ResponseHeaders = nil
				c.Result = &result
			}
			if c.PricingAt.IsZero() {
				c.PricingAt = time.Now().UTC()
			}
			e.OpenAI = &c
		}
	}
	if len(e.Images) == 0 {
		return nil, errors.New("upstream response did not contain an image")
	}
	if err := validateAsyncImageResultEnvelope(e, task, cfg); err != nil {
		return nil, err
	}
	return e, nil
}

func (h *DurableAsyncImageHandler) beginAsyncImageResultFetch(parent context.Context, task *service.AsyncImageTask, body []byte, usage *AsyncImageUsageCapture, gemini *service.GeminiImageResponseCapture, cfg service.AsyncImageRuntimeConfig) (asyncImageWorkerDisposition, bool) {
	repo, ok := h.tasks.Repository().(service.AsyncImageResultFetchRepository)
	if !ok || h.encryptor == nil {
		return asyncImageWorkerDisposition{}, false
	}
	envelope, err := buildAsyncImageResultEnvelope(task, body, usage, gemini, cfg)
	if err != nil {
		if errors.Is(err, errAsyncImageResultLimit) {
			h.failAsyncImageTask(parent, task, "result_resource_limit", err.Error(), false)
			return asyncImageWorkerDisposition{}, true
		}
		return asyncImageWorkerDisposition{}, false
	} // existing deterministic-output handling
	remote := false
	for _, im := range envelope.Images {
		if im.URL != "" {
			remote = true
		}
	}
	if !remote {
		return asyncImageWorkerDisposition{}, false
	}
	var account *service.Account
	if envelope.Gemini != nil {
		account = envelope.Gemini.Account
	}
	if envelope.OpenAI != nil {
		account = envelope.OpenAI.Account
	}
	if account == nil {
		return asyncImageWorkerDisposition{}, false
	}
	plain, err := json.Marshal(envelope)
	if err != nil {
		h.markAsyncImageExecutionUnknown(parent, task, "result recovery snapshot could not be encoded")
		return asyncImageWorkerDisposition{}, true
	}
	ciphertext, err := h.encryptor.Encrypt(string(plain))
	if err != nil {
		h.markAsyncImageExecutionUnknown(parent, task, "result recovery snapshot could not be encrypted")
		return asyncImageWorkerDisposition{}, true
	}
	attempts, ids := asyncImageAttemptState(task, parent)
	now := time.Now().UTC()
	progress, count := 60, len(envelope.Images)
	billingID := "client:async-image:" + task.TaskID
	upstreamID := ""
	if envelope.Gemini != nil && envelope.Gemini.Result != nil {
		upstreamID = envelope.Gemini.Result.RequestID
	}
	if envelope.OpenAI != nil && envelope.OpenAI.Result != nil {
		upstreamID = envelope.OpenAI.Result.RequestID
		if upstreamID == "" {
			upstreamID = envelope.OpenAI.Result.ResponseID
		}
	}
	tr := service.AsyncImageTaskTransition{TaskID: task.TaskID, ExpectedVersion: task.Version, FromStatuses: []string{service.AsyncImageTaskStatusInvoking}, ToStatus: service.AsyncImageTaskStatusUpstreamSucceeded, AccountID: &account.ID, AccountAttempts: attempts, AttemptedAccountIDs: ids, UpstreamSucceededAt: &now, Progress: &progress, ImageCount: &count, BillingRequestID: &billingID, ClearRequestPayload: true}
	if upstreamID != "" {
		tr.UpstreamRequestID = &upstreamID
	}
	persistCtx, cancel := asyncImageTerminalTransitionContext(parent)
	defer cancel()
	var saved *service.AsyncImageTask
	for i := 0; i < 3; i++ {
		saved, err = repo.SaveAsyncImageResultFetch(persistCtx, tr, []byte(ciphertext), now.Add(24*time.Hour))
		if err == nil {
			break
		}
		// An ambiguous COMMIT must be reconciled before retrying the write.
		current, getErr := h.tasks.Repository().GetAsyncImageTaskByTaskID(persistCtx, task.TaskID)
		if getErr == nil && current != nil && current.Status == service.AsyncImageTaskStatusUpstreamSucceeded {
			pending, pendingErr := repo.GetAsyncImageResultFetch(persistCtx, task.TaskID)
			if pendingErr == nil && pending != nil {
				saved = current
				err = nil
				break
			}
		}
		if errors.Is(err, service.ErrAsyncImageInvalidTransition) || !waitAsyncImageResultPersist(persistCtx, i) {
			break
		}
	}
	if err != nil {
		h.markAsyncImageExecutionUnknown(parent, task, "result recovery snapshot could not be persisted")
		return asyncImageWorkerDisposition{}, true
	}
	result, _ := h.resumeAsyncImageResultFetch(parent, saved, cfg)
	return result, true
}

func waitAsyncImageResultPersist(ctx context.Context, attempt int) bool {
	timer := time.NewTimer(time.Duration(attempt+1) * 100 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func asyncImageResultFetchRetryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var e *service.AsyncImageReferenceDownloadError
	if !errors.As(err, &e) {
		return false
	}
	if e.StatusCode > 0 {
		return e.StatusCode == http.StatusRequestTimeout || e.StatusCode == http.StatusTooEarly || e.StatusCode == http.StatusTooManyRequests || (e.StatusCode >= 500 && e.StatusCode < 600)
	}
	return e.Phase == "download" || e.Phase == "read"
}

func asyncImageResultFetchDelay(err error) time.Duration {
	delay := time.Duration(5+rand.IntN(6)) * time.Second
	var e *service.AsyncImageReferenceDownloadError
	if errors.As(err, &e) && e.RetryAfter > delay {
		delay = e.RetryAfter
	}
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	return delay
}

func (h *DurableAsyncImageHandler) resultFetchEvent(ctx context.Context, taskID, event string, index, attempt int, message string) {
	payload, _ := json.Marshal(map[string]any{"stage": "result_fetch", "image_index": index, "attempt": attempt, "message": message})
	_ = h.tasks.Repository().AppendAsyncImageEvent(ctx, service.AsyncImageEvent{TaskID: taskID, EventType: event, Payload: payload})
}

func (h *DurableAsyncImageHandler) resumeAsyncImageResultFetch(parent context.Context, task *service.AsyncImageTask, cfg service.AsyncImageRuntimeConfig) (asyncImageWorkerDisposition, bool) {
	repo, ok := h.tasks.Repository().(service.AsyncImageResultFetchRepository)
	if !ok {
		return asyncImageWorkerDisposition{}, false
	}
	pending, err := repo.GetAsyncImageResultFetch(parent, task.TaskID)
	if err != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
	}
	if pending == nil {
		return asyncImageWorkerDisposition{}, false
	}
	if !pending.ExpiresAt.After(time.Now()) {
		h.failAsyncImageTask(parent, task, "result_fetch_expired", "stored result retrieval expired; do not regenerate automatically", false)
		return asyncImageWorkerDisposition{}, true
	}
	if h.encryptor == nil {
		h.failAsyncImageTask(parent, task, "result_fetch_decryption_failed", "result recovery key is unavailable", false)
		return asyncImageWorkerDisposition{}, true
	}
	plain, err := h.encryptor.Decrypt(string(pending.EncryptedPayload))
	var envelope asyncImageResultEnvelope
	if err != nil || json.Unmarshal([]byte(plain), &envelope) != nil || envelope.Version != 1 || len(envelope.Images) == 0 {
		h.failAsyncImageTask(parent, task, "result_fetch_decryption_failed", "stored result recovery snapshot is invalid", false)
		return asyncImageWorkerDisposition{}, true
	}
	if err := validateAsyncImageResultEnvelope(&envelope, task, cfg); err != nil {
		h.failAsyncImageTask(parent, task, "result_resource_limit", err.Error(), false)
		return asyncImageWorkerDisposition{}, true
	}
	existing, err := h.tasks.Repository().ListAsyncImageStagingObjects(parent, task.TaskID)
	if err != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
	}
	objects := make(map[int]service.AsyncImageStagingObject, len(existing))
	for _, o := range existing {
		objects[o.ImageIndex] = o
	}
	outputs := make([]asyncImageCapturedOutput, len(envelope.Images))
	// Generation's deadline is intentionally not reused for fetching a completed response.
	budget := time.Duration(max(cfg.DownloadTimeoutSeconds, 1)*len(envelope.Images)+15) * time.Second
	if budget > 10*time.Minute {
		budget = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	var totalBytes int64
	for index, im := range envelope.Images {
		if o, found := objects[index]; found {
			totalBytes += int64(len(o.Content))
			if int64(len(o.Content)) > asyncImageResultByteLimit(cfg) || totalBytes > asyncImageResultMaxBytes {
				h.failAsyncImageTask(parent, task, "result_resource_limit", errAsyncImageResultLimit.Error(), false)
				return asyncImageWorkerDisposition{}, true
			}
			w, height := 0, 0
			if o.Width != nil {
				w = *o.Width
			}
			if o.Height != nil {
				height = *o.Height
			}
			outputs[index] = asyncImageCapturedOutput{Data: o.Content, ContentType: o.ContentType, Checksum: o.Checksum, Width: w, Height: height}
			continue
		}
		if ctx.Err() != nil {
			return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
		}
		attempt := 0
		downloadCfg := cfg
		downloadCfg.DownloadMaxBytes = min(asyncImageResultByteLimit(cfg), asyncImageResultMaxBytes-totalBytes)
		if downloadCfg.DownloadMaxBytes <= 0 {
			h.failAsyncImageTask(parent, task, "result_resource_limit", errAsyncImageResultLimit.Error(), false)
			return asyncImageWorkerDisposition{}, true
		}
		if im.URL != "" {
			if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(im.URL)), "data:") {
				if pending.Attempts[index] >= asyncImageResultFetchMaxAttempts {
					h.failAsyncImageTask(parent, task, "result_fetch_exhausted", "result download retry budget exhausted; do not regenerate automatically", false)
					return asyncImageWorkerDisposition{}, true
				}
				attempt, err = repo.BeginAsyncImageResultFetchAttempt(ctx, task.TaskID, index, asyncImageResultFetchMaxAttempts)
				if err != nil {
					return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
				}
				h.resultFetchEvent(ctx, task.TaskID, "result_fetch_attempt", index, attempt, "")
			}
			outputs[index], err = downloadGeneratedAsyncImage(ctx, im.URL, downloadCfg)
		} else {
			outputs[index], err = validateGeneratedAsyncImage(im.Data, im.MIME, downloadCfg)
		}
		if err != nil {
			message := asyncImageSafeError(err)
			if parent.Err() != nil {
				return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
			}
			if asyncImageResultFetchRetryable(err) && attempt < asyncImageResultFetchMaxAttempts {
				h.resultFetchEvent(parent, task.TaskID, "result_fetch_retry", index, attempt, message)
				return asyncImageWorkerDisposition{requeue: true, delay: asyncImageResultFetchDelay(err)}, true
			}
			h.resultFetchEvent(parent, task.TaskID, "result_fetch_failed", index, attempt, message)
			h.failAsyncImageTask(parent, task, "result_fetch_failed", message, false)
			return asyncImageWorkerDisposition{}, true
		}
		out := outputs[index]
		totalBytes += int64(len(out.Data))
		w, height := out.Width, out.Height
		if err = repo.SaveAsyncImageFetchedObject(ctx, service.AsyncImageStagingObject{TaskID: task.TaskID, ImageIndex: index, Content: out.Data, ContentType: out.ContentType, ByteSize: int64(len(out.Data)), Checksum: out.Checksum, Width: &w, Height: &height, ExpiresAt: pending.ExpiresAt}); err != nil {
			return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
		}
	}
	capture := &AsyncImageUsageCapture{}
	// The serialized snapshot omits this runtime service, but command
	// preparation still needs the original quota-update flag.
	if envelope.HasAPIKeyQuotaUpdater && h.apiKeys != nil {
		if envelope.Gemini != nil {
			envelope.Gemini.APIKeyService = h.apiKeys
		}
		if envelope.OpenAI != nil {
			envelope.OpenAI.APIKeyService = h.apiKeys
		}
	}
	capture.setGemini(envelope.Gemini)
	capture.setOpenAI(envelope.OpenAI)
	if parent.Err() != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
	}
	if pending.Attempts[-1] >= 3 {
		h.failAsyncImageTask(parent, task, "result_billing_prepare_failed", "result finalization retry budget exhausted; images were not regenerated", false)
		return asyncImageWorkerDisposition{}, true
	}
	// Pricing may perform repository reads. Give it a fresh bounded budget,
	// independent from downloads; persist its own attempts under reserved -1.
	finalCtx, finalCancel := context.WithTimeout(parent, 15*time.Second)
	defer finalCancel()
	finalAttempt, err := repo.BeginAsyncImageResultFetchAttempt(finalCtx, task.TaskID, -1, 3)
	if err != nil {
		return asyncImageWorkerDisposition{requeue: true, delay: 5 * time.Second}, true
	}
	finalCtx = context.WithValue(finalCtx, ctxkey.ClientRequestID, "async-image:"+task.TaskID)
	finalCtx = context.WithValue(finalCtx, asyncImageResolvedOutputsKey{}, outputs)
	finalCtx = context.WithValue(finalCtx, asyncImageResultFinalizeAttemptKey{}, finalAttempt)
	return h.completeAsyncImageInvocation(parent, finalCtx, task, nil, capture, nil, cfg), true
}

func asyncImageFailureEvent(code, message, status string) json.RawMessage {
	stage := status
	if strings.HasPrefix(code, "result_fetch") || strings.Contains(message, "generated image") {
		stage = "result_fetch"
	}
	payload, _ := json.Marshal(map[string]string{"stage": stage, "error_code": code, "message": asyncImageSafeError(errors.New(message))})
	return payload
}

func (h *DurableAsyncImageHandler) recordAsyncImageResultWithRetry(ctx context.Context, params service.RecordAsyncImageUpstreamSuccessParams) (*service.AsyncImageTask, error) {
	ctx, cancel := asyncImageTerminalTransitionContext(ctx)
	defer cancel()
	var task *service.AsyncImageTask
	var err error
	for i := 0; i < 3; i++ {
		task, err = h.tasks.RecordUpstreamSuccess(ctx, params)
		if err == nil {
			return task, nil
		}
		current, getErr := h.tasks.Repository().GetAsyncImageTaskByTaskID(ctx, params.TaskID)
		if getErr == nil && current != nil && len(current.BillingPayload) > 0 && current.BillingRequestID != nil && *current.BillingRequestID == params.BillingRequestID && current.Status != service.AsyncImageTaskStatusInvoking {
			return current, nil
		}
		if errors.Is(err, service.ErrAsyncImageInvalidTransition) || !waitAsyncImageResultPersist(ctx, i) {
			break
		}
	}
	return nil, fmt.Errorf("persist captured image result: %w", err)
}
