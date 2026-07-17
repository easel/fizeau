package core

import (
	"errors"
	"fmt"
	"math"
)

const (
	// ContextCapacityErrorCode is the stable machine-readable identity for a
	// direct-core provider call rejected before dispatch.
	ContextCapacityErrorCode = "CONTEXT_CAPACITY_EXCEEDED"

	ContextCapacityCallPlanning = "planning"
	ContextCapacityCallMain     = "main"

	ContextCapacityActionClamped         = "clamped"
	ContextCapacityActionPlanningSkipped = "planning_skipped"
	ContextCapacityActionRejected        = "rejected"

	ContextCapacityInputCompactionContextWindow = "CompactionContextWindow"
)

var (
	ErrContextCapacityExceeded     = errors.New("agent: context capacity exceeded")
	ErrContextCapacityInputInvalid = errors.New("agent: invalid context capacity input")
)

// ContextCapacityInputError identifies an invalid raw input before a core
// session starts. It is distinct from ContextCapacityError, which means a
// valid provider-call envelope exhausted the selected route's capacity.
type ContextCapacityInputError struct {
	Field string
	Value int
}

func (e *ContextCapacityInputError) Error() string {
	if e == nil {
		return ErrContextCapacityInputInvalid.Error()
	}
	return fmt.Sprintf("%s: %s=%d must be >= 0", ErrContextCapacityInputInvalid, e.Field, e.Value)
}

func (*ContextCapacityInputError) Unwrap() error { return ErrContextCapacityInputInvalid }

// ContextCapacityError reports the exact provider-call envelope that could
// not fit within the selected route's effective context window.
type ContextCapacityError struct {
	CallKind              string
	TurnIndex             int
	AttemptIndex          int
	ContextWindow         int
	EffectiveWindow       int
	EstimatedInputTokens  int
	RequestedMaxTokens    int
	AvailableOutputTokens int
}

func (e *ContextCapacityError) Error() string {
	if e == nil {
		return ErrContextCapacityExceeded.Error()
	}
	return fmt.Sprintf("%s: %s call turn %d attempt %d estimated input %d leaves no output capacity in effective window %d (context %d, requested max_tokens %d)",
		ContextCapacityErrorCode, e.CallKind, e.TurnIndex, e.AttemptIndex,
		e.EstimatedInputTokens, e.EffectiveWindow, e.ContextWindow, e.RequestedMaxTokens)
}

func (*ContextCapacityError) Unwrap() error { return ErrContextCapacityExceeded }

func (*ContextCapacityError) Code() string { return ContextCapacityErrorCode }

// ResolveWorkingContextWindow applies the raw operator override without
// enlarging known selected-route capacity.
func ResolveWorkingContextWindow(selectedWindow, compactionOverride int) (int, error) {
	if compactionOverride < 0 {
		return 0, &ContextCapacityInputError{
			Field: ContextCapacityInputCompactionContextWindow,
			Value: compactionOverride,
		}
	}
	if compactionOverride == 0 {
		return selectedWindow, nil
	}
	if selectedWindow <= 0 || compactionOverride < selectedWindow {
		return compactionOverride, nil
	}
	return selectedWindow, nil
}

// ContextCapacityEventData is the primitive core-owned event payload. Public
// and harness projections are deliberately owned by later service layers.
type ContextCapacityEventData struct {
	Action                 string `json:"action"`
	CallKind               string `json:"call_kind"`
	TurnIndex              int    `json:"turn_index"`
	AttemptIndex           int    `json:"attempt_index"`
	ContextWindow          int    `json:"context_window"`
	EffectiveContextWindow int    `json:"effective_context_window"`
	EstimatedInputTokens   int    `json:"estimated_input_tokens"`
	RequestedMaxTokens     int    `json:"requested_max_tokens"`
	EffectiveMaxTokens     int    `json:"effective_max_tokens"`
	AvailableOutputTokens  int    `json:"available_output_tokens"`
}

// EstimateTextTokens estimates one string as ceil(UTF-8 bytes/4).
func EstimateTextTokens(s string) int {
	n := len(s)
	return n/4 + boolInt(n%4 != 0)
}

// EstimateMessageTokens estimates every provider-visible string in one
// message, saturating at math.MaxInt.
func EstimateMessageTokens(message Message) int {
	total := EstimateTextTokens(string(message.Role))
	total = saturatingAdd(total, EstimateTextTokens(message.Content))
	if message.Role == RoleAssistant {
		for _, call := range message.ToolCalls {
			total = saturatingAdd(total, EstimateTextTokens(call.Name))
			total = saturatingAdd(total, EstimateTextTokens(string(call.Arguments)))
		}
	}
	if message.Role == RoleTool {
		total = saturatingAdd(total, EstimateTextTokens(message.ToolCallID))
	}
	return total
}

// EstimateProviderCallTokens estimates the exact messages and tool
// definitions about to be sent to a native provider.
func EstimateProviderCallTokens(messages []Message, tools []ToolDef) int {
	total := 0
	for _, message := range messages {
		total = saturatingAdd(total, EstimateMessageTokens(message))
	}
	for _, tool := range tools {
		total = saturatingAdd(total, EstimateTextTokens(tool.Name))
		total = saturatingAdd(total, EstimateTextTokens(tool.Description))
		total = saturatingAdd(total, EstimateTextTokens(string(tool.Parameters)))
	}
	return total
}

func saturatingAdd(a, b int) int {
	if a >= math.MaxInt-b {
		return math.MaxInt
	}
	return a + b
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func scaleContextPercent(window, percent int) int {
	if window <= 0 || percent <= 0 {
		return 0
	}
	if percent >= 100 {
		return window
	}
	quotient := window / 100
	remainder := window % 100
	return quotient*percent + remainder*percent/100
}

type contextCapacityController struct {
	contextWindow int
	attempts      CapacityAttemptState
}

type contextCapacityDecision struct {
	Options      Options
	AttemptIndex int
	Event        *ContextCapacityEventData
	Err          *ContextCapacityError
}

func newContextCapacityController(contextWindow int, initial CapacityAttemptState) *contextCapacityController {
	return &contextCapacityController{
		contextWindow: contextWindow,
		attempts:      cloneCapacityAttempts(initial),
	}
}

func cloneCapacityAttempts(initial CapacityAttemptState) CapacityAttemptState {
	cloned := make(CapacityAttemptState, len(initial))
	for key, attempt := range initial {
		cloned[key] = attempt
	}
	return cloned
}

func (c *contextCapacityController) preflight(callKind string, turnIndex int, messages []Message, tools []ToolDef, requested Options) contextCapacityDecision {
	key := CapacityAttemptKey{CallKind: callKind, TurnIndex: turnIndex}
	attemptIndex := c.attempts[key]
	if attemptIndex < math.MaxInt {
		attemptIndex++
	}
	c.attempts[key] = attemptIndex
	decision := contextCapacityDecision{Options: requested, AttemptIndex: attemptIndex}

	// A non-positive selected window means direct-core capacity is unknown.
	// Native service execution resolves a positive fallback before this seam.
	if c.contextWindow <= 0 {
		return decision
	}

	effectiveWindow := scaleContextPercent(c.contextWindow, 95)
	estimatedInput := EstimateProviderCallTokens(messages, tools)
	availableOutput := 0
	if estimatedInput < effectiveWindow {
		availableOutput = effectiveWindow - estimatedInput
	}
	payload := ContextCapacityEventData{
		CallKind:               callKind,
		TurnIndex:              turnIndex,
		AttemptIndex:           attemptIndex,
		ContextWindow:          c.contextWindow,
		EffectiveContextWindow: effectiveWindow,
		EstimatedInputTokens:   estimatedInput,
		RequestedMaxTokens:     requested.MaxTokens,
		AvailableOutputTokens:  availableOutput,
	}

	if availableOutput == 0 {
		if callKind == ContextCapacityCallPlanning {
			payload.Action = ContextCapacityActionPlanningSkipped
		} else {
			payload.Action = ContextCapacityActionRejected
			decision.Err = &ContextCapacityError{
				CallKind:              callKind,
				TurnIndex:             turnIndex,
				AttemptIndex:          attemptIndex,
				ContextWindow:         c.contextWindow,
				EffectiveWindow:       effectiveWindow,
				EstimatedInputTokens:  estimatedInput,
				RequestedMaxTokens:    requested.MaxTokens,
				AvailableOutputTokens: availableOutput,
			}
		}
		decision.Event = &payload
		return decision
	}

	if requested.MaxTokens > availableOutput {
		decision.Options.MaxTokens = availableOutput
		payload.Action = ContextCapacityActionClamped
		payload.EffectiveMaxTokens = availableOutput
		decision.Event = &payload
	}
	return decision
}
