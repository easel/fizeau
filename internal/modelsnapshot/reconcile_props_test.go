package modelsnapshot

import (
	"testing"

	"github.com/easel/fizeau/internal/modelcatalog"
)

// TestReconcilePropsModels_ServedAliasInheritsRecoveredIdentity verifies that a
// generic served alias ("dflash", no catalog power) inherits the catalog
// identity recovered from /props ("Qwen3.6-27B", power 5) as its CatalogID, and
// that the standalone /props identities are dropped.
func TestReconcilePropsModels_ServedAliasInheritsRecoveredIdentity(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	models := []discoveredModel{
		{Provider: "sindri", ID: "dflash", Via: SourceNativeAPI, EndpointName: "default"},
		{Provider: "sindri", ID: "Qwen3.6-27B", Via: SourcePropsAPI, EndpointName: "default"},
		{Provider: "sindri", ID: "Qwen3.6-27B-Q4_K_M", Via: SourcePropsAPI, EndpointName: "default"},
	}
	out := reconcilePropsModels(models, true, "available", cat)

	if len(out) != 1 {
		t.Fatalf("want 1 served model, got %d: %+v", len(out), out)
	}
	if out[0].ID != "dflash" {
		t.Errorf("wire ID = %q, want dflash (server alias preserved)", out[0].ID)
	}
	if out[0].CatalogID == "" {
		t.Errorf("served alias did not inherit a recovered catalog identity; CatalogID empty")
	}
	if pw := EnrichModel(KnownModel{ID: out[0].CatalogID, Status: StatusAvailable}, true, cat).Power; pw <= 0 {
		t.Errorf("CatalogID %q resolves to power %d, want > 0", out[0].CatalogID, pw)
	}
}

func TestReconcilePropsModels_NoPropsIsNoOp(t *testing.T) {
	cat, err := modelcatalog.Default()
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	models := []discoveredModel{
		{Provider: "bragi", ID: "qwen3.5-27b", Via: SourceNativeAPI},
	}
	out := reconcilePropsModels(models, true, "available", cat)
	if len(out) != 1 || out[0].ID != "qwen3.5-27b" || out[0].CatalogID != "" {
		t.Errorf("no-props provider mutated: %+v", out)
	}
}
