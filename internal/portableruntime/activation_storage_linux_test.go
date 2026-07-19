//go:build linux

package portableruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
