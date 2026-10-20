// Package herdrtest provides a fake Herdr socket server for tests.
package herdrtest

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// FakeServer answers Herdr socket requests from a method → result table and records the requests.
type FakeServer struct {
	Path     string
	mu       sync.Mutex
	Requests []map[string]any
	Results  map[string]string
	Errors   map[string]string
	// watchers are the connections that subscribed to events; Emit writes to them.
	watchers []net.Conn
}

func Start(t *testing.T) *FakeServer {
	t.Helper()
	// macOS limits Unix socket paths to 104 bytes, and t.TempDir() is often longer.
	dir, err := os.MkdirTemp("/tmp", "hd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	f := &FakeServer{Path: filepath.Join(dir, "s"), Results: map[string]string{}, Errors: map[string]string{}}
	ln, err := net.Listen("unix", f.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *FakeServer) serve(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req map[string]any
	_ = json.Unmarshal(line, &req)
	f.mu.Lock()
	f.Requests = append(f.Requests, req)
	method, _ := req["method"].(string)
	result, ok := f.Results[method]
	errMsg, isErr := f.Errors[method]
	f.mu.Unlock()
	id, _ := req["id"].(string)
	if method == "events.subscribe" && !isErr {
		_, _ = conn.Write([]byte(`{"id":"` + id + `","result":{"type":"subscription_started"}}` + "\n"))
		f.mu.Lock()
		f.watchers = append(f.watchers, conn)
		f.mu.Unlock()
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	switch {
	case isErr:
		_, _ = conn.Write([]byte(`{"id":"` + id + `","error":{"code":"bad_request","message":"` + errMsg + `"}}` + "\n"))
	case ok:
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(result)); err != nil {
			compact.WriteString(`{"type":"invalid_fake_result"}`)
		}
		_, _ = conn.Write([]byte(`{"id":"` + id + `","result":` + compact.String() + "}\n"))
	default:
		_, _ = conn.Write([]byte(`{"id":"` + id + `","result":{"type":"ok"}}` + "\n"))
	}
}

// SetResult changes a method's answer while the server runs.
func (f *FakeServer) SetResult(method, result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Results[method] = result
}

// Emit sends one event line to every subscribed connection.
func (f *FakeServer) Emit(line string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.watchers {
		_, _ = c.Write([]byte(line + "\n"))
	}
}

// Watchers counts the connections subscribed to events, closed ones included.
func (f *FakeServer) Watchers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.watchers)
}

// DropWatchers closes every subscribed connection, as a Herdr restart would.
func (f *FakeServer) DropWatchers() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.watchers {
		c.Close()
	}
	f.watchers = nil
}

func (f *FakeServer) Last(method string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.Requests) - 1; i >= 0; i-- {
		if f.Requests[i]["method"] == method {
			p, _ := f.Requests[i]["params"].(map[string]any)
			return p
		}
	}
	return nil
}
