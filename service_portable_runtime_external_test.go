package fizeau_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	fizeau "github.com/easel/fizeau"
)

type portableRuntimePublicMock struct {
	fizeau.FizeauService
}

func (portableRuntimePublicMock) Continue(context.Context, fizeau.ServiceContinuationRequest) (<-chan fizeau.ServiceEvent, error) {
	return nil, fizeau.ErrContinuationUnsupported
}

func (portableRuntimePublicMock) PreparePortableRuntime(context.Context, fizeau.PortableRuntimeRequest) (*fizeau.PortableRuntimeBundle, error) {
	return &fizeau.PortableRuntimeBundle{}, nil
}

var _ fizeau.FizeauService = portableRuntimePublicMock{}

func TestPortableRuntimePublicCompileFixture(t *testing.T) {
	requestType := reflect.TypeOf(fizeau.PortableRuntimeRequest{})
	if requestType.PkgPath() != "github.com/easel/fizeau" || requestType.Name() != "PortableRuntimeRequest" {
		t.Fatalf("request identity = %s.%s", requestType.PkgPath(), requestType.Name())
	}
	assertExactPublicFields(t, requestType, []publicField{
		{"DestinationRoot", reflect.TypeFor[string]()},
		{"TargetGOOS", reflect.TypeFor[string]()},
		{"TargetGOARCH", reflect.TypeFor[string]()},
	})

	mountType := reflect.TypeOf(fizeau.PortableRuntimeMount{})
	if mountType.PkgPath() != "github.com/easel/fizeau" || mountType.Name() != "PortableRuntimeMount" {
		t.Fatalf("mount identity = %s.%s", mountType.PkgPath(), mountType.Name())
	}
	assertExactPublicFields(t, mountType, []publicField{
		{"Source", reflect.TypeFor[string]()},
		{"Target", reflect.TypeFor[string]()},
		{"ReadOnly", reflect.TypeFor[bool]()},
	})

	bundleType := reflect.TypeOf(fizeau.PortableRuntimeBundle{})
	if bundleType.PkgPath() != "github.com/easel/fizeau" || bundleType.Name() != "PortableRuntimeBundle" {
		t.Fatalf("bundle identity = %s.%s", bundleType.PkgPath(), bundleType.Name())
	}
	for index := 0; index < bundleType.NumField(); index++ {
		if bundleType.Field(index).IsExported() {
			t.Fatalf("portable bundle exposes field %s", bundleType.Field(index).Name)
		}
	}
	requiredMethods := map[string]bool{
		"RuntimeRoot": false, "Mounts": false, "EnvironmentNames": false, "Close": false,
	}
	pointerType := reflect.PointerTo(bundleType)
	for index := 0; index < pointerType.NumMethod(); index++ {
		method := pointerType.Method(index)
		if _, ok := requiredMethods[method.Name]; !ok {
			t.Fatalf("portable bundle exposes unexpected method %s", method.Name)
		}
		requiredMethods[method.Name] = true
	}
	for method, present := range requiredMethods {
		if !present {
			t.Fatalf("portable bundle lacks method %s", method)
		}
	}

	request := fizeau.PortableRuntimeRequest{
		DestinationRoot: "/home/account-bearing/private-runtime",
		TargetGOOS:      "linux",
		TargetGOARCH:    runtime.GOARCH,
	}
	encodedRequest, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestDiagnostics := fmt.Sprintf("%s %v %+v %#v", encodedRequest, request, request, request)
	if strings.Contains(requestDiagnostics, request.DestinationRoot) || strings.Contains(requestDiagnostics, "account-bearing") {
		t.Fatalf("request diagnostics expose DestinationRoot: %s", requestDiagnostics)
	}

	mount := fizeau.PortableRuntimeMount{Source: "/caller/runtime", Target: fizeau.PortableRuntimeGuestRoot(), ReadOnly: true}
	if runtime.GOOS == "linux" && mount.Target != "/opt/fizeau/runtime" {
		t.Fatalf("Linux guest root = %q", mount.Target)
	}
	if runtime.GOOS != "linux" && mount.Target != "" {
		t.Fatalf("unsupported guest root = %q", mount.Target)
	}

	zero := &fizeau.PortableRuntimeBundle{}
	if zero.RuntimeRoot() != "" || len(zero.Mounts()) != 0 || len(zero.EnvironmentNames()) != 0 {
		t.Fatal("zero bundle is not empty")
	}
	if err := zero.Close(); err != nil {
		t.Fatalf("zero Close() error = %v", err)
	}
	if err := zero.Close(); err != nil {
		t.Fatalf("repeated zero Close() error = %v", err)
	}
	var nilBundle *fizeau.PortableRuntimeBundle
	if nilBundle.RuntimeRoot() != "" || len(nilBundle.Mounts()) != 0 || len(nilBundle.EnvironmentNames()) != 0 || nilBundle.Close() != nil {
		t.Fatal("nil bundle receiver is not empty and closed")
	}

	mocked, err := (portableRuntimePublicMock{}).PreparePortableRuntime(context.Background(), request)
	if err != nil || mocked == nil || mocked.Close() != nil {
		t.Fatalf("external zero-bundle mock = (%v, %v)", mocked, err)
	}

	for got, want := range map[error]string{
		fizeau.ErrPortableRuntimeRequestInvalid:    "invalid portable runtime request",
		fizeau.ErrPortableRuntimeClosureIncomplete: "portable runtime closure incomplete",
		fizeau.ErrPortableRuntimeActivationInvalid: "portable runtime activation invalid",
		fizeau.ErrPortableRuntimeCleanupIncomplete: "portable runtime cleanup incomplete",
	} {
		if got.Error() != want {
			t.Fatalf("sentinel text = %q, want %q", got, want)
		}
	}

	activated, err := fizeau.NewFromPortableRuntime(fizeau.ServiceOptions{})
	if activated != nil || !errors.Is(err, fizeau.ErrPortableRuntimeActivationInvalid) {
		t.Fatalf("NewFromPortableRuntime() = (%v, %v)", activated, err)
	}
	activated, err = fizeau.NewFromPortableRuntime(fizeau.ServiceOptions{
		ConfigPath:    "/host/config.yaml",
		ServiceConfig: &stubServiceConfig{},
	})
	if activated != nil || !errors.Is(err, fizeau.ErrPortableRuntimeActivationInvalid) {
		t.Fatalf("NewFromPortableRuntime(host config) = (%v, %v)", activated, err)
	}
}

func TestPreparePortableRuntimeRejectsInvalidPublicRequest(t *testing.T) {
	svc, err := fizeau.New(fizeau.ServiceOptions{
		ServiceConfig:       &stubServiceConfig{},
		QuotaRefreshContext: canceledPublicRefreshContext(),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	bundle, err := svc.PreparePortableRuntime(context.Background(), fizeau.PortableRuntimeRequest{
		DestinationRoot: t.TempDir(),
		TargetGOOS:      "unsupported",
		TargetGOARCH:    runtime.GOARCH,
	})
	if bundle != nil || !errors.Is(err, fizeau.ErrPortableRuntimeRequestInvalid) {
		t.Fatalf("invalid preparation = (%v, %v)", bundle, err)
	}
}

type publicField struct {
	name   string
	typeOf reflect.Type
}

func assertExactPublicFields(t *testing.T, typ reflect.Type, want []publicField) {
	t.Helper()
	if typ.NumField() != len(want) {
		t.Fatalf("%s field count = %d, want %d", typ.Name(), typ.NumField(), len(want))
	}
	for index, expected := range want {
		field := typ.Field(index)
		if field.Name != expected.name || field.Type != expected.typeOf {
			t.Fatalf("%s field %d = %s %v, want %s %v", typ.Name(), index, field.Name, field.Type, expected.name, expected.typeOf)
		}
	}
}
