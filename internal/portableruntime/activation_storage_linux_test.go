//go:build linux

package portableruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActivationProjectionRecipeDescriptorPins(t *testing.T) {
	assemble := func(t *testing.T) (*Bundle, string, ActivationRecipe) {
		t.Helper()
		fixture := newActivationFixture(t)
		bundle := prepareMaterializerFixture(t, fixture)
		writable := emptyActivationWritableRoot(t)
		plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), writable, os.LookupEnv, testActivationIdentityReader)
		if err != nil {
			t.Fatal(err)
		}
		recipe, ok := plan.EntrypointRecipe("fixture")
		if !ok || recipe.projection == nil {
			t.Fatal("activation did not retain an opaque projection descriptor recipe")
		}
		return bundle, writable, recipe
	}

	t.Run("mutable leaf remains replaceable", func(t *testing.T) {
		_, writable, recipe := assemble(t)
		mutable := filepath.Join(writable, activationChild, "data", "tool", "auth.json")
		if err := os.Remove(mutable); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(mutable, []byte("replacement credential\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := recipe.RevalidatePortableProjectionDescriptors(); err != nil {
			t.Fatalf("mutable projection leaf invalidated recipe: %v", err)
		}
	})

	t.Run("immutable source content is pinned", func(t *testing.T) {
		bundle, _, recipe := assemble(t)
		if err := os.WriteFile(filepath.Join(bundle.RuntimeRoot(), "config", "tool", "settings.json"), []byte("tampered\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := recipe.RevalidatePortableProjectionDescriptors(); !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("tampered immutable source revalidation error = %v", err)
		}
	})

	t.Run("required absent parent is pinned", func(t *testing.T) {
		_, writable, recipe := assemble(t)
		forbidden := filepath.Join(writable, activationChild, "data", "tool", "forbidden.lock")
		if err := os.MkdirAll(filepath.Dir(forbidden), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(forbidden, []byte("present\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := recipe.RevalidatePortableProjectionDescriptors(); !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("required-absent path revalidation error = %v", err)
		}
	})

	t.Run("activation mountpoint replacement is rejected", func(t *testing.T) {
		_, writable, recipe := assemble(t)
		activation := filepath.Join(writable, activationChild)
		if err := os.Rename(activation, filepath.Join(writable, "moved-activation")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(activation, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := recipe.RevalidatePortableProjectionDescriptors(); !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("replaced activation mountpoint revalidation error = %v", err)
		}
	})
}

func TestActivationProjectionMountPlanIsDescriptorIndexedAndImmutableLast(t *testing.T) {
	fixture := newActivationFixture(t)
	bundle := prepareMaterializerFixture(t, fixture)
	plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), emptyActivationWritableRoot(t), os.LookupEnv, testActivationIdentityReader)
	if err != nil {
		t.Fatal(err)
	}
	recipe, ok := plan.EntrypointRecipe("fixture")
	if !ok {
		t.Fatal("missing fixture recipe")
	}
	mountPlan, err := recipe.PortableNamespaceProjectionPlan()
	if err != nil {
		t.Fatalf("PortableNamespaceProjectionPlan: %v", err)
	}
	if !mountPlan.valid() || len(mountPlan.records) == 0 || len(mountPlan.descriptors) == 0 {
		t.Fatalf("invalid mount plan: %#v", mountPlan)
	}
	immutableStarted := false
	immutableCount := 0
	for index, record := range mountPlan.records {
		if record.order != uint16(index) || int(record.descriptorIndex) >= len(mountPlan.descriptors) {
			t.Fatalf("record %d is not a deterministic descriptor index: %#v", index, record)
		}
		if record.role == portableProjectionRoleImmutableConfig {
			immutableStarted = true
			immutableCount++
			if record.operation != portableProjectionOperationReadOnlyBind {
				t.Fatalf("immutable config operation = %d", record.operation)
			}
		} else if immutableStarted {
			t.Fatalf("non-immutable projection record follows immutable bind: %#v", record)
		}
		if !validPortableProjectionTarget(record.target) {
			t.Fatalf("record target is not guest-relative: %q", record.target)
		}
	}
	if immutableCount == 0 {
		t.Fatal("fixture mount plan did not contain an immutable config bind")
	}
	if len(mountPlan.records) < 2 || mountPlan.records[0].role != portableProjectionRoleGovernedRoot || mountPlan.records[1].role != portableProjectionRoleActivationRoot {
		t.Fatalf("mount-plan roots = %#v", mountPlan.records[:min(2, len(mountPlan.records))])
	}

	text := fmt.Sprintf("%#v", mountPlan)
	for _, forbidden := range []string{bundle.RuntimeRoot(), plan.BackingRoot(), "settings.json", "auth.json"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("mount-plan diagnostic leaked %q: %s", forbidden, text)
		}
	}

	// The accessor owns its result; a caller cannot modify the activation plan
	// that will later be carried to PID 1.
	mountPlan.records[0].target = "mutated"
	again, err := recipe.PortableNamespaceProjectionPlan()
	if err != nil || again.records[0].target != "." {
		t.Fatalf("mount plan accessor aliases activation state: %#v, %v", again, err)
	}
}

func TestActivationProjectionMountPlanFailsClosedForUnsafeSemantics(t *testing.T) {
	fixture := newActivationFixture(t)
	bundle := prepareMaterializerFixture(t, fixture)
	plan, err := AssembleActivationWithIdentityReader(context.Background(), bundle.RuntimeRoot(), emptyActivationWritableRoot(t), os.LookupEnv, testActivationIdentityReader)
	if err != nil {
		t.Fatal(err)
	}
	recipe, ok := plan.EntrypointRecipe("fixture")
	if !ok || recipe.projection == nil || recipe.projection.plan == nil {
		t.Fatal("missing projection mount plan")
	}

	for _, mutate := range []func(*portableNamespaceProjectionPlan){
		func(plan *portableNamespaceProjectionPlan) { plan.records[0].target = "/host/secret" },
		func(plan *portableNamespaceProjectionPlan) {
			plan.records[0].operation = portableProjectionOperationReadOnlyBind
		},
		func(plan *portableNamespaceProjectionPlan) {
			plan.records[0].descriptorIndex = uint8(len(plan.descriptors))
		},
		func(plan *portableNamespaceProjectionPlan) { plan.records[len(plan.records)-1].order = 0 },
	} {
		candidate := recipe
		candidate.projection = &activationProjectionRecipe{plan: recipe.projection.plan.clone()}
		mutate(candidate.projection.plan)
		if _, err := candidate.PortableNamespaceProjectionPlan(); !errors.Is(err, ErrActivationInvalid) {
			t.Fatalf("unsafe mount plan error = %v", err)
		} else if strings.Contains(err.Error(), "secret") {
			t.Fatalf("unsafe mount plan diagnostic leaked target: %v", err)
		}
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
