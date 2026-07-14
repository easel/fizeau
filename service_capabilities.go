package fizeau

import "github.com/easel/fizeau/internal/serviceimpl"

// HarnessCapabilityStatus classifies one harness capability in the public
// ListHarnesses capability matrix.
type HarnessCapabilityStatus string

const (
	HarnessCapabilityRequired      HarnessCapabilityStatus = "required"
	HarnessCapabilityOptional      HarnessCapabilityStatus = "optional"
	HarnessCapabilityUnsupported   HarnessCapabilityStatus = "unsupported"
	HarnessCapabilityNotApplicable HarnessCapabilityStatus = "not_applicable"
)

// HarnessCapability describes one capability row for one harness.
type HarnessCapability struct {
	Status HarnessCapabilityStatus
	Detail string
}

// HarnessCapabilityMatrix is the public, per-harness capability table exposed
// by ListHarnesses. The fields intentionally match CONTRACT-003's required
// capability categories.
type HarnessCapabilityMatrix struct {
	ExecutePrompt   HarnessCapability
	ModelDiscovery  HarnessCapability
	ModelPinning    HarnessCapability
	WorkdirContext  HarnessCapability
	ReasoningLevels HarnessCapability
	PermissionModes HarnessCapability
	ProgressEvents  HarnessCapability
	UsageCapture    HarnessCapability
	FinalText       HarnessCapability
	ToolEvents      HarnessCapability
	QuotaStatus     HarnessCapability
	RecordReplay    HarnessCapability
}

func publicHarnessCapabilityMatrix(matrix serviceimpl.HarnessCapabilityMatrix) HarnessCapabilityMatrix {
	return HarnessCapabilityMatrix{
		ExecutePrompt:   publicHarnessCapability(matrix.ExecutePrompt),
		ModelDiscovery:  publicHarnessCapability(matrix.ModelDiscovery),
		ModelPinning:    publicHarnessCapability(matrix.ModelPinning),
		WorkdirContext:  publicHarnessCapability(matrix.WorkdirContext),
		ReasoningLevels: publicHarnessCapability(matrix.ReasoningLevels),
		PermissionModes: publicHarnessCapability(matrix.PermissionModes),
		ProgressEvents:  publicHarnessCapability(matrix.ProgressEvents),
		UsageCapture:    publicHarnessCapability(matrix.UsageCapture),
		FinalText:       publicHarnessCapability(matrix.FinalText),
		ToolEvents:      publicHarnessCapability(matrix.ToolEvents),
		QuotaStatus:     publicHarnessCapability(matrix.QuotaStatus),
		RecordReplay:    publicHarnessCapability(matrix.RecordReplay),
	}
}

func publicHarnessCapability(capability serviceimpl.HarnessCapability) HarnessCapability {
	return HarnessCapability{
		Status: HarnessCapabilityStatus(capability.Status),
		Detail: capability.Detail,
	}
}
