package fizeau

import (
	"encoding/json"

	"github.com/easel/fizeau/internal/routingquality"
)

// executeOverridePayload projects the root override record into the
// API-neutral coordinator payload. The coordinator adds the terminal outcome
// to the public event immediately before final delivery.
func executeOverridePayload(ovr *overrideContext, sessionID string) json.RawMessage {
	if ovr == nil {
		return nil
	}
	payload := ovr.payload
	payload.SessionID = sessionID
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return raw
}

func recordExecuteOverrideOutcome(ovr *overrideContext, status string) {
	if ovr == nil {
		return
	}
	ovr.emitted.Store(true)
	routingquality.StampOutcome(ovr.record, &routingquality.Outcome{Status: status})
}
