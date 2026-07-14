package fizeau

import (
	"time"

	"github.com/easel/fizeau/internal/harnesses"
	"github.com/easel/fizeau/internal/routingquality"
)

// wrapExecuteWithHub wraps the inner out channel so that every event emitted
// by runExecute is also broadcast to TailSessionLog subscribers.
func wrapExecuteWithHub(fanout executeEventFanout, sessionID string, outer chan ServiceEvent, ovr *overrideContext, meta map[string]string) chan ServiceEvent {
	inner := make(chan ServiceEvent, 64)
	go func() {
		defer close(outer)
		var lastFinal ServiceEvent
		for ev := range inner {
			if ev.Type == harnesses.EventTypeFinal && ovr != nil && !ovr.emitted.Load() {
				if overrideEv, payload, ok := makeOverrideEvent(ovr, sessionID, ev, meta); ok {
					ovr.emitted.Store(true)
					if payload.Outcome != nil {
						routingquality.StampOutcome(ovr.record, &routingquality.Outcome{Status: payload.Outcome.Status})
					}
					outer <- overrideEv
					fanout.BroadcastEvent(sessionID, overrideEv)
				}
			}
			if ev.Type == harnesses.EventTypeFinal {
				// A terminal fact is never best-effort. Backpressure may delay
				// delivery, but it must not let the public stream close without
				// the same final event already committed to the session log.
				outer <- ev
			} else {
				select {
				case outer <- ev:
				case <-time.After(5 * time.Second):
				}
			}
			fanout.BroadcastEvent(sessionID, ev)
			if ev.Type == harnesses.EventTypeFinal {
				lastFinal = ev
			}
		}
		fanout.CloseSession(sessionID, lastFinal)
	}()
	return inner
}
