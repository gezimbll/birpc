// Copyright 2010 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cgrates/birpc"
	"github.com/cgrates/birpc/context"
)

type Args struct {
	A, B int
}

type Reply struct {
	C int
}

type Arith int

type ArithAddResp struct {
	Id     any   `json:"id"`
	Result Reply `json:"result"`
	Error  any   `json:"error"`
}

func (t *Arith) Add(_ *context.Context, args *Args, reply *Reply) error {
	reply.C = args.A + args.B
	return nil
}

func (t *Arith) Mul(_ *context.Context, args *Args, reply *Reply) error {
	reply.C = args.A * args.B
	return nil
}

func (t *Arith) Div(_ *context.Context, args *Args, reply *Reply) error {
	if args.B == 0 {
		return errors.New("divide by zero")
	}
	reply.C = args.A / args.B
	return nil
}

func (t *Arith) Error(_ *context.Context, args *Args, reply *Reply) error {
	panic("ERROR")
}

type BuiltinTypes struct{}

func (BuiltinTypes) Map(_ *context.Context, i int, reply *map[int]int) error {
	(*reply)[i] = i
	return nil
}

func (BuiltinTypes) Slice(_ *context.Context, i int, reply *[]int) error {
	*reply = append(*reply, i)
	return nil
}

func (BuiltinTypes) Array(_ *context.Context, i int, reply *[1]int) error {
	(*reply)[0] = i
	return nil
}

func init() {
	birpc.Register(new(Arith))
	birpc.Register(BuiltinTypes{})
}

func TestServerNoParams(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	go ServeConn(srv)
	dec := json.NewDecoder(cli)

	fmt.Fprintf(cli, `{"method": "Arith.Add", "id": "123"}`)
	var resp ArithAddResp
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("Decode after no params: %s", err)
	}
	if resp.Error == nil {
		t.Fatalf("Expected error, got nil")
	}
}

func TestServerEmptyMessage(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	go ServeConn(srv)
	dec := json.NewDecoder(cli)

	fmt.Fprintf(cli, "{}")
	var resp ArithAddResp
	if err := dec.Decode(&resp); err != nil {
		t.Fatalf("Decode after empty: %s", err)
	}
	if resp.Error == nil {
		t.Fatalf("Expected error, got nil")
	}
}

func TestServer(t *testing.T) {
	cli, srv := net.Pipe()
	defer cli.Close()
	go ServeConn(srv)
	dec := json.NewDecoder(cli)

	// Send hand-coded requests to server, parse responses.
	for i := 0; i < 10; i++ {
		fmt.Fprintf(cli, `{"method": "Arith.Add", "id": "\u%04d", "params": [{"A": %d, "B": %d}]}`, i, i, i+1)
		var resp ArithAddResp
		err := dec.Decode(&resp)
		if err != nil {
			t.Fatalf("Decode: %s", err)
		}
		if resp.Error != nil {
			t.Fatalf("resp.Error: %s", resp.Error)
		}
		if resp.Id.(string) != string(rune(i)) {
			t.Fatalf("resp: bad id %q want %q", resp.Id.(string), string(rune(i)))
		}
		if resp.Result.C != 2*i+1 {
			t.Fatalf("resp: bad result: %d+%d=%d", i, i+1, resp.Result.C)
		}
	}
}

func TestClient(t *testing.T) {
	// Assume server is okay (TestServer is above).
	// Test client against server.
	cli, srv := net.Pipe()
	go ServeConn(srv)

	client := NewClient(cli)
	defer client.Close()

	// Synchronous calls
	ctx := context.Background()
	args := &Args{7, 8}
	reply := new(Reply)
	err := client.Call(ctx, "Arith.Add", args, reply)
	if err != nil {
		t.Errorf("Add: expected no error but got string %q", err.Error())
	}
	if reply.C != args.A+args.B {
		t.Errorf("Add: got %d expected %d", reply.C, args.A+args.B)
	}

	args = &Args{7, 8}
	reply = new(Reply)
	err = client.Call(ctx, "Arith.Mul", args, reply)
	if err != nil {
		t.Errorf("Mul: expected no error but got string %q", err.Error())
	}
	if reply.C != args.A*args.B {
		t.Errorf("Mul: got %d expected %d", reply.C, args.A*args.B)
	}

	// Out of order.
	args = &Args{7, 8}
	mulReply := new(Reply)
	mulCall := client.Go("Arith.Mul", args, mulReply, nil)
	addReply := new(Reply)
	addCall := client.Go("Arith.Add", args, addReply, nil)

	addCall = <-addCall.Done
	if addCall.Error != nil {
		t.Errorf("Add: expected no error but got string %q", addCall.Error.Error())
	}
	if addReply.C != args.A+args.B {
		t.Errorf("Add: got %d expected %d", addReply.C, args.A+args.B)
	}

	mulCall = <-mulCall.Done
	if mulCall.Error != nil {
		t.Errorf("Mul: expected no error but got string %q", mulCall.Error.Error())
	}
	if mulReply.C != args.A*args.B {
		t.Errorf("Mul: got %d expected %d", mulReply.C, args.A*args.B)
	}

	// Error test
	args = &Args{7, 0}
	reply = new(Reply)
	err = client.Call(ctx, "Arith.Div", args, reply)
	// expect an error: zero divide
	if err == nil {
		t.Error("Div: expected error")
	} else if err.Error() != "divide by zero" {
		t.Error("Div: expected divide by zero error; got", err)
	}
}

func TestBuiltinTypes(t *testing.T) {
	cli, srv := net.Pipe()
	go ServeConn(srv)

	client := NewClient(cli)
	defer client.Close()

	// Map
	ctx := context.Background()
	arg := 7
	replyMap := map[int]int{}
	err := client.Call(ctx, "BuiltinTypes.Map", arg, &replyMap)
	if err != nil {
		t.Errorf("Map: expected no error but got string %q", err.Error())
	}
	if replyMap[arg] != arg {
		t.Errorf("Map: expected %d got %d", arg, replyMap[arg])
	}

	// Slice
	replySlice := []int{}
	err = client.Call(ctx, "BuiltinTypes.Slice", arg, &replySlice)
	if err != nil {
		t.Errorf("Slice: expected no error but got string %q", err.Error())
	}
	if e := []int{arg}; !reflect.DeepEqual(replySlice, e) {
		t.Errorf("Slice: expected %v got %v", e, replySlice)
	}

	// Array
	replyArray := [1]int{}
	err = client.Call(ctx, "BuiltinTypes.Array", arg, &replyArray)
	if err != nil {
		t.Errorf("Array: expected no error but got string %q", err.Error())
	}
	if e := [1]int{arg}; !reflect.DeepEqual(replyArray, e) {
		t.Errorf("Array: expected %v got %v", e, replyArray)
	}
}

func TestMalformedInput(t *testing.T) {
	cli, srv := net.Pipe()
	go cli.Write([]byte(`{id:1}`)) // invalid json
	ServeConn(srv)                 // must return, not loop
}

func TestMalformedOutput(t *testing.T) {
	cli, srv := net.Pipe()
	go srv.Write([]byte(`{"id":0,"result":null,"error":null}`))
	go io.ReadAll(srv)

	client := NewClient(cli)
	defer client.Close()

	args := &Args{7, 8}
	reply := new(Reply)
	err := client.Call(context.Background(), "Arith.Add", args, reply)
	if err == nil {
		t.Error("expected error")
	}
}

func TestServerErrorHasNullResult(t *testing.T) {
	var out bytes.Buffer
	sc := NewServerCodec(struct {
		io.Reader
		io.Writer
		io.Closer
	}{
		Reader: strings.NewReader(`{"method": "Arith.Add", "id": "123", "params": []}`),
		Writer: &out,
		Closer: io.NopCloser(nil),
	})
	r := new(birpc.Request)
	if err := sc.ReadRequestHeader(r); err != nil {
		t.Fatal(err)
	}
	const valueText = "the value we don't want to see"
	const errorText = "some error"
	err := sc.WriteResponse(&birpc.Response{
		Seq:   1,
		Error: errorText,
	}, valueText)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), errorText) {
		t.Fatalf("Response didn't contain expected error %q: %s", errorText, &out)
	}
	if strings.Contains(out.String(), valueText) {
		t.Errorf("Response contains both an error and value: %s", &out)
	}
}

func TestUnexpectedError(t *testing.T) {
	cli, srv := myPipe()
	go cli.PipeWriter.CloseWithError(errors.New("unexpected error!")) // reader will get this error
	ServeConn(srv)                                                    // must return, not loop
}

// Copied from package net.
func myPipe() (*pipe, *pipe) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	return &pipe{r1, w2}, &pipe{r2, w1}
}

type pipe struct {
	*io.PipeReader
	*io.PipeWriter
}

type pipeAddr int

func (pipeAddr) Network() string {
	return "pipe"
}

func (pipeAddr) String() string {
	return "pipe"
}

func (p *pipe) Close() error {
	err := p.PipeReader.Close()
	err1 := p.PipeWriter.Close()
	if err == nil {
		err = err1
	}
	return err
}

func (p *pipe) LocalAddr() net.Addr {
	return pipeAddr(0)
}

func (p *pipe) RemoteAddr() net.Addr {
	return pipeAddr(0)
}

func (p *pipe) SetTimeout(nsec int64) error {
	return errors.New("net.Pipe does not support timeouts")
}

func (p *pipe) SetReadTimeout(nsec int64) error {
	return errors.New("net.Pipe does not support timeouts")
}

func (p *pipe) SetWriteTimeout(nsec int64) error {
	return errors.New("net.Pipe does not support timeouts")
}

type Context struct {
	started chan struct{}
	done    chan struct{}
}

func (t *Context) Wait(ctx *context.Context, s string, reply *int) error {
	close(t.started)
	<-ctx.Done()
	close(t.done)
	return nil
}

type HTTPReadWriteCloser struct {
	In  io.Reader
	Out io.Writer
}

func (c *HTTPReadWriteCloser) Read(p []byte) (n int, err error)  { return c.In.Read(p) }
func (c *HTTPReadWriteCloser) Write(d []byte) (n int, err error) { return c.Out.Write(d) }
func (c *HTTPReadWriteCloser) Close() error                      { return nil }

type JsonServer struct {
	srv *birpc.Server
}

func (server *JsonServer) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	rwc := &HTTPReadWriteCloser{
		In:  req.Body,
		Out: w,
	}
	codec := NewServerCodec(rwc)
	server.srv.ServeRequestContext(&context.Context{Context: req.Context()}, codec)
}

func TestContextCodec(t *testing.T) {
	svc := &Context{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}

	srv := birpc.NewServer()
	srv.Register(svc)
	handler := &JsonServer{srv}
	ts := httptest.NewServer(handler)
	defer ts.Close()

	wait := func(desc string, c chan struct{}) {
		t.Helper()
		select {
		case <-c:
			return
		case <-time.After(5 * time.Second):
			t.Fatal("Failed to wait for", desc)
		}
	}

	b, err := json.Marshal(&struct {
		Method string `json:"method"`
		Params [1]any `json:"params"`
		Id     uint64 `json:"id"`
	}{
		Method: "Context.Wait",
		Params: [1]any{""},
		Id:     1234,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		var req *http.Request
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, ts.URL, bytes.NewBuffer(b))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		_, err = http.DefaultClient.Do(req)
	}()

	wait("server side to be called", svc.started)
	cancel()
	wait("client side to be done after cancel", done)
	wait("server side to be done after cancel", svc.done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected to fail due to context cancellation: %v", err)
	}
}
