package targetlint

import (
	"strings"
	"testing"
)

func TestFlagsTargetInRoutingContext(t *testing.T) {
	content := strings.Join([]string{
		"package fizeau",
		"func policyAlias(req request) string {",
		"	target := req.Policy",
		"	return target",
		"}",
		"// target-level routing is legacy vocabulary.",
	}, "\n") + "\n"

	findings := ScanContent("service_policies.go", []byte(content))
	if len(findings) != 3 {
		t.Fatalf("findings = %#v, want 3", findings)
	}
}

func TestAllowsHealthTargetAndErrorsIsTarget(t *testing.T) {
	content := strings.Join([]string{
		"package fizeau",
		"type HealthTarget struct{}",
		"func (s service) HealthCheck(target HealthTarget) error {",
		"	return nil",
		"}",
	}, "\n") + "\n"
	if findings := ScanContent("service_providers.go", []byte(content)); len(findings) != 0 {
		t.Fatalf("HealthTarget findings = %#v, want none", findings)
	}

	errorsContent := strings.Join([]string{
		"package routing",
		"func (e errType) Is(target error) bool {",
		"	switch target.(type) {",
		"	default:",
		"		return errors.Is(sentinel, target)",
		"	}",
		"}",
	}, "\n") + "\n"
	if findings := ScanContent("internal/routing/errors.go", []byte(errorsContent)); len(findings) != 0 {
		t.Fatalf("errors.Is findings = %#v, want none", findings)
	}
}

func TestAllowsPortableRuntimeTargetVocabularyOnlyInItsFacade(t *testing.T) {
	content := []byte("package fizeau\ntype PortableRuntimeRequest struct { TargetGOOS string `json:\"target_goos\"` }\n")
	if findings := ScanContent("service_portable_runtime.go", content); len(findings) != 0 {
		t.Fatalf("portable-runtime target findings = %#v, want none", findings)
	}
	if findings := ScanContent("service_routing.go", content); len(findings) != 1 {
		t.Fatalf("routing target findings = %#v, want one", findings)
	}
}

func TestRepositoryTargetVocabulary(t *testing.T) {
	findings, err := Scan(Options{Root: "../../.."})
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("unexpected target vocabulary findings: %#v", findings)
	}
}
