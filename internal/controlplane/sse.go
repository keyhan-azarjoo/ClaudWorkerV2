package controlplane

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// handleEvents streams events as Server-Sent Events. On connect it replays any retained events newer
// than the client's Last-Event-ID (so a reconnecting client misses nothing within the ring), then
// streams live events until the client disconnects.
//
// SSE is used because it needs no extra dependency (stdlib only, matching the zero-dep rule) and fits
// one-way server→client event push. A WebSocket transport can be added later as an alternative.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Subscribe BEFORE replaying so no event slips through the gap between replay and live stream.
	id, ch := s.bus.Subscribe(256)
	defer s.bus.Unsubscribe(id)

	// Replay retained events after the client's last seen id.
	after := lastEventID(r)
	var replayedTo uint64
	for _, ev := range s.bus.Recent(after) {
		writeEvent(w, ev)
		replayedTo = ev.Seq
	}
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			// Skip any live event already covered by replay (avoids duplicates on reconnect).
			if ev.Seq <= replayedTo {
				continue
			}
			writeEvent(w, ev)
			flusher.Flush()
		}
	}
}

// writeEvent serialises one event in SSE framing: id + event + data.
func writeEvent(w http.ResponseWriter, ev Event) {
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.Seq, ev.Type, b)
}

// lastEventID reads the SSE reconnect header (or ?last_event_id=) → the last seq the client saw.
func lastEventID(r *http.Request) uint64 {
	v := r.Header.Get("Last-Event-ID")
	if v == "" {
		v = r.URL.Query().Get("last_event_id")
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0
	}
	return n
}
