package herdrcore

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/pavp/tkt/internal/herdrport"
)

// hcKinds names the port event each hc fact stands for; status is translated on its own, and facts such
// as turn, title, labels or size have no port event.
var hcKinds = map[string]string{
	"created":  "pane.created",
	"removed":  "pane.closed",
	"finished": "pane.exited",
	"lost":     "pane.exited",
}

type snapshot struct {
	Terminals []terminal `json:"terminals"`
}

type line struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	snapshot
}

type fact struct {
	Terminal string `json:"terminal"`
	Type     string `json:"type"`
	Data     struct {
		Status *struct {
			State string `json:"state"`
		} `json:"status"`
	} `json:"data"`
}

// Watch follows `hc events` and calls on for each event subs ask for, until ctx ends, returning ctx's
// error, or hc stops. Agent events come from status facts: a terminal's first status detects its agent.
func (c *Client) Watch(ctx context.Context, subs []herdrport.Subscription, on func(herdrport.Event)) error {
	args := []string{"events", "--json"}
	if p := onePane(subs); p != "" {
		args = append(args, "--terminal", p)
	}
	rc, err := c.hc.Stream(ctx, args...)
	if err != nil {
		return fmt.Errorf("hc events: %w", err)
	}
	w := watcher{subs: subs, on: on}
	r := bufio.NewReader(rc)
	for {
		b, rerr := r.ReadBytes('\n')
		if len(b) > 0 {
			w.handle(b)
		}
		if rerr != nil {
			cerr := rc.Close()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errors.Join(errors.New("hc events: stream ended"), cerr)
		}
	}
}

// onePane returns the pane every subscription names, so hc sends only its facts.
func onePane(subs []herdrport.Subscription) string {
	if len(subs) == 0 {
		return ""
	}
	p := subs[0].PaneID
	for _, s := range subs {
		if s.PaneID != p {
			return ""
		}
	}
	return p
}

type watcher struct {
	subs []herdrport.Subscription
	on   func(herdrport.Event)
	// status holds each known terminal's status, "" for none; nil until the first snapshot.
	status map[string]string
}

func (w *watcher) handle(b []byte) {
	var l line
	if json.Unmarshal(b, &l) != nil {
		return
	}
	switch l.Method {
	case "":
		w.resync(l.Terminals, w.status != nil)
	case "missed":
		var s snapshot
		if json.Unmarshal(l.Params, &s) == nil {
			w.resync(s.Terminals, true)
		}
	case "event":
		var f fact
		if json.Unmarshal(l.Params, &f) != nil {
			return
		}
		if f.Type == "status" {
			state := ""
			if f.Data.Status != nil {
				state = f.Data.Status.State
			}
			w.setStatus(f.Terminal, state, true)
			return
		}
		switch f.Type {
		case "created":
			if _, ok := w.status[f.Terminal]; !ok && w.status != nil {
				w.status[f.Terminal] = ""
			}
		case "removed":
			delete(w.status, f.Terminal)
		}
		w.emit(hcKinds[f.Type], f.Terminal)
	}
}

// resync replaces the known terminals with a snapshot, reporting what changed when report is set.
func (w *watcher) resync(ts []terminal, report bool) {
	old := w.status
	w.status = map[string]string{}
	for _, t := range ts {
		prev, known := old[t.Address]
		w.status[t.Address] = prev
		if report && !known {
			w.emit("pane.created", t.Address)
		}
		state := ""
		if t.Status != nil {
			state = t.Status.State
		}
		w.setStatus(t.Address, state, report && state != prev)
	}
	for _, t := range ts {
		delete(old, t.Address)
	}
	for _, addr := range slices.Sorted(maps.Keys(old)) {
		if report {
			w.emit("pane.closed", addr)
		}
	}
}

func (w *watcher) setStatus(addr, state string, report bool) {
	if w.status == nil {
		w.status = map[string]string{}
	}
	prev := w.status[addr]
	w.status[addr] = state
	if !report {
		return
	}
	if prev == "" && state != "" {
		w.emit("pane.agent_detected", addr)
	}
	w.emit("pane.agent_status_changed", addr)
}

func (w *watcher) emit(kind, pane string) {
	if kind == "" {
		return
	}
	for _, s := range w.subs {
		if s.Type == kind && (s.PaneID == "" || s.PaneID == pane) {
			data, _ := json.Marshal(map[string]string{"pane_id": pane})
			w.on(herdrport.Event{Kind: kind, Data: data})
			return
		}
	}
}
