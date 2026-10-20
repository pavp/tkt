package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"

	"github.com/pavp/tkt/internal/board"
	"github.com/pavp/tkt/internal/ticket"
)

// clientBuffer is how many events a client may fall behind before the daemon drops it; a dropped client
// reconnects and starts again from a snapshot.
const clientBuffer = 64

// writeTimeout bounds one event's write, so a peer that stops reading cannot hold its handler forever.
const writeTimeout = 10 * time.Second

// closeUnauthorized is the close code for a missing or wrong hello, as section 3 of the design says.
const closeUnauthorized websocket.StatusCode = 4401

type client struct {
	out chan []byte
	seq int
}

type helloMsg struct {
	Type     string `json:"type"`
	Token    string `json:"token"`
	Protocol int    `json:"protocol"`
}

// ws streams events to one client. Browsers cannot set headers on the upgrade, so the token comes in
// the first message; websocket.Accept refuses origins other than the daemon's own.
func (s *Server) ws(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	// A timer rather than a Read deadline: an expired deadline closes the socket without the 4401 code.
	silent := time.AfterFunc(s.opts.HelloTimeout, func() { conn.Close(closeUnauthorized, "no hello") })
	_, b, err := conn.Read(r.Context())
	silent.Stop()
	var h helloMsg
	if err != nil || json.Unmarshal(b, &h) != nil || h.Type != "hello" || h.Protocol != Protocol || !s.validToken(h.Token) {
		conn.Close(closeUnauthorized, "unauthorized")
		return
	}
	cl := s.join()
	defer s.leave(cl)
	ctx := conn.CloseRead(r.Context())
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.quit:
			conn.Close(websocket.StatusGoingAway, "daemon stopping")
			return
		case msg, ok := <-cl.out:
			if !ok {
				conn.Close(websocket.StatusPolicyViolation, "client too slow")
				return
			}
			wctx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// join registers a client and queues the current board for it under the same lock publish takes, so
// it can neither miss an event nor see one twice.
func (s *Server) join() *client {
	cl := &client{out: make(chan []byte, clientBuffer)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[cl] = struct{}{}
	if s.have {
		s.sendTo(cl, "snapshot", public(s.snap))
	}
	return cl
}

func (s *Server) leave(cl *client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.clients[cl]; ok {
		delete(s.clients, cl)
		close(cl.out)
	}
}

func (s *Server) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}

// publish sends an event to every client; callers hold s.mu.
func (s *Server) publish(kind string, data any) {
	for cl := range s.clients {
		s.sendTo(cl, kind, data)
	}
}

// sendTo queues one event with the client's next seq, dropping a client whose queue is full; callers
// hold s.mu.
func (s *Server) sendTo(cl *client, kind string, data any) {
	cl.seq++
	msg, err := json.Marshal(map[string]any{"seq": cl.seq, "type": kind, "ts": time.Now().UTC().Format(time.RFC3339), "data": data})
	if err != nil {
		return
	}
	select {
	case cl.out <- msg:
	default:
		delete(s.clients, cl)
		close(cl.out)
	}
}

// publishChanges tells clients how the board moved from old to snap: the first board, or one whose
// reviews, sprints, repositories or warnings changed, goes out whole; otherwise only changed tickets do.
// Callers hold s.mu.
func (s *Server) publishChanges(old board.Snapshot, had bool, snap board.Snapshot) {
	if !had || !sameBesidesTickets(old, snap) {
		s.publish("snapshot", public(snap))
		return
	}
	before := map[ticket.Key][]byte{}
	for _, t := range old.Tickets {
		before[t.Key], _ = json.Marshal(t)
	}
	seen := map[ticket.Key]bool{}
	for _, t := range snap.Tickets {
		seen[t.Key] = true
		if b, _ := json.Marshal(t); !bytes.Equal(b, before[t.Key]) {
			s.publish("ticket.updated", t)
		}
	}
	for _, t := range old.Tickets {
		if !seen[t.Key] {
			s.publish("ticket.removed", map[string]string{"key": string(t.Key)})
		}
	}
}

// sameBesidesTickets compares what a client shows apart from tickets; sync times, the development-panel
// cache and agent states change on their own and are left out.
func sameBesidesTickets(a, b board.Snapshot) bool {
	strip := func(s board.Snapshot) []byte {
		s.FetchedAt, s.Tickets, s.Panels, s.Agents = time.Time{}, nil, nil, nil
		out, _ := json.Marshal(s)
		return out
	}
	return bytes.Equal(strip(a), strip(b))
}
