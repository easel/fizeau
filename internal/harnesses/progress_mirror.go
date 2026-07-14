package harnesses

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/easel/fizeau/internal/sessionlog"
)

func OpenProgressLog(sessionLogDir, sessionID, prefix string) (*os.File, error) {
	if sessionLogDir == "" {
		return nil, nil
	}
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return sessionlog.OpenAppend(sessionLogDir, sessionID)
}

func MirrorEvents(dst chan<- Event, log io.Writer, ctx context.Context) (chan Event, <-chan struct{}) {
	mid := make(chan Event, cap(dst))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range mid {
			WriteProgressEvent(log, ev)
			select {
			case dst <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return mid, done
}

func WriteProgressEvent(log io.Writer, ev Event) {
	if log == nil {
		return
	}
	if data, err := json.Marshal(ev); err == nil {
		_, _ = log.Write(data)
		_, _ = log.Write([]byte("\n"))
	}
}
