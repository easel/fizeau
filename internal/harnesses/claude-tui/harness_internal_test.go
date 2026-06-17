package claudetui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/easel/fizeau/internal/harnesses"
)

func TestEmitTranscriptAndFinalSynthesizesFinalForIncompleteTranscript(t *testing.T) {
	dir := t.TempDir()
	transcriptPath := filepath.Join(dir, "transcript.jsonl")
	body := `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`
	if err := os.WriteFile(transcriptPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	events := make(chan harnesses.Event, 4)
	seq := int64(0)
	(&Harness{}).emitTranscriptAndFinal(context.Background(), &turnEnv{}, transcriptPath, events, &seq, time.Now(), nil)
	close(events)

	var finals []harnesses.FinalData
	for ev := range events {
		if ev.Type != harnesses.EventTypeFinal {
			continue
		}
		var final harnesses.FinalData
		if err := json.Unmarshal(ev.Data, &final); err != nil {
			t.Fatalf("unmarshal final: %v", err)
		}
		finals = append(finals, final)
	}
	if len(finals) != 1 {
		t.Fatalf("final events = %d, want 1", len(finals))
	}
	if finals[0].Status != "failed" {
		t.Fatalf("final status = %q, want failed", finals[0].Status)
	}
	if finals[0].Error == "" {
		t.Fatal("final error must describe the incomplete transcript")
	}
}
