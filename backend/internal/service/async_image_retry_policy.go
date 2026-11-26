package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

const AsyncImageRetryMaxPolicyBytes = 1 << 20

type AsyncImageRetryPolicy struct {
	Revision               int64                 `json:"revision"`
	PauseGenerationRetries bool                  `json:"pause_generation_retries"`
	Rules                  []AsyncImageRetryRule `json:"rules"`
}

type AsyncImageRetryRule struct {
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	Enabled            bool                  `json:"enabled"`
	Scope              AsyncImageRetryScope  `json:"scope"`
	Match              AsyncImageRetryMatch  `json:"match"`
	Category           string                `json:"category,omitempty"`
	MaxAttempts        int                   `json:"max_attempts"`
	AllowedAccountIDs  []int64               `json:"allowed_account_ids,omitempty"`
	ExcludedAccountIDs []int64               `json:"excluded_account_ids,omitempty"`
	Steps              []AsyncImageRetryStep `json:"steps"`
	OnExhausted        string                `json:"on_exhausted"`
}

type AsyncImageRetryScope struct {
	Platforms        []string `json:"platforms,omitempty"`
	Models           []string `json:"models,omitempty"`
	GroupIDs         []int64  `json:"group_ids,omitempty"`
	SourceAccountIDs []int64  `json:"source_account_ids,omitempty"`
	Stages           []string `json:"stages,omitempty"`
	Categories       []string `json:"categories,omitempty"`
}

type AsyncImageRetryMatch struct {
	HTTPStatuses []int    `json:"http_statuses,omitempty"`
	ErrorCodes   []string `json:"error_codes,omitempty"`
	ContainsAny  []string `json:"contains_any,omitempty"`
	ContainsAll  []string `json:"contains_all,omitempty"`
	Excludes     []string `json:"excludes,omitempty"`
	Regex        string   `json:"regex,omitempty"`
	compiled     *regexp.Regexp
}

type AsyncImageRetryStep struct {
	ID              string `json:"id"`
	Action          string `json:"action"`
	MaxAttempts     int    `json:"max_attempts"`
	DelaySeconds    int    `json:"delay_seconds"`
	Backoff         string `json:"backoff"`
	MaxDelaySeconds int    `json:"max_delay_seconds,omitempty"`
}

type AsyncImageRetryFact struct {
	TaskID        string    `json:"task_id,omitempty"`
	FailureID     string    `json:"failure_id,omitempty"`
	AttemptID     string    `json:"attempt_id,omitempty"`
	Platform      string    `json:"platform,omitempty"`
	Model         string    `json:"model,omitempty"`
	Stage         string    `json:"stage"`
	Category      string    `json:"category,omitempty"`
	ErrorCode     string    `json:"error_code,omitempty"`
	Message       string    `json:"message"`
	GroupID       int64     `json:"group_id,omitempty"`
	AccountID     int64     `json:"account_id,omitempty"`
	HTTPStatus    int       `json:"http_status,omitempty"`
	OccurredAt    time.Time `json:"occurred_at"`
	SafeToRetry   bool      `json:"safe_to_retry"`
	HasOutput     bool      `json:"has_output"`
	HasReferences bool      `json:"has_references"`
	Dispatched    bool      `json:"dispatched"`
	Unknown       bool      `json:"unknown"`
}

type AsyncImageRetryDecision struct {
	Source             string  `json:"source"`
	RuleID             string  `json:"rule_id,omitempty"`
	StepID             string  `json:"step_id,omitempty"`
	Category           string  `json:"category,omitempty"`
	RequestedAction    string  `json:"requested_action,omitempty"`
	Action             string  `json:"action,omitempty"`
	BlockReason        string  `json:"block_reason,omitempty"`
	Revision           int64   `json:"revision"`
	DelaySeconds       int     `json:"delay_seconds"`
	AllowedAccountIDs  []int64 `json:"allowed_account_ids,omitempty"`
	ExcludedAccountIDs []int64 `json:"excluded_account_ids,omitempty"`
}

type AsyncImageRetryUsage struct {
	Rules map[string]int            `json:"rules"`
	Steps map[string]map[string]int `json:"steps"`
}

var asyncImageRetryStages = []string{"prepare", "submit", "download", "storage", "billing"}
var asyncImageRetryActions = []string{
	"same_account", "other_account", "local_same_account", "local_other_account", "retry_prepare",
	"resume_download", "resume_storage", "resume_billing", "stop", "manual_review",
}
var asyncImageRetryID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)
var asyncImageRetryDiagnosticURL = regexp.MustCompile(`(?i)(?:https?://|data:)[^\s<>"'\x60]+`)
var asyncImageRetryCredentialDiagnostic = regexp.MustCompile(`(?i)\b(?:bearer\s+[A-Za-z0-9_./+=-]+|sk-[A-Za-z0-9_-]{12,})`)
var asyncImageRetryLongDiagnosticValue = regexp.MustCompile(`[A-Za-z0-9_+/=-]{96,}`)
var asyncImageRetryPromptDiagnostic = regexp.MustCompile(`(?i)(?:prompt|messages?|input_text|提示词)["']?\s*[:=]\s*.+`)

func ValidateAsyncImageRetryPolicy(policy *AsyncImageRetryPolicy) error {
	raw, err := json.Marshal(policy)
	if err != nil || len(raw) > AsyncImageRetryMaxPolicyBytes || len(policy.Rules) > 200 || policy.Revision < 0 {
		return fmt.Errorf("invalid revision or policy exceeds 200 rules / 1 MiB")
	}
	if policy.Rules == nil {
		policy.Rules = []AsyncImageRetryRule{}
	}
	ruleIDs := make(map[string]bool, len(policy.Rules))
	for i := range policy.Rules {
		rule := &policy.Rules[i]
		if !asyncImageRetryID.MatchString(rule.ID) || ruleIDs[rule.ID] {
			return fmt.Errorf("rule %d has an invalid or duplicate ID", i+1)
		}
		ruleIDs[rule.ID] = true
		if err := validateAsyncImageRetryRule(rule); err != nil {
			return fmt.Errorf("rule %s: %w", rule.ID, err)
		}
	}
	return nil
}

func validateAsyncImageRetryRule(rule *AsyncImageRetryRule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" || utf8.RuneCountInString(rule.Name) > 120 || utf8.RuneCountInString(rule.Category) > 80 {
		return fmt.Errorf("name or category length is invalid")
	}
	if len(rule.Steps) < 1 || len(rule.Steps) > 8 || rule.MaxAttempts < 0 || rule.MaxAttempts > 128 {
		return fmt.Errorf("invalid rule budget or step count")
	}
	for _, ids := range [][]int64{rule.Scope.GroupIDs, rule.Scope.SourceAccountIDs, rule.AllowedAccountIDs, rule.ExcludedAccountIDs} {
		for _, id := range ids {
			if id <= 0 {
				return fmt.Errorf("account and group IDs must be positive")
			}
		}
	}
	for _, stage := range rule.Scope.Stages {
		if !slices.Contains(asyncImageRetryStages, stage) {
			return fmt.Errorf("unsupported stage %q", stage)
		}
	}
	for _, platform := range rule.Scope.Platforms {
		if platform != PlatformOpenAI && platform != PlatformGemini {
			return fmt.Errorf("unsupported platform %q", platform)
		}
	}
	m := &rule.Match
	for _, status := range m.HTTPStatuses {
		if status < 100 || status > 599 {
			return fmt.Errorf("invalid HTTP status %d", status)
		}
	}
	for _, values := range [][]string{rule.Scope.Categories, rule.Scope.Models, m.ErrorCodes, m.ContainsAny, m.ContainsAll, m.Excludes} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" || utf8.RuneCountInString(value) > 256 {
				return fmt.Errorf("match values must contain 1–256 characters")
			}
		}
	}
	if len(m.HTTPStatuses)+len(m.ErrorCodes)+len(m.ContainsAny)+len(m.ContainsAll)+len(rule.Scope.Categories) == 0 && m.Regex == "" {
		return fmt.Errorf("at least one positive error condition is required")
	}
	if len(m.Regex) > 2048 {
		return fmt.Errorf("regular expression exceeds 2048 bytes")
	}
	m.compiled = nil
	if m.Regex != "" {
		pattern, err := regexp.Compile(m.Regex)
		if err != nil {
			return fmt.Errorf("invalid regular expression")
		}
		m.compiled = pattern
	}
	if rule.OnExhausted == "" {
		rule.OnExhausted = "manual_review"
	}
	if rule.OnExhausted != "manual_review" && rule.OnExhausted != "stop" {
		return fmt.Errorf("invalid exhaustion action")
	}
	stepIDs := make(map[string]bool, len(rule.Steps))
	for i := range rule.Steps {
		step := &rule.Steps[i]
		if !asyncImageRetryID.MatchString(step.ID) || stepIDs[step.ID] || !slices.Contains(asyncImageRetryActions, step.Action) {
			return fmt.Errorf("invalid action, invalid ID, or duplicate ID on step %d", i+1)
		}
		stepIDs[step.ID] = true
		if step.MaxAttempts < 1 || step.MaxAttempts > 16 || step.DelaySeconds < 0 || step.DelaySeconds > 1800 ||
			step.MaxDelaySeconds < 0 || step.MaxDelaySeconds > 1800 || (step.MaxDelaySeconds > 0 && step.MaxDelaySeconds < step.DelaySeconds) {
			return fmt.Errorf("invalid retry budget or delay on step %s", step.ID)
		}
		if step.Backoff == "" {
			step.Backoff = "fixed"
		}
		if step.Backoff != "fixed" && step.Backoff != "exponential" {
			return fmt.Errorf("invalid backoff on step %s", step.ID)
		}
	}
	return nil
}

// EvaluateAsyncImageRetry performs only rule matching. Runtime and sample
// preview must also call ProtectAsyncImageRetryDecision with trusted facts.
func EvaluateAsyncImageRetry(policy AsyncImageRetryPolicy, fact AsyncImageRetryFact, usage AsyncImageRetryUsage) AsyncImageRetryDecision {
	decision := AsyncImageRetryDecision{Source: "none", Revision: policy.Revision}
	for _, rule := range policy.Rules {
		if !rule.Enabled || !matchAsyncImageRetryRule(rule, fact) {
			continue
		}
		decision.Source, decision.RuleID, decision.Category = "custom", rule.ID, rule.Category
		decision.AllowedAccountIDs, decision.ExcludedAccountIDs = rule.AllowedAccountIDs, rule.ExcludedAccountIDs
		if rule.MaxAttempts == 0 || usage.Rules[rule.ID] < rule.MaxAttempts {
			for _, step := range rule.Steps {
				used := usage.Steps[rule.ID][step.ID]
				if used < step.MaxAttempts {
					decision.StepID, decision.RequestedAction, decision.Action = step.ID, step.Action, step.Action
					decision.DelaySeconds = asyncImageRuleDelay(step, used)
					return decision
				}
			}
		}
		decision.Action = rule.OnExhausted
		if decision.Action == "" {
			decision.Action = "manual_review"
		}
		decision.RequestedAction, decision.BlockReason = decision.Action, "rule_exhausted"
		return decision
	}
	return decision
}

func matchAsyncImageRetryRule(rule AsyncImageRetryRule, fact AsyncImageRetryFact) bool {
	scope, match := rule.Scope, rule.Match
	if !matchesAsyncImageRetryField(scope.Platforms, fact.Platform) || !matchesAsyncImageRetryField(scope.Models, fact.Model) ||
		!matchesAsyncImageRetryField(scope.GroupIDs, fact.GroupID) || !matchesAsyncImageRetryField(scope.SourceAccountIDs, fact.AccountID) ||
		!matchesAsyncImageRetryField(scope.Stages, fact.Stage) || !matchesAsyncImageRetryField(scope.Categories, fact.Category) ||
		!matchesAsyncImageRetryField(match.HTTPStatuses, fact.HTTPStatus) || !matchesAsyncImageRetryField(match.ErrorCodes, fact.ErrorCode) {
		return false
	}
	message := strings.ToLower(fact.Message)
	contains := func(word string) bool { return strings.Contains(message, strings.ToLower(strings.TrimSpace(word))) }
	if slices.ContainsFunc(match.Excludes, contains) || (len(match.ContainsAny) > 0 && !slices.ContainsFunc(match.ContainsAny, contains)) ||
		slices.ContainsFunc(match.ContainsAll, func(word string) bool { return !contains(word) }) {
		return false
	}
	if match.Regex == "" {
		return true
	}
	pattern := match.compiled
	if pattern == nil {
		var err error
		pattern, err = regexp.Compile(match.Regex)
		if err != nil {
			return false
		}
	}
	return pattern.MatchString(fact.Message)
}

func matchesAsyncImageRetryField[T comparable](values []T, value T) bool {
	return len(values) == 0 || slices.Contains(values, value)
}

func asyncImageRuleDelay(step AsyncImageRetryStep, used int) int {
	if step.Backoff != "exponential" {
		return step.DelaySeconds
	}
	limit := step.MaxDelaySeconds
	if limit <= 0 {
		limit = 1800
	}
	return min(limit, step.DelaySeconds*(1<<min(16, max(0, used))))
}

func IsAsyncImageGenerationRetry(action string) bool {
	switch action {
	case "same_account", "other_account", "local_same_account", "local_other_account":
		return true
	default:
		return false
	}
}

func AsyncImageRetryAccountAllowed(decision AsyncImageRetryDecision, fact AsyncImageRetryFact, accountID int64) bool {
	if !IsAsyncImageGenerationRetry(decision.Action) {
		return true
	}
	if accountID <= 0 || fact.AccountID <= 0 || slices.Contains(decision.ExcludedAccountIDs, accountID) ||
		!matchesAsyncImageRetryField(decision.AllowedAccountIDs, accountID) {
		return false
	}
	switch decision.Action {
	case "same_account", "local_same_account":
		return accountID == fact.AccountID
	default:
		return accountID != fact.AccountID
	}
}

func ProtectAsyncImageRetryDecision(fact AsyncImageRetryFact, decision AsyncImageRetryDecision) AsyncImageRetryDecision {
	if decision.Source != "custom" {
		return decision
	}
	block := func(reason string) AsyncImageRetryDecision {
		decision.Action, decision.BlockReason, decision.DelaySeconds = "manual_review", reason, 0
		return decision
	}
	if fact.Unknown {
		return block("execution_unknown")
	}
	stageRecovery := ""
	switch fact.Stage {
	case "download":
		stageRecovery = "resume_download"
	case "storage":
		stageRecovery = "resume_storage"
	case "billing":
		stageRecovery = "resume_billing"
	}
	if fact.HasOutput && decision.Action == "stop" {
		return block("protect_existing_output")
	}
	if IsAsyncImageGenerationRetry(decision.Action) {
		if fact.HasOutput {
			if stageRecovery != "" && fact.SafeToRetry {
				decision.Action, decision.BlockReason = stageRecovery, "protect_existing_output"
				return decision
			}
			return block("protect_existing_output")
		}
		if fact.Stage != "submit" && fact.Stage != "prepare" {
			return block("action_stage_mismatch")
		}
		if !fact.SafeToRetry {
			return block("upstream_replay_not_confirmed_safe")
		}
		if fact.AccountID <= 0 {
			return block("source_account_unavailable")
		}
		if strings.HasPrefix(decision.Action, "local_") && !fact.HasReferences {
			return block("references_unavailable")
		}
		return decision
	}
	if decision.Action == "retry_prepare" {
		if fact.Stage != "prepare" || !fact.SafeToRetry {
			return block("prepare_recovery_unavailable")
		}
	} else if strings.HasPrefix(decision.Action, "resume_") &&
		(stageRecovery != decision.Action || !fact.HasOutput || !fact.SafeToRetry) {
		return block("post_processing_recovery_unavailable")
	}
	return decision
}

func SanitizeAsyncImageRetryFact(fact AsyncImageRetryFact) AsyncImageRetryFact {
	fact.Message = sanitizeAsyncImageRetryDiagnostic(fact.Message, 768)
	fact.ErrorCode = sanitizeAsyncImageRetryDiagnostic(fact.ErrorCode, 128)
	return fact
}

func sanitizeAsyncImageRetryDiagnostic(value string, limit int) string {
	value = asyncImageRetryPromptDiagnostic.ReplaceAllString(value, "[redacted input]")
	value = asyncImageRetryCredentialDiagnostic.ReplaceAllString(value, "[credential]")
	value = logredact.RedactText(value, "secret", "cookie", "x-goog-api-key", "access_token", "refresh_token", "signature", "password", "request_id", "upstream_request_id")
	value = asyncImageRetryDiagnosticURL.ReplaceAllString(value, "[url]")
	value = asyncImageRetryLongDiagnosticValue.ReplaceAllString(value, "[redacted]")
	value = strings.Join(strings.Fields(value), " ")
	if utf8.RuneCountInString(value) > limit {
		value = string([]rune(value)[:limit])
	}
	return value
}
