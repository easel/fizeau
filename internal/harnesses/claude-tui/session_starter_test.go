package claudetui

import (
	"os"
	"strings"
	"testing"
)

func TestStartPTYSessionUsesOneCommonInvocation(t *testing.T) {
	data, err := os.ReadFile("harness.go")
	if err != nil {
		t.Fatalf("read harness.go: %v", err)
	}
	source := string(data)
	if !strings.Contains(source, "starter := harnesses.PTYSessionStarter(session.Start)") {
		t.Fatal("startPTYSession no longer selects session.Start as its default starter")
	}
	const commonCall = "return starter(ctx, command, args, workdir, env, size, opts...)"
	if got := strings.Count(source, commonCall); got != 1 {
		t.Fatalf("common starter invocation count = %d, want exactly 1", got)
	}
}
