package fizeau_test

import (
	"reflect"
	"strings"
	"testing"

	fizeau "github.com/easel/fizeau"
)

func TestRoutingQualityFieldsExposedOnPublicTypes(t *testing.T) {
	report := reflect.TypeOf(fizeau.RouteStatusReport{})
	usage := reflect.TypeOf(fizeau.UsageReport{})
	rq := reflect.TypeOf(fizeau.RoutingQualityMetrics{})
	bucket := reflect.TypeOf(fizeau.OverrideClassBucket{})

	assertRoutingQualityFieldTypeAndTag(t, report, "RoutingQuality", "RoutingQualityMetrics", "")
	assertRoutingQualityFieldTypeAndTag(t, usage, "RoutingQuality", "RoutingQualityMetrics", `json:"routing_quality"`)

	assertRoutingQualityFieldTypeAndTag(t, rq, "AutoAcceptanceRate", "float64", `json:"auto_acceptance_rate"`)
	assertRoutingQualityFieldTypeAndTag(t, rq, "OverrideDisagreementRate", "float64", `json:"override_disagreement_rate"`)
	assertRoutingQualityFieldTypeAndTag(t, rq, "OverrideClassBreakdown", "[]OverrideClassBucket", `json:"override_class_breakdown,omitempty"`)
	assertRoutingQualityFieldTypeAndTag(t, rq, "TotalRequests", "int", `json:"total_requests"`)
	assertRoutingQualityFieldTypeAndTag(t, rq, "TotalOverrides", "int", `json:"total_overrides"`)

	assertRoutingQualityFieldTypeAndTag(t, bucket, "PromptFeatureBucket", "string", `json:"prompt_feature_bucket"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "Axis", "string", `json:"axis"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "Match", "bool", `json:"match"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "Count", "int", `json:"count"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "SuccessOutcomes", "int", `json:"success_outcomes"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "StalledOutcomes", "int", `json:"stalled_outcomes"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "FailedOutcomes", "int", `json:"failed_outcomes"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "CancelledOutcomes", "int", `json:"cancelled_outcomes"`)
	assertRoutingQualityFieldTypeAndTag(t, bucket, "UnknownOutcomes", "int", `json:"unknown_outcomes"`)
}

func assertRoutingQualityFieldTypeAndTag(t *testing.T, typ reflect.Type, fieldName, wantType, wantTag string) {
	t.Helper()
	field, ok := typ.FieldByName(fieldName)
	if !ok {
		t.Fatalf("%s missing field %s", typ.Name(), fieldName)
	}
	gotType := strings.ReplaceAll(field.Type.String(), "fizeau.", "")
	if gotType != wantType {
		t.Errorf("%s.%s type = %s, want %s", typ.Name(), fieldName, gotType, wantType)
	}
	if got := string(field.Tag); got != wantTag {
		t.Errorf("%s.%s tag = %q, want %q", typ.Name(), fieldName, got, wantTag)
	}
}
