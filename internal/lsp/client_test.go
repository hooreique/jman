package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"
)

func writeFrame(t *testing.T, c net.Conn, v any) {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fmt.Fprintf(c, "Content-Length: %d\r\n\r\n%s", len(b), b); e != nil {
		t.Error(e)
	}
}
func TestBidirectionalAndOutOfOrder(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	client := New(a, a, func(method string, p json.RawMessage) (any, error) { return "configured", nil })
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		r := bufio.NewReader(b)
		first, e := readMessage(r)
		if e != nil {
			t.Error(e)
			return
		}
		second, e := readMessage(r)
		if e != nil {
			t.Error(e)
			return
		}
		writeFrame(t, b, map[string]any{"jsonrpc": "2.0", "id": "server-1", "method": "workspace/configuration", "params": map[string]any{}})
		reply, e := readMessage(r)
		if e != nil || string(reply.Result) != "\"configured\"" {
			t.Errorf("reverse request: %+v %v", reply, e)
		}
		for _, request := range []Message{second, first} {
			writeFrame(t, b, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": request.Method})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	results := make(chan error, 2)
	for _, method := range []string{"first", "second"} {
		go func() {
			var out string
			e := client.Call(ctx, method, nil, &out)
			if e == nil && out != method {
				e = fmt.Errorf("mismatched response %s for %s", out, method)
			}
			results <- e
		}()
	}
	for i := 0; i < 2; i++ {
		if e := <-results; e != nil {
			t.Fatal(e)
		}
	}
	<-serverDone
}
func TestCancellationAndDisconnect(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	client := New(a, a, nil)
	go func() {
		r := bufio.NewReader(b)
		_, _ = readMessage(r)
		m, e := readMessage(r)
		if e != nil || m.Method != "$/cancelRequest" {
			t.Errorf("cancel: %s %v", m.Method, e)
		}
		b.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := client.Call(ctx, "slow", nil, nil); e != context.DeadlineExceeded {
		t.Fatalf("expected deadline, got %v", e)
	}
	select {
	case <-client.Done():
	case <-time.After(time.Second):
		t.Fatal("disconnect not propagated")
	}
}
