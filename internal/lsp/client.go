// Package lsp implements the bidirectional, cancellable subset of JSON-RPC used by JDTLS.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("LSP %d: %s", e.Code, e.Message) }

type Handler func(string, json.RawMessage) (any, error)
type Client struct {
	writer  io.Writer
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan Message
	seq     atomic.Int64
	done    chan struct{}
	err     error
	handler Handler
}

func New(r io.Reader, w io.Writer, h Handler) *Client {
	c := &Client{writer: w, pending: map[string]chan Message{}, done: make(chan struct{}), handler: h}
	go c.read(r)
	return c
}
func (c *Client) Done() <-chan struct{} { return c.done }
func (c *Client) send(v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, e = fmt.Fprintf(c.writer, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return e
}
func (c *Client) Notify(method string, params any) error {
	return c.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}
func (c *Client) Call(ctx context.Context, method string, params any, out any) error {
	id := strconv.FormatInt(c.seq.Add(1), 10)
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }()
	if e := c.send(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(id), "method": method, "params": params}); e != nil {
		return e
	}
	select {
	case m := <-ch:
		if m.Error != nil {
			return m.Error
		}
		if out != nil {
			return json.Unmarshal(m.Result, out)
		}
		return nil
	case <-ctx.Done():
		_ = c.Notify("$/cancelRequest", map[string]any{"id": json.RawMessage(id)})
		return ctx.Err()
	case <-c.done:
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.err
	}
}
func readMessage(r *bufio.Reader) (Message, error) {
	var m Message
	size := -1
	for {
		line, e := r.ReadString('\n')
		if e != nil {
			return m, e
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(k, "Content-Length") {
			size, e = strconv.Atoi(strings.TrimSpace(v))
			if e != nil {
				return m, e
			}
		}
	}
	if size < 0 || size > 64<<20 {
		return m, fmt.Errorf("invalid LSP content length: %d", size)
	}
	b := make([]byte, size)
	if _, e := io.ReadFull(r, b); e != nil {
		return m, e
	}
	e := json.Unmarshal(b, &m)
	return m, e
}
func (c *Client) read(r io.Reader) {
	reader := bufio.NewReader(r)
	for {
		m, e := readMessage(reader)
		if e != nil {
			c.mu.Lock()
			c.err = e
			c.mu.Unlock()
			close(c.done)
			return
		}
		if m.Method == "" {
			c.mu.Lock()
			ch := c.pending[string(m.ID)]
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		// Notifications are processed in wire order; handlers must never make a blocking RPC call.
		var result any
		var err error
		if c.handler != nil {
			result, err = c.handler(m.Method, m.Params)
		}
		if len(m.ID) > 0 {
			reply := map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": result}
			if err != nil {
				delete(reply, "result")
				reply["error"] = &RPCError{-32603, err.Error()}
			}
			if e = c.send(reply); e != nil {
				c.mu.Lock()
				c.err = e
				c.mu.Unlock()
				close(c.done)
				return
			}
		}
	}
}
