package fizeau_test

import (
	"reflect"
	"testing"

	fizeau "github.com/easel/fizeau"
)

type compatibleServiceConfigSource struct{}

func (compatibleServiceConfigSource) ProviderNames() []string {
	return []string{"router"}
}

var _ fizeau.ServiceConfigSource = compatibleServiceConfigSource{}

func TestServiceConfigSourcePublicCompatibility(t *testing.T) {
	source := fizeau.ServiceConfigSource(compatibleServiceConfigSource{})
	if got, want := source.ProviderNames(), []string{"router"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ProviderNames() = %v, want %v", got, want)
	}
}
