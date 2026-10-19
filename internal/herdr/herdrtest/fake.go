// Package herdrtest provides a fake Herdr socket server for tests.
package herdrtest

import (
	"bufio"
	"bytes"
	"encoding/json"
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
