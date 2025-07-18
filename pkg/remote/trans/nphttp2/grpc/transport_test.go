/*
 *
 * Copyright 2014 gRPC authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * This file may have been modified by CloudWeGo authors. All CloudWeGo
 * Modifications are Copyright 2021 CloudWeGo Authors.
 */

package grpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"

	"github.com/cloudwego/netpoll"

	"github.com/cloudwego/kitex/internal/test"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/codes"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/grpc/grpcframe"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/grpc/testutils"
	"github.com/cloudwego/kitex/pkg/remote/trans/nphttp2/status"
	"github.com/cloudwego/kitex/pkg/utils"
)

type server struct {
	lis        netpoll.Listener
	eventLoop  netpoll.EventLoop
	port       string
	startedErr chan error // error (or nil) with server start value
	mu         sync.Mutex
	conns      map[ServerTransport]bool
	h          *testStreamHandler
	ready      chan struct{}
	hdlWG      sync.WaitGroup
	transWG    sync.WaitGroup

	srvReady chan struct{}
}

var (
	expectedRequest            = []byte("ping")
	expectedResponse           = []byte("pong")
	expectedRequestLarge       = make([]byte, initialWindowSize*2)
	expectedResponseLarge      = make([]byte, initialWindowSize*2)
	expectedInvalidHeaderField = "invalid/content-type"
	errSelfCloseForTest        = errors.New("self-close in test")
)

func init() {
	expectedRequestLarge[0] = 'g'
	expectedRequestLarge[len(expectedRequestLarge)-1] = 'r'
	expectedResponseLarge[0] = 'p'
	expectedResponseLarge[len(expectedResponseLarge)-1] = 'c'
}

type testStreamHandler struct {
	t           *http2Server
	srv         *server
	notify      chan struct{}
	getNotified chan struct{}
}

type hType int

const (
	normal hType = iota
	suspended
	notifyCall
	misbehaved
	encodingRequiredStatus
	invalidHeaderField
	delayRead
	pingpong

	gracefulShutdown
	cancel
)

func (h *testStreamHandler) handleStreamAndNotify(s *Stream) {
	if h.notify == nil {
		return
	}
	go func() {
		select {
		case <-h.notify:
		default:
			close(h.notify)
		}
	}()
}

func (h *testStreamHandler) handleStream(t *testing.T, s *Stream) {
	req := expectedRequest
	resp := expectedResponse
	if s.Method() == "foo.Large" {
		req = expectedRequestLarge
		resp = expectedResponseLarge
	}
	p := make([]byte, len(req))
	_, err := s.Read(p)
	if err != nil {
		return
	}
	if !bytes.Equal(p, req) {
		t.Errorf("handleStream got %v, want %v", p, req)
		h.t.WriteStatus(s, status.New(codes.Internal, "panic"))
		return
	}
	// send a response back to the client.
	h.t.Write(s, nil, resp, &Options{})
	// send the trailer to end the stream.
	h.t.WriteStatus(s, status.New(codes.OK, ""))
}

func (h *testStreamHandler) handleStreamPingPong(t *testing.T, s *Stream) {
	header := make([]byte, 5)
	for {
		if _, err := s.Read(header); err != nil {
			if err == io.EOF {
				h.t.WriteStatus(s, status.New(codes.OK, ""))
				return
			}
			t.Errorf("Error on server while reading data header: %v", err)
			h.t.WriteStatus(s, status.New(codes.Internal, "panic"))
			return
		}
		sz := binary.BigEndian.Uint32(header[1:])
		msg := make([]byte, int(sz))
		if _, err := s.Read(msg); err != nil {
			t.Errorf("Error on server while reading message: %v", err)
			h.t.WriteStatus(s, status.New(codes.Internal, "panic"))
			return
		}
		buf := make([]byte, sz+5)
		buf[0] = byte(0)
		binary.BigEndian.PutUint32(buf[1:], uint32(sz))
		copy(buf[5:], msg)
		h.t.Write(s, nil, buf, &Options{})
	}
}

func (h *testStreamHandler) handleStreamMisbehave(t *testing.T, s *Stream) {
	conn, ok := s.st.(*http2Server)
	if !ok {
		t.Errorf("Failed to convert %v to *http2Server", s.st)
		h.t.WriteStatus(s, status.New(codes.Internal, ""))
		return
	}
	var sent int
	p := make([]byte, http2MaxFrameLen)
	for sent < int(initialWindowSize) {
		n := int(initialWindowSize) - sent
		// The last message may be smaller than http2MaxFrameLen
		if n <= http2MaxFrameLen {
			if s.Method() == "foo.Connection" {
				// Violate connection level flow control window of client but do not
				// violate any stream level windows.
				p = make([]byte, n)
			} else {
				// Violate stream level flow control window of client.
				p = make([]byte, n+1)
			}
		}
		conn.controlBuf.put(&dataFrame{
			streamID: s.id,
			h:        nil,
			d:        p,
		})
		sent += len(p)
	}
}

func (h *testStreamHandler) handleStreamEncodingRequiredStatus(t *testing.T, s *Stream) {
	// raw newline is not accepted by http2 framer so it must be encoded.
	h.t.WriteStatus(s, encodingTestStatus)
}

func (h *testStreamHandler) handleStreamInvalidHeaderField(t *testing.T, s *Stream) {
	headerFields := []hpack.HeaderField{}
	headerFields = append(headerFields, hpack.HeaderField{Name: "content-type", Value: expectedInvalidHeaderField})
	h.t.controlBuf.put(&headerFrame{
		streamID:  s.id,
		hf:        headerFields,
		endStream: false,
	})
}

// handleStreamDelayRead delays reads so that the other side has to halt on
// stream-level flow control.
// This handler assumes dynamic flow control is turned off and assumes window
// sizes to be set to defaultWindowSize.
func (h *testStreamHandler) handleStreamDelayRead(t *testing.T, s *Stream) {
	req := expectedRequest
	resp := expectedResponse
	if s.Method() == "foo.Large" {
		req = expectedRequestLarge
		resp = expectedResponseLarge
	}
	var (
		mu    sync.Mutex
		total int
	)
	s.wq.replenish = func(n int) {
		mu.Lock()
		total += n
		mu.Unlock()
		s.wq.realReplenish(n)
	}
	getTotal := func() int {
		mu.Lock()
		defer mu.Unlock()
		return total
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			// Prevent goroutine from leaking.
			case <-done:
				return
			default:
			}
			if getTotal() == int(defaultWindowSize) {
				// Signal the client to start reading and
				// thereby send window update.
				close(h.notify)
				return
			}
			runtime.Gosched()
		}
	}()
	p := make([]byte, len(req))

	// Let the other side run out of stream-level window before
	// starting to read and thereby sending a window update.
	timer := time.NewTimer(time.Second * 10)
	select {
	case <-h.getNotified:
		timer.Stop()
	case <-timer.C:
		t.Errorf("Server timed-out.")
		return
	}
	_, err := s.Read(p)
	if err != nil {
		t.Errorf("s.Read(_) = _, %v, want _, <nil>", err)
		return
	}

	if !bytes.Equal(p, req) {
		t.Errorf("handleStream got %v, want %v", p, req)
		return
	}
	// This write will cause server to run out of stream level,
	// flow control and the other side won't send a window update
	// until that happens.
	if err := h.t.Write(s, nil, resp, &Options{}); err != nil {
		t.Errorf("server write got %v, want <nil>", err)
		return
	}
	// Read one more time to ensure that everything remains fine and
	// that the goroutine, that we launched earlier to signal client
	// to read, gets enough time to process.
	_, err = s.Read(p)
	if err != nil {
		t.Errorf("s.Read(_) = _, %v, want _, nil", err)
		return
	}
	// send the trailer to end the stream.
	if err := h.t.WriteStatus(s, status.New(codes.OK, "")); err != nil {
		t.Errorf("server WriteStatus got %v, want <nil>", err)
		return
	}
}

func (h *testStreamHandler) gracefulShutdown(t *testing.T, s *Stream) {
	t.Log("run graceful shutdown")
	close(h.srv.srvReady)
	msg := make([]byte, 5)
	num, err := s.Read(msg)
	test.Assert(t, err == nil, err)
	test.Assert(t, num == 5, num)
	test.Assert(t, string(msg) == "hello", string(msg))
	err = h.t.Write(s, nil, msg, &Options{})
	test.Assert(t, err == nil, err)
	_, err = s.Read(msg)
	test.Assert(t, err != nil, err)
	test.Assert(t, strings.Contains(err.Error(), gracefulShutdownMsg), err)
	st, ok := status.FromError(err)
	test.Assert(t, ok, err)
	test.Assert(t, st.Code() == codes.Unavailable, st)
}

func (h *testStreamHandler) handleStreamCancel(t *testing.T, s *Stream) {
	header := make([]byte, 5)
	_, err := s.Read(header)
	test.Assert(t, err != nil, err)
	st, ok := status.FromError(err)
	test.Assert(t, ok)
	test.Assert(t, st.Code() == codes.Canceled, st.Code())
	test.Assert(t, strings.Contains(st.Message(), "transport: RSTStream Frame received with error code"), st.Message())
	close(h.srv.srvReady)
}

// start starts server. Other goroutines should block on s.readyChan for further operations.
func (s *server) start(t *testing.T, port int, serverConfig *ServerConfig, ht hType) {
	// 创建 listener
	var err error
	if port == 0 {
		s.lis, err = netpoll.CreateListener("tcp", "localhost:0")
	} else {
		s.lis, err = netpoll.CreateListener("tcp", "localhost:"+strconv.Itoa(port))
	}
	if err != nil {
		s.startedErr <- fmt.Errorf("failed to create netpoll listener: %v", err)
		return
	}
	_, p, err := net.SplitHostPort(s.lis.Addr().String())
	if err != nil {
		s.startedErr <- fmt.Errorf("failed to parse listener address: %v", err)
		return
	}
	s.port = p
	s.conns = make(map[ServerTransport]bool)

	// handle: 连接读数据和处理逻辑
	var onConnect netpoll.OnConnect = func(ctx context.Context, connection netpoll.Connection) context.Context {
		transport, err := NewServerTransport(context.Background(), connection, serverConfig)
		if err != nil {
			panic(fmt.Sprintf("NewServerTransport err: %s", err.Error()))
		}
		s.mu.Lock()
		if s.conns == nil {
			s.mu.Unlock()
			transport.Close()
			return ctx
		}
		s.conns[transport] = true
		h := &testStreamHandler{t: transport.(*http2Server)}
		s.h = h
		h.srv = s
		s.mu.Unlock()
		switch ht {
		case notifyCall:
			go transport.HandleStreams(func(stream *Stream) {
				s.mu.Lock()
				h.handleStreamAndNotify(stream)
				s.mu.Unlock()
			}, func(ctx context.Context, _ string) context.Context {
				return ctx
			})
		case suspended:
			go transport.HandleStreams(func(*Stream) {}, // Do nothing to handle the stream.
				func(ctx context.Context, method string) context.Context {
					return ctx
				})
		case misbehaved:
			go transport.HandleStreams(func(s *Stream) {
				go h.handleStreamMisbehave(t, s)
			}, func(ctx context.Context, method string) context.Context {
				return ctx
			})
		case encodingRequiredStatus:
			go transport.HandleStreams(func(s *Stream) {
				go h.handleStreamEncodingRequiredStatus(t, s)
			}, func(ctx context.Context, method string) context.Context {
				return ctx
			})
		case invalidHeaderField:
			go transport.HandleStreams(func(s *Stream) {
				go h.handleStreamInvalidHeaderField(t, s)
			}, func(ctx context.Context, method string) context.Context {
				return ctx
			})
		case delayRead:
			h.notify = make(chan struct{})
			h.getNotified = make(chan struct{})
			s.mu.Lock()
			close(s.ready)
			s.mu.Unlock()
			go transport.HandleStreams(func(s *Stream) {
				go h.handleStreamDelayRead(t, s)
			}, func(ctx context.Context, method string) context.Context {
				return ctx
			})
		case pingpong:
			go transport.HandleStreams(func(s *Stream) {
				go h.handleStreamPingPong(t, s)
			}, func(ctx context.Context, method string) context.Context {
				return ctx
			})
		case gracefulShutdown:
			s.transWG.Add(1)
			go func() {
				defer s.transWG.Done()
				transport.HandleStreams(func(stream *Stream) {
					s.hdlWG.Add(1)
					go func() {
						defer s.hdlWG.Done()
						h.gracefulShutdown(t, stream)
					}()
				}, func(ctx context.Context, method string) context.Context { return ctx })
			}()
		case cancel:
			go transport.HandleStreams(func(s *Stream) {
				go h.handleStreamCancel(t, s)
			}, func(ctx context.Context, method string) context.Context {
				return ctx
			})
		default:
			go func() {
				transport.HandleStreams(func(s *Stream) {
					go h.handleStream(t, s)
				}, func(ctx context.Context, method string) context.Context {
					return ctx
				})
			}()
		}
		return ctx
	}

	// options: EventLoop 初始化自定义配置项
	opts := []netpoll.Option{
		netpoll.WithIdleTimeout(10 * time.Minute),
		netpoll.WithOnConnect(onConnect),
	}

	// 创建 EventLoop
	s.eventLoop, err = netpoll.NewEventLoop(nil, opts...)
	if err != nil {
		panic("create netpoll event-loop fail")
	}
	s.startedErr <- nil

	// 运行 Server
	err = s.eventLoop.Serve(s.lis)
	if err != nil {
		t.Logf("netpoll server Serve failed, err=%v", err)
	}
}

func (s *server) wait(t *testing.T, timeout time.Duration) {
	select {
	case err := <-s.startedErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(timeout):
		t.Fatalf("Timed out after %v waiting for server to be ready", timeout)
	}
}

func (s *server) stop() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s.lis.Close()
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.conns = nil
	s.mu.Unlock()
	if err := s.eventLoop.Shutdown(ctx); err != nil {
		fmt.Printf("netpoll server exit failed, err=%v", err)
	}
}

func (s *server) gracefulShutdown(finishCh chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.lis.Close()
	s.mu.Lock()
	for trans := range s.conns {
		trans.Drain()
	}
	s.mu.Unlock()
	timeout, _ := ctx.Deadline()
	graceTimer := time.NewTimer(time.Until(timeout))
	exitCh := make(chan struct{})
	go func() {
		select {
		case <-graceTimer.C:
			s.mu.Lock()
			for trans := range s.conns {
				trans.Close()
			}
			s.mu.Unlock()
			return
		case <-exitCh:
			return
		}
	}()
	s.hdlWG.Wait()
	s.transWG.Wait()
	close(exitCh)
	close(finishCh)
}

func (s *server) addr() string {
	if s.lis == nil {
		return ""
	}
	return s.lis.Addr().String()
}

func setUpServerOnly(t *testing.T, port int, serverConfig *ServerConfig, ht hType) *server {
	server := &server{startedErr: make(chan error, 1), ready: make(chan struct{}), srvReady: make(chan struct{})}
	go server.start(t, port, serverConfig, ht)
	server.wait(t, time.Second)
	return server
}

func setUp(t *testing.T, port int, maxStreams uint32, ht hType) (*server, *http2Client) {
	return setUpWithOptions(t, port, &ServerConfig{MaxStreams: maxStreams}, ht, ConnectOptions{})
}

func setUpWithOptions(t *testing.T, port int, serverConfig *ServerConfig, ht hType, copts ConnectOptions) (*server, *http2Client) {
	server := setUpServerOnly(t, port, serverConfig, ht)
	conn, err := netpoll.NewDialer().DialTimeout("tcp", "localhost:"+server.port, time.Second)
	if err != nil {
		t.Fatalf("failed to dial connection: %v", err)
	}
	ct, connErr := NewClientTransport(context.Background(), conn.(netpoll.Connection), copts, "", func(GoAwayReason) {}, func() {})
	if connErr != nil {
		t.Fatalf("failed to create transport: %v", connErr)
	}
	return server, ct.(*http2Client)
}

func setUpWithNoPingServer(t *testing.T, copts ConnectOptions, connCh chan net.Conn, exitCh chan struct{}) *http2Client {
	lis, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	// Launch a non responsive server and save the conn.
	eventLoop, err := netpoll.NewEventLoop(
		func(ctx context.Context, connection netpoll.Connection) error { return nil },
		netpoll.WithOnConnect(func(ctx context.Context, connection netpoll.Connection) context.Context {
			connCh <- connection.(netpoll.Conn)
			t.Logf("event loop on connect: %s", connection.RemoteAddr().String())
			return ctx
		}),
	)
	if err != nil {
		t.Fatalf("Create netpoll event-loop failed: %v", err)
	}
	go func() {
		go func() {
			err = eventLoop.Serve(lis)
			if err != nil {
				t.Errorf("netpoll server exit failed, err=%v", err)
				return
			}
		}()
		<-exitCh
		// shutdown will called lis.Close()
		_ = eventLoop.Shutdown(context.Background())
	}()

	conn, err := netpoll.NewDialer().DialTimeout("tcp", lis.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("Failed to dial: %v", err)
	}
	tr, err := NewClientTransport(context.Background(), conn.(netpoll.Connection), copts, "mockDestService", func(GoAwayReason) {}, func() {})
	if err != nil {
		// Server clean-up.
		if conn, ok := <-connCh; ok {
			conn.Close()
		}
		t.Fatalf("Failed to dial: %v", err)
	}
	return tr.(*http2Client)
}

func setUpWithOnGoAway(t *testing.T, port int, serverConfig *ServerConfig, ht hType, copts ConnectOptions, onGoAway func(reason GoAwayReason)) (*server, *http2Client) {
	server := setUpServerOnly(t, port, serverConfig, ht)
	conn, err := netpoll.NewDialer().DialTimeout("tcp", "localhost:"+server.port, time.Second)
	if err != nil {
		t.Fatalf("failed to dial connection: %v", err)
	}
	ct, connErr := NewClientTransport(context.Background(), conn.(netpoll.Connection), copts, "", onGoAway, func() {})
	if connErr != nil {
		t.Fatalf("failed to create transport: %v", connErr)
	}
	return server, ct.(*http2Client)
}

// TestInflightStreamClosing ensures that closing in-flight stream
// sends status error to concurrent stream reader.
func TestInflightStreamClosing(t *testing.T) {
	serverConfig := &ServerConfig{}
	server, client := setUpWithOptions(t, 0, serverConfig, suspended, ConnectOptions{})
	defer server.stop()
	defer client.Close(fmt.Errorf("self-close in test"))

	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	stream, err := client.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("Client failed to create RPC request: %v", err)
	}

	donec := make(chan struct{})
	// serr := &status.Error{e: s.Proto()}
	serr := status.Err(codes.Internal, "client connection is closing")
	go func() {
		defer close(donec)
		if _, err := stream.Read(make([]byte, defaultWindowSize)); err != serr {
			t.Errorf("unexpected Stream error %v, expected %v", err, serr)
		}
	}()

	// should unblock concurrent stream.Read
	client.CloseStream(stream, serr)

	// wait for stream.Read error
	timeout := time.NewTimer(3 * time.Second)
	select {
	case <-donec:
		if !timeout.Stop() {
			<-timeout.C
		}
	case <-timeout.C:
		t.Fatalf("%s", "Test timed out, expected a status error.")
	}
}

func TestClientSendAndReceive(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, normal)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Small",
	}
	ctx, ctxCancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer ctxCancel()
	s1, err1 := ct.NewStream(ctx, callHdr)
	if err1 != nil {
		t.Fatalf("failed to open stream: %v", err1)
	}
	if s1.id != 1 {
		t.Fatalf("wrong stream id: %d", s1.id)
	}
	s2, err2 := ct.NewStream(ctx, callHdr)
	if err2 != nil {
		t.Fatalf("failed to open stream: %v", err2)
	}
	if s2.id != 3 {
		t.Fatalf("wrong stream id: %d", s2.id)
	}
	opts := Options{Last: true}
	if err := ct.Write(s1, nil, expectedRequest, &opts); err != nil && err != io.EOF {
		t.Fatalf("failed to send data: %v", err)
	}
	p := make([]byte, len(expectedResponse))
	_, recvErr := s1.Read(p)
	if recvErr != nil || !bytes.Equal(p, expectedResponse) {
		t.Fatalf("Error: %v, want <nil>; Result: %v, want %v", recvErr, p, expectedResponse)
	}
	_, recvErr = s1.Read(p)
	if recvErr != io.EOF {
		t.Fatalf("Error: %v; want <EOF>", recvErr)
	}
	ct.Close(errSelfCloseForTest)
	server.stop()
}

func TestClientErrorNotify(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, normal)
	go func() {
		if server != nil {
			server.stop()
		}
	}()
	// ct.reader should detect the error and activate ct.Error().
	<-ct.Error()
	ct.Close(nil)
}

func performOneRPC(ct ClientTransport) {
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Small",
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		return
	}
	opts := Options{Last: true}
	if err := ct.Write(s, []byte{}, expectedRequest, &opts); err == nil || err == io.EOF {
		time.Sleep(2 * time.Millisecond)
		// The following s.Recv()'s could error out because the
		// underlying transport is gone.
		//
		// Read response
		p := make([]byte, len(expectedResponse))
		s.Read(p)
		// Read io.EOF
		s.Read(p)
	}
}

func TestClientMix(t *testing.T) {
	s, ct := setUp(t, 0, math.MaxUint32, normal)
	go func(s *server) {
		time.Sleep(50 * time.Millisecond)
		s.stop()
	}(s)
	go func(ct ClientTransport) {
		<-ct.Error()
		ct.Close(errSelfCloseForTest)
	}(ct)
	for i := 0; i < 100; i++ {
		time.Sleep(1 * time.Millisecond)
		go performOneRPC(ct)
	}
}

func TestLargeMessage(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, normal)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Large",
	}
	ctx, ctxCancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer ctxCancel()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := ct.NewStream(ctx, callHdr)
			if err != nil {
				t.Errorf("%v.NewStream(_, _) = _, %v, want _, <nil>", ct, err)
			}
			if err := ct.Write(s, []byte{}, expectedRequestLarge, &Options{Last: true}); err != nil && err != io.EOF {
				t.Errorf("%v.write(_, _, _) = %v, want  <nil>", ct, err)
			}
			p := make([]byte, len(expectedResponseLarge))
			if _, err := s.Read(p); err != nil || !bytes.Equal(p, expectedResponseLarge) {
				t.Errorf("s.Read(%v) = _, %v, want %v, <nil>", err, p, expectedResponse)
			}
			if _, err = s.Read(p); err != io.EOF {
				t.Errorf("Failed to complete the stream %v; want <EOF>", err)
			}
		}()
	}
	wg.Wait()
	ct.Close(errSelfCloseForTest)
	server.stop()
}

func TestLargeMessageWithDelayRead(t *testing.T) {
	// Disable dynamic flow control.
	sc := &ServerConfig{
		InitialWindowSize:     defaultWindowSize,
		InitialConnWindowSize: defaultWindowSize,
	}
	co := ConnectOptions{
		InitialWindowSize:     defaultWindowSize,
		InitialConnWindowSize: defaultWindowSize,
	}
	server, ct := setUpWithOptions(t, 0, sc, delayRead, co)
	defer server.stop()
	defer ct.Close(errSelfCloseForTest)
	server.mu.Lock()
	ready := server.ready
	server.mu.Unlock()
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Large",
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*10))
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		t.Fatalf("%v.NewStream(_, _) = _, %v, want _, <nil>", ct, err)
		return
	}
	// Wait for server's handler to be initialized
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatalf("%s", "Client timed out waiting for server handler to be initialized.")
	}
	server.mu.Lock()
	serviceHandler := server.h
	server.mu.Unlock()
	var (
		mu    sync.Mutex
		total int
	)
	s.wq.replenish = func(n int) {
		mu.Lock()
		total += n
		mu.Unlock()
		s.wq.realReplenish(n)
	}
	getTotal := func() int {
		mu.Lock()
		defer mu.Unlock()
		return total
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			// Prevent goroutine from leaking in case of error.
			case <-done:
				return
			default:
			}
			if getTotal() == int(defaultWindowSize) {
				// unblock server to be able to read and
				// thereby send stream level window update.
				close(serviceHandler.getNotified)
				return
			}
			runtime.Gosched()
		}
	}()
	// This write will cause client to run out of stream level,
	// flow control and the other side won't send a window update
	// until that happens.
	if err := ct.Write(s, []byte{}, expectedRequestLarge, &Options{}); err != nil {
		t.Fatalf("write(_, _, _) = %v, want  <nil>", err)
	}
	p := make([]byte, len(expectedResponseLarge))

	// Wait for the other side to run out of stream level flow control before
	// reading and thereby sending a window update.
	select {
	case <-serviceHandler.notify:
	case <-ctx.Done():
		t.Fatalf("%s", "Client timed out")
	}
	if _, err := s.Read(p); err != nil || !bytes.Equal(p, expectedResponseLarge) {
		t.Fatalf("s.Read(_) = _, %v, want _, <nil>", err)
	}
	if err := ct.Write(s, []byte{}, expectedRequestLarge, &Options{Last: true}); err != nil {
		t.Fatalf("write(_, _, _) = %v, want <nil>", err)
	}
	if _, err = s.Read(p); err != io.EOF {
		t.Fatalf("Failed to complete the stream %v; want <EOF>", err)
	}
}

// FIXME Test failed because goroutine leak.
//func TestGracefulClose(t *testing.T) {
//	server, ct := setUp(t, 0, math.MaxUint32, pingpong)
//	defer func() {
//		// Stop the server's listener to make the server's goroutines terminate
//		// (after the last active stream is done).
//		server.lis.Close()
//		// Check for goroutine leaks (i.e. GracefulClose with an active stream
//		// doesn't eventually close the connection when that stream completes).
//		leakcheck.Check(t)
//		// Correctly clean up the server
//		server.stop()
//	}()
//	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*10))
//	defer cancel()
//	s, err := ct.NewStream(ctx, &CallHdr{})
//	if err != nil {
//		t.Fatalf("NewStream(_, _) = _, %v, want _, <nil>", err)
//	}
//	msg := make([]byte, 1024)
//	outgoingHeader := make([]byte, 5)
//	outgoingHeader[0] = byte(0)
//	binary.BigEndian.PutUint32(outgoingHeader[1:], uint32(len(msg)))
//	incomingHeader := make([]byte, 5)
//	if err := ct.write(s, outgoingHeader, msg, &Options{}); err != nil {
//		t.Fatalf("Error while writing: %v", err)
//	}
//	if _, err := s.Read(incomingHeader); err != nil {
//		t.Fatalf("Error while reading: %v", err)
//	}
//	sz := binary.BigEndian.Uint32(incomingHeader[1:])
//	recvMsg := make([]byte, int(sz))
//	if _, err := s.Read(recvMsg); err != nil {
//		t.Fatalf("Error while reading: %v", err)
//	}
//	ct.GracefulClose()
//	var wg sync.WaitGroup
//	// Expect the failure for all the follow-up streams because ct has been closed gracefully.
//	for i := 0; i < 200; i++ {
//		wg.Add(1)
//		go func() {
//			defer wg.Done()
//			str, err := ct.NewStream(ctx, &CallHdr{})
//			if err == ErrConnClosing {
//				return
//			} else if err != nil {
//				t.Errorf("_.NewStream(_, _) = _, %v, want _, %v", err, ErrConnClosing)
//				return
//			}
//			ct.write(str, nil, nil, &Options{Last: true})
//			if _, err := str.Read(make([]byte, 8)); err != errStreamDrain && err != ErrConnClosing {
//				t.Errorf("_.Read(_) = _, %v, want _, %v or %v", err, errStreamDrain, ErrConnClosing)
//			}
//		}()
//	}
//	ct.write(s, nil, nil, &Options{Last: true})
//	if _, err := s.Read(incomingHeader); err != io.EOF {
//		t.Fatalf("Client expected EOF from the server. Got: %v", err)
//	}
//	// The stream which was created before graceful close can still proceed.
//	wg.Wait()
//}

func TestLargeMessageSuspension(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, suspended)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Large",
	}
	// Set a long enough timeout for writing a large message out.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		t.Fatalf("failed to open stream: %v", err)
	}
	// Launch a goroutine similar to the stream monitoring goroutine in
	// stream.go to keep track of context timeout and call CloseStream.
	go func() {
		<-ctx.Done()
		ct.CloseStream(s, ContextErr(ctx.Err()))
	}()
	// write should not be done successfully due to flow control.
	msg := make([]byte, initialWindowSize*8)
	ct.Write(s, nil, msg, &Options{})
	err = ct.Write(s, nil, msg, &Options{Last: true})
	test.Assert(t, errors.Is(err, ContextErr(ctx.Err())), err)
	expectedErr := status.Err(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	if _, err := s.Read(make([]byte, 8)); err.Error() != expectedErr.Error() {
		t.Fatalf("Read got %v of type %T, want %v", err, err, expectedErr)
	}
	ct.Close(errSelfCloseForTest)
	server.stop()
}

func TestMaxStreams(t *testing.T) {
	serverConfig := &ServerConfig{
		MaxStreams: 1,
	}
	server, ct := setUpWithOptions(t, 0, serverConfig, suspended, ConnectOptions{})
	defer ct.Close(errSelfCloseForTest)
	defer server.stop()
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Large",
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		t.Fatalf("Failed to open stream: %v", err)
	}
	// Keep creating streams until one fails with deadline exceeded, marking the application
	// of server settings on client.
	slist := []*Stream{}
	pctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	timer := time.NewTimer(time.Second * 10)
	expectedErr := status.Err(codes.DeadlineExceeded, context.DeadlineExceeded.Error())
	for {
		select {
		case <-timer.C:
			t.Fatalf("%s", "Test timeout: client didn't receive server settings.")
		default:
		}
		ctx, cancel := context.WithDeadline(pctx, time.Now().Add(100*time.Millisecond))
		// This is only to get rid of govet. All these context are based on a base
		// context which is canceled at the end of the test.
		defer cancel()
		if str, err := ct.NewStream(ctx, callHdr); err == nil {
			slist = append(slist, str)
			continue
		} else if err.Error() != expectedErr.Error() {
			t.Fatalf("ct.NewStream(_,_) = _, %v, want _, %v", err, expectedErr)
		}
		timer.Stop()
		break
	}
	done := make(chan struct{})
	// Try and create a new stream.
	go func() {
		defer close(done)
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*10))
		defer cancel()
		if _, err := ct.NewStream(ctx, callHdr); err != nil {
			t.Errorf("Failed to open stream: %v", err)
		}
	}()
	// Close all the extra streams created and make sure the new stream is not created.
	for _, str := range slist {
		ct.CloseStream(str, nil)
	}
	select {
	case <-done:
		t.Fatalf("%s", "Test failed: didn't expect new stream to be created just yet.")
	default:
	}
	// Close the first stream created so that the new stream can finally be created.
	ct.CloseStream(s, nil)
	<-done
	ct.Close(errSelfCloseForTest)
	<-ct.writerDone
	if ct.maxConcurrentStreams != 1 {
		t.Fatalf("ct.maxConcurrentStreams: %d, want 1", ct.maxConcurrentStreams)
	}
}

func TestServerContextCanceledOnClosedConnection(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, suspended)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo",
	}
	var sc *http2Server
	// Wait until the server transport is setup.
	for {
		server.mu.Lock()
		if len(server.conns) == 0 {
			server.mu.Unlock()
			time.Sleep(time.Millisecond)
			continue
		}
		for k := range server.conns {
			var ok bool
			sc, ok = k.(*http2Server)
			if !ok {
				t.Fatalf("Failed to convert %v to *http2Server", k)
			}
		}
		server.mu.Unlock()
		break
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		t.Fatalf("Failed to open stream: %v", err)
	}
	ct.controlBuf.put(&dataFrame{
		streamID:  s.id,
		endStream: false,
		h:         nil,
		d:         make([]byte, http2MaxFrameLen),
	})
	// Loop until the server side stream is created.
	var ss *Stream
	for {
		time.Sleep(20 * time.Millisecond)
		sc.mu.Lock()
		if len(sc.activeStreams) == 0 {
			sc.mu.Unlock()
			continue
		}
		ss = sc.activeStreams[s.id]
		sc.mu.Unlock()
		break
	}
	ct.Close(errSelfCloseForTest)
	select {
	case <-ss.Context().Done():
		if ss.Context().Err() != errConnectionEOF {
			t.Fatalf("ss.Context().Err() got %v, want %v", ss.Context().Err(), errConnectionEOF)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("%s", "Failed to cancel the context of the sever side stream.")
	}
	server.stop()
}

// FIXME delete the comments
func TestClientConnDecoupledFromApplicationRead(t *testing.T) {
	connectOptions := ConnectOptions{
		InitialWindowSize:     defaultWindowSize,
		InitialConnWindowSize: defaultWindowSize,
	}
	server, client := setUpWithOptions(t, 0, &ServerConfig{}, notifyCall, connectOptions)
	defer server.stop()
	defer client.Close(errSelfCloseForTest)

	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()

		if len(server.conns) == 0 {
			return true, fmt.Errorf("timed-out while waiting for connection to be created on the server")
		}
		return false, nil
	})

	var st *http2Server
	server.mu.Lock()
	for k := range server.conns {
		st = k.(*http2Server)
	}
	notifyChan := make(chan struct{})
	server.h.notify = notifyChan
	server.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	cstream1, err := client.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("Client failed to create first stream. Err: %v", err)
	}

	<-notifyChan
	var sstream1 *Stream
	// Access stream on the server.
	st.mu.Lock()
	for _, v := range st.activeStreams {
		if v.id == cstream1.id {
			sstream1 = v
		}
	}
	st.mu.Unlock()
	if sstream1 == nil {
		t.Fatalf("Didn't find stream corresponding to client cstream.id: %v on the server", cstream1.id)
	}
	// Exhaust client's connection window.
	if err := st.Write(sstream1, []byte{}, make([]byte, defaultWindowSize), &Options{}); err != nil {
		t.Fatalf("Server failed to write data. Err: %v", err)
	}
	notifyChan = make(chan struct{})
	server.mu.Lock()
	server.h.notify = notifyChan
	server.mu.Unlock()
	// Create another stream on client.
	cstream2, err := client.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("Client failed to create second stream. Err: %v", err)
	}
	<-notifyChan
	var sstream2 *Stream
	st.mu.Lock()
	for _, v := range st.activeStreams {
		if v.id == cstream2.id {
			sstream2 = v
		}
	}
	st.mu.Unlock()
	if sstream2 == nil {
		t.Fatalf("Didn't find stream corresponding to client cstream.id: %v on the server", cstream2.id)
	}
	// Server should be able to send data on the new stream, even though the client hasn't read anything on the first stream.
	if err := st.Write(sstream2, []byte{}, make([]byte, defaultWindowSize), &Options{}); err != nil {
		t.Fatalf("Server failed to write data. Err: %v", err)
	}

	// Client should be able to read data on second stream.
	if _, err := cstream2.Read(make([]byte, defaultWindowSize)); err != nil {
		t.Fatalf("_.Read(_) = _, %v, want _, <nil>", err)
	}

	// Client should be able to read data on first stream.
	if _, err := cstream1.Read(make([]byte, defaultWindowSize)); err != nil {
		t.Fatalf("_.Read(_) = _, %v, want _, <nil>", err)
	}
}

func TestServerConnDecoupledFromApplicationRead(t *testing.T) {
	serverConfig := &ServerConfig{
		InitialWindowSize:     defaultWindowSize,
		InitialConnWindowSize: defaultWindowSize,
	}
	server, client := setUpWithOptions(t, 0, serverConfig, suspended, ConnectOptions{})
	defer server.stop()
	defer client.Close(errSelfCloseForTest)
	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()

		if len(server.conns) == 0 {
			return true, fmt.Errorf("timed-out while waiting for connection to be created on the server")
		}
		return false, nil
	})
	var st *http2Server
	server.mu.Lock()
	for k := range server.conns {
		st = k.(*http2Server)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	server.mu.Unlock()
	cstream1, err := client.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("Failed to create 1st stream. Err: %v", err)
	}
	// Exhaust server's connection window.
	if err := client.Write(cstream1, nil, make([]byte, defaultWindowSize), &Options{Last: true}); err != nil {
		t.Fatalf("Client failed to write data. Err: %v", err)
	}
	// Client should be able to create another stream and send data on it.
	cstream2, err := client.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("Failed to create 2nd stream. Err: %v", err)
	}
	if err := client.Write(cstream2, nil, make([]byte, defaultWindowSize), &Options{}); err != nil {
		t.Fatalf("Client failed to write data. Err: %v", err)
	}
	// Get the streams on server.
	waitWhileTrue(t, func() (bool, error) {
		st.mu.Lock()
		defer st.mu.Unlock()

		if len(st.activeStreams) != 2 {
			return true, fmt.Errorf("timed-out while waiting for server to have created the streams")
		}
		return false, nil
	})
	var sstream1 *Stream
	st.mu.Lock()
	for _, v := range st.activeStreams {
		if v.id == 1 {
			sstream1 = v
		}
	}
	st.mu.Unlock()
	// Reading from the stream on server should succeed.
	if _, err := sstream1.Read(make([]byte, defaultWindowSize)); err != nil {
		t.Fatalf("_.Read(_) = %v, want <nil>", err)
	}

	if _, err := sstream1.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("_.Read(_) = %v, want io.EOF", err)
	}
}

func TestServerWithMisbehavedClient(t *testing.T) {
	var wg sync.WaitGroup
	server := setUpServerOnly(t, 0, &ServerConfig{}, suspended)
	defer func() {
		wg.Wait()
		server.stop()
	}()
	defer server.stop()
	// Create a client that can override server stream quota.
	mconn, err := netpoll.NewDialer().DialTimeout("tcp", server.lis.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("Client failed to dial:%v", err)
	}
	defer mconn.Close()
	if err := mconn.(netpoll.Connection).SetIdleTimeout(10 * time.Second); err != nil {
		t.Fatalf("Failed to set write deadline: %v", err)
	}
	if n, err := mconn.Write(ClientPreface); err != nil || n != len(ClientPreface) {
		t.Fatalf("mconn.write(ClientPreface ) = %d, %v, want %d, <nil>", n, err, len(ClientPreface))
	}
	// success chan indicates that reader received a RSTStream from server.
	success := make(chan struct{})
	var mu sync.Mutex
	framer := grpcframe.NewFramer(mconn, mconn.(netpoll.Connection).Reader())
	if err := framer.WriteSettings(); err != nil {
		t.Fatalf("Error while writing settings: %v", err)
	}
	wg.Add(1)
	go func() { // Launch a reader for this misbehaving client.
		defer wg.Done()
		for {
			frame, err := framer.ReadFrame()
			if err != nil {
				return
			}
			switch frame := frame.(type) {
			case *http2.PingFrame:
				// write ping ack back so that server's BDP estimation works right.
				mu.Lock()
				framer.WritePing(true, frame.Data)
				mu.Unlock()
			case *http2.RSTStreamFrame:
				if frame.Header().StreamID != 1 || http2.ErrCode(frame.ErrCode) != http2.ErrCodeFlowControl {
					t.Errorf("RST stream received with streamID: %d and code: %v, want streamID: 1 and code: http2.ErrCodeFlowControl", frame.Header().StreamID, http2.ErrCode(frame.ErrCode))
				}
				close(success)
				return
			default:
				// Do nothing.
			}

		}
	}()
	// Create a stream.
	var buf bytes.Buffer
	henc := hpack.NewEncoder(&buf)
	// TODO(mmukhi): Remove unnecessary fields.
	if err := henc.WriteField(hpack.HeaderField{Name: ":method", Value: "POST"}); err != nil {
		t.Fatalf("Error while encoding header: %v", err)
	}
	if err := henc.WriteField(hpack.HeaderField{Name: ":path", Value: "foo"}); err != nil {
		t.Fatalf("Error while encoding header: %v", err)
	}
	if err := henc.WriteField(hpack.HeaderField{Name: ":authority", Value: "localhost"}); err != nil {
		t.Fatalf("Error while encoding header: %v", err)
	}
	if err := henc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"}); err != nil {
		t.Fatalf("Error while encoding header: %v", err)
	}
	mu.Lock()
	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: buf.Bytes(), EndHeaders: true}); err != nil {
		mu.Unlock()
		t.Fatalf("Error while writing headers: %v", err)
	}
	mu.Unlock()

	// Test server behavior for violation of stream flow control window size restriction.
	timer := time.NewTimer(time.Second * 5)
	dbuf := make([]byte, http2MaxFrameLen)
	for {
		select {
		case <-timer.C:
			t.Fatalf("%s", "Test timed out.")
		case <-success:
			return
		default:
		}
		mu.Lock()
		if err := framer.WriteData(1, false, dbuf); err != nil {
			mu.Unlock()
			// Error here means the server could have closed the connection due to flow control
			// violation. Make sure that is the case by waiting for success chan to be closed.
			select {
			case <-timer.C:
				t.Fatalf("Error while writing data: %v", err)
			case <-success:
				return
			}
		}
		mu.Unlock()
		// This for loop is capable of hogging the CPU and cause starvation
		// in Go versions prior to 1.9,
		// in single CPU environment. Explicitly relinquish processor.
		runtime.Gosched()
	}
}

// FIXME Test failed, hang up.
//func TestClientWithMisbehavedServer(t *testing.T) {
//	// Create a misbehaving server.
//	lis, err := netpoll.CreateListener("tcp", "localhost:0")
//	if err != nil {
//		t.Fatalf("Error while listening: %v", err)
//	}
//	defer lis.Close()
//	// success chan indicates that the server received
//	// RSTStream from the client.
//	success := make(chan struct{})
//
//	exitCh := make(chan struct{}, 1)
//	eventLoop, _ := netpoll.NewEventLoop(func(ctx context.Context, connection netpoll.Connection) error {
//		defer func() {
//			exitCh <- struct{}{}
//		}()
//		defer connection.Close()
//		if _, err := io.ReadFull(connection, make([]byte, len(ClientPreface))); err != nil {
//			t.Errorf("Error while reading clieng preface: %v", err)
//			return err
//		}
//		sfr := http2.NewFramer(connection, connection)
//		if err := sfr.WriteSettingsAck(); err != nil {
//			t.Errorf("Error while writing settings: %v", err)
//			return err
//		}
//		var mu sync.Mutex
//		for {
//			frame, err := sfr.ReadFrame()
//			if err != nil {
//				return err
//			}
//			switch frame := frame.(type) {
//			case *http2.HeadersFrame:
//				// When the client creates a stream, violate the stream flow control.
//				go func() {
//					buf := make([]byte, http2MaxFrameLen)
//					for {
//						mu.Lock()
//						if err := sfr.WriteData(1, false, buf); err != nil {
//							mu.Unlock()
//							return
//						}
//						mu.Unlock()
//						// This for loop is capable of hogging the CPU and cause starvation
//						// in Go versions prior to 1.9,
//						// in single CPU environment. Explicitly relinquish processor.
//						runtime.Gosched()
//					}
//				}()
//			case *http2.RSTStreamFrame:
//				if frame.Header().StreamID != 1 || http2.ErrCode(frame.ErrCode) != http2.ErrCodeFlowControl {
//					t.Errorf("RST stream received with streamID: %d and code: %v, want streamID: 1 and code: http2.ErrCodeFlowControl", frame.Header().StreamID, http2.ErrCode(frame.ErrCode))
//				}
//				close(success)
//				return err
//			case *http2.PingFrame:
//				mu.Lock()
//				sfr.WritePing(true, frame.Data)
//				mu.Unlock()
//			default:
//			}
//		}
//	})
//	go func() {
//		if err := eventLoop.Serve(lis); err != nil {
//			t.Errorf("eventLoop Serve failed, err=%s", err.Error())
//		}
//	}()
//
//	select {
//	case <-exitCh:
//		var ctx, cancel = context.WithTimeout(context.Background(), time.Second)
//		defer cancel()
//		if err := eventLoop.Shutdown(ctx); err != nil {
//			t.Errorf("netpoll server exit failed, err=%v", err)
//		}
//	}
//
//	// FIXME
//	//go func() { // Launch the misbehaving server.
//	//	sconn, err := lis.Accept()
//	//	if err != nil {
//	//		t.Errorf("Error while accepting: %v", err)
//	//		return
//	//	}
//	//	defer sconn.Close()
//	//	if _, err := io.ReadFull(sconn, make([]byte, len(ClientPreface))); err != nil {
//	//		t.Errorf("Error while reading clieng preface: %v", err)
//	//		return
//	//	}
//	//	sfr := http2.NewFramer(sconn.(netpoll.Connection).Writer(), sconn.(netpoll.Connection).Reader())
//	//	if err := sfr.WriteSettingsAck(); err != nil {
//	//		t.Errorf("Error while writing settings: %v", err)
//	//		return
//	//	}
//	//	var mu sync.Mutex
//	//	for {
//	//		frame, err := sfr.ReadFrame()
//	//		if err != nil {
//	//			return
//	//		}
//	//		switch frame := frame.(type) {
//	//		case *http2.HeadersFrame:
//	//			// When the client creates a stream, violate the stream flow control.
//	//			go func() {
//	//				buf := make([]byte, http2MaxFrameLen)
//	//				for {
//	//					mu.Lock()
//	//					if err := sfr.WriteData(1, false, buf); err != nil {
//	//						mu.Unlock()
//	//						return
//	//					}
//	//					mu.Unlock()
//	//					// This for loop is capable of hogging the CPU and cause starvation
//	//					// in Go versions prior to 1.9,
//	//					// in single CPU environment. Explicitly relinquish processor.
//	//					runtime.Gosched()
//	//				}
//	//			}()
//	//		case *http2.RSTStreamFrame:
//	//			if frame.Header().StreamID != 1 || http2.ErrCode(frame.ErrCode) != http2.ErrCodeFlowControl {
//	//				t.Errorf("RST stream received with streamID: %d and code: %v, want streamID: 1 and code: http2.ErrCodeFlowControl", frame.Header().StreamID, http2.ErrCode(frame.ErrCode))
//	//			}
//	//			close(success)
//	//			return
//	//		case *http2.PingFrame:
//	//			mu.Lock()
//	//			sfr.WritePing(true, frame.Data)
//	//			mu.Unlock()
//	//		default:
//	//		}
//	//	}
//	//}()
//	connectCtx, cancel := context.WithDeadline(context.Background(), time.Now().Add(2*time.Second))
//	defer cancel()
//	conn, err := netpoll.NewDialer().DialTimeout("tcp", lis.Addr().String(), time.Second)
//	if err != nil {
//		t.Fatalf("dial failed: %v", err)
//	}
//	ct, err := NewClientTransport(context.Background(), conn.(netpoll.Connection), ConnectOptions{}, "mockDestService", func(GoAwayReason) {}, func() {})
//	if err != nil {
//		t.Fatalf("Error while creating client transport: %v", err)
//	}
//	defer ct.Close()
//	str, err := ct.NewStream(connectCtx, &CallHdr{})
//	if err != nil {
//		t.Fatalf("Error while creating stream: %v", err)
//	}
//	timer := time.NewTimer(time.Second * 5)
//	go func() { // This go routine mimics the one in stream.go to call CloseStream.
//		<-str.Done()
//		ct.CloseStream(str, nil)
//	}()
//	select {
//	case <-timer.C:
//		t.Fatalf("Test timed-out.")
//	case <-success:
//	}
//}

var encodingTestStatus = status.New(codes.Internal, "\n")

func TestEncodingRequiredStatus(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, encodingRequiredStatus)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo",
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		return
	}
	opts := Options{Last: true}
	if err := ct.Write(s, nil, expectedRequest, &opts); err != nil {
		// in case server-side returns earlier, Write also returns a status error.
		if err != errStatusStreamDone || !testutils.StatusErrEqual(s.Status().Err(), encodingTestStatus.Err()) {
			t.Fatalf("Failed to write the request: %v", err)
		}
	}
	p := make([]byte, http2MaxFrameLen)
	if _, err := s.trReader.(*transportReader).Read(p); err != io.EOF {
		t.Fatalf("Read got error %v, want %v", err, io.EOF)
	}
	if !testutils.StatusErrEqual(s.Status().Err(), encodingTestStatus.Err()) {
		t.Fatalf("stream with status %v, want %v", s.Status(), encodingTestStatus)
	}
	ct.Close(errSelfCloseForTest)
	server.stop()
}

func TestInvalidHeaderField(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, invalidHeaderField)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo",
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	s, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		return
	}
	p := make([]byte, http2MaxFrameLen)
	_, err = s.trReader.(*transportReader).Read(p)
	if se, ok := status.FromError(err); !ok || se.Code() != codes.Internal || !strings.Contains(err.Error(), expectedInvalidHeaderField) {
		t.Fatalf("Read got error %v, want error with code %v and contains %q", err, codes.Internal, expectedInvalidHeaderField)
	}
	ct.Close(errSelfCloseForTest)
	server.stop()
}

func TestGetClientStat(t *testing.T) {
	svr, cli := setUp(t, 0, math.MaxUint32, normal)
	defer svr.stop()

	callHdr := &CallHdr{Host: "localhost", Method: "foo"}
	s, err := cli.NewStream(context.Background(), callHdr)
	if err != nil {
		t.Fatal(err)
	}

	// no header
	md, ok := s.tryGetHeader()
	test.Assert(t, md == nil)
	test.Assert(t, !ok)
	test.Assert(t, !s.getHeaderValid())

	// write header
	outgoingHeader := make([]byte, 5)
	outgoingHeader[0] = byte(0)
	binary.BigEndian.PutUint32(outgoingHeader[1:], uint32(len(expectedRequest)))
	err = cli.Write(s, []byte{}, expectedRequest, &Options{Last: true})
	if err != nil {
		t.Fatal(err)
	}
	// wait header
	s.waitOnHeader()

	// try to get header
	md, ok = s.tryGetHeader()
	test.Assert(t, md != nil)
	test.Assert(t, ok)
	test.Assert(t, s.getHeaderValid())
	// send quota
	w := cli.getOutFlowWindow()
	test.Assert(t, uint32(w) == cli.loopy.sendQuota)

	// Dump
	d := cli.Dump()
	td, ok := d.(clientTransportDump)
	test.Assert(t, ok)
	test.Assert(t, td.State == reachable)
	cli.mu.Lock()
	remoteAddr := cli.remoteAddr.String()
	test.Assert(t, len(td.ActiveStreams) == len(cli.activeStreams))
	cli.mu.Unlock()

	if len(td.ActiveStreams) != 0 {
		// If receiving RST, the stream will be removed from ActiveStreams. So, only check if the stream is still in ActiveStreams.
		sd := td.ActiveStreams[0]
		test.Assert(t, sd.ID == s.id)
		test.Assert(t, sd.ValidHeaderReceived)
		test.Assert(t, sd.Method == callHdr.Method)
		test.Assert(t, sd.RemoteAddress == remoteAddr)
	}

	// with "rip"
	s.hdrMu.Lock()
	s.header.Set("rip", "rip")
	s.hdrMu.Unlock()
	td = cli.Dump().(clientTransportDump)
	if len(td.ActiveStreams) != 0 {
		sd := td.ActiveStreams[0]
		test.Assert(t, sd.RemoteAddress == "rip")
	}
}

func TestHeaderChanClosedAfterReceivingAnInvalidHeader(t *testing.T) {
	server, ct := setUp(t, 0, math.MaxUint32, invalidHeaderField)
	defer server.stop()
	defer ct.Close(errSelfCloseForTest)
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	s, err := ct.NewStream(ctx, &CallHdr{Host: "localhost", Method: "foo"})
	if err != nil {
		t.Fatalf("%s", "failed to create the stream")
	}
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-s.headerChan:
	case <-timer.C:
		t.Errorf("%s", "s.headerChan: got open, want closed")
	}
}

func TestIsReservedHeader(t *testing.T) {
	tests := []struct {
		h    string
		want bool
	}{
		{"", false}, // but should be rejected earlier
		{"foo", false},
		{"content-type", true},
		{"user-agent", true},
		{":anything", true},
		{"grpc-message-type", true},
		{"grpc-encoding", true},
		{"grpc-message", true},
		{"grpc-status", true},
		{"grpc-timeout", true},
		{"te", true},
	}
	for _, tt := range tests {
		got := isReservedHeader(tt.h)
		if got != tt.want {
			t.Errorf("isReservedHeader(%q) = %v; want %v", tt.h, got, tt.want)
		}
	}
}

func TestContextErr(t *testing.T) {
	for _, test := range []struct {
		// input
		errIn error
		// outputs
		errOut error
	}{
		{context.DeadlineExceeded, status.Err(codes.DeadlineExceeded, context.DeadlineExceeded.Error())},
		{context.Canceled, status.Err(codes.Canceled, context.Canceled.Error())},
	} {
		err := ContextErr(test.errIn)
		if err.Error() != test.errOut.Error() {
			t.Fatalf("ContextErr{%v} = %v \nwant %v", test.errIn, err, test.errOut)
		}
	}
}

type windowSizeConfig struct {
	serverStream uint32
	serverConn   uint32
	clientStream uint32
	clientConn   uint32
}

// FIXME Test failed.
//func TestAccountCheckWindowSizeWithLargeWindow(t *testing.T) {
//	wc := windowSizeConfig{
//		serverStream: 10 * 1024 * 1024,
//		serverConn:   12 * 1024 * 1024,
//		clientStream: 6 * 1024 * 1024,
//		clientConn:   8 * 1024 * 1024,
//	}
//	testFlowControlAccountCheck(t, 1024*1024, wc)
//}

func TestAccountCheckWindowSizeWithSmallWindow(t *testing.T) {
	wc := windowSizeConfig{
		serverStream: defaultWindowSize,
		// Note this is smaller than initialConnWindowSize which is the current default.
		serverConn:   defaultWindowSize,
		clientStream: defaultWindowSize,
		clientConn:   defaultWindowSize,
	}
	testFlowControlAccountCheck(t, 1024*1024, wc)
}

func TestAccountCheckDynamicWindowSmallMessage(t *testing.T) {
	testFlowControlAccountCheck(t, 1024, windowSizeConfig{})
}

func TestAccountCheckDynamicWindowLargeMessage(t *testing.T) {
	testFlowControlAccountCheck(t, 1024*1024, windowSizeConfig{})
}

func testFlowControlAccountCheck(t *testing.T, msgSize int, wc windowSizeConfig) {
	sc := &ServerConfig{
		InitialWindowSize:     wc.serverStream,
		InitialConnWindowSize: wc.serverConn,
	}
	co := ConnectOptions{
		InitialWindowSize:     wc.clientStream,
		InitialConnWindowSize: wc.clientConn,
	}
	server, client := setUpWithOptions(t, 0, sc, pingpong, co)
	defer server.stop()
	defer client.Close(errSelfCloseForTest)
	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if len(server.conns) == 0 {
			return true, fmt.Errorf("timed out while waiting for server transport to be created")
		}
		return false, nil
	})
	var st *http2Server
	server.mu.Lock()
	for k := range server.conns {
		st = k.(*http2Server)
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	server.mu.Unlock()
	const numStreams = 10
	clientStreams := make([]*Stream, numStreams)
	for i := 0; i < numStreams; i++ {
		var err error
		clientStreams[i], err = client.NewStream(ctx, &CallHdr{})
		if err != nil {
			t.Fatalf("Failed to create stream. Err: %v", err)
		}
	}
	var wg sync.WaitGroup
	// For each stream send pingpong messages to the server.
	for _, stream := range clientStreams {
		wg.Add(1)
		go func(stream *Stream) {
			defer wg.Done()
			buf := make([]byte, msgSize+5)
			buf[0] = byte(0)
			binary.BigEndian.PutUint32(buf[1:], uint32(msgSize))
			opts := Options{}
			header := make([]byte, 5)
			for i := 1; i <= 10; i++ {
				if err := client.Write(stream, nil, buf, &opts); err != nil {
					t.Errorf("Error on client while writing message: %v", err)
					return
				}
				if _, err := stream.Read(header); err != nil {
					t.Errorf("Error on client while reading data frame header: %v", err)
					return
				}
				sz := binary.BigEndian.Uint32(header[1:])
				recvMsg := make([]byte, int(sz))
				if _, err := stream.Read(recvMsg); err != nil {
					t.Errorf("Error on client while reading data: %v", err)
					return
				}
				if len(recvMsg) != msgSize {
					t.Errorf("Length of message received by client: %v, want: %v", len(recvMsg), msgSize)
					return
				}
			}
		}(stream)
	}
	wg.Wait()
	serverStreams := map[uint32]*Stream{}
	loopyClientStreams := map[uint32]*outStream{}
	loopyServerStreams := map[uint32]*outStream{}
	// Get all the streams from server reader and writer and client writer.
	st.mu.Lock()
	for _, stream := range clientStreams {
		id := stream.id
		serverStreams[id] = st.activeStreams[id]
		loopyServerStreams[id] = st.loopy.estdStreams[id]
		loopyClientStreams[id] = client.loopy.estdStreams[id]

	}
	st.mu.Unlock()
	// Close all streams
	for _, stream := range clientStreams {
		client.Write(stream, nil, nil, &Options{Last: true})
		if _, err := stream.Read(make([]byte, 5)); err != io.EOF {
			t.Fatalf("Client expected an EOF from the server. Got: %v", err)
		}
	}
	// Close down both server and client so that their internals can be read without data
	// races.
	client.Close(errSelfCloseForTest)
	st.Close()
	<-st.readerDone
	<-st.writerDone
	<-client.readerDone
	<-client.writerDone
	for _, cstream := range clientStreams {
		id := cstream.id
		sstream := serverStreams[id]
		loopyServerStream := loopyServerStreams[id]
		loopyClientStream := loopyClientStreams[id]
		// Check stream flow control.
		if int(cstream.fc.limit+cstream.fc.delta-cstream.fc.pendingData-cstream.fc.pendingUpdate) != int(st.loopy.oiws)-loopyServerStream.bytesOutStanding {
			t.Fatalf("Account mismatch: client stream inflow limit(%d) + delta(%d) - pendingData(%d) - pendingUpdate(%d) != server outgoing InitialWindowSize(%d) - outgoingStream.bytesOutStanding(%d)", cstream.fc.limit, cstream.fc.delta, cstream.fc.pendingData, cstream.fc.pendingUpdate, st.loopy.oiws, loopyServerStream.bytesOutStanding)
		}
		if int(sstream.fc.limit+sstream.fc.delta-sstream.fc.pendingData-sstream.fc.pendingUpdate) != int(client.loopy.oiws)-loopyClientStream.bytesOutStanding {
			t.Fatalf("Account mismatch: server stream inflow limit(%d) + delta(%d) - pendingData(%d) - pendingUpdate(%d) != client outgoing InitialWindowSize(%d) - outgoingStream.bytesOutStanding(%d)", sstream.fc.limit, sstream.fc.delta, sstream.fc.pendingData, sstream.fc.pendingUpdate, client.loopy.oiws, loopyClientStream.bytesOutStanding)
		}
	}
	// Check transport flow control.
	if client.fc.limit != client.fc.unacked+st.loopy.sendQuota {
		t.Fatalf("Account mismatch: client transport inflow(%d) != client unacked(%d) + server sendQuota(%d)", client.fc.limit, client.fc.unacked, st.loopy.sendQuota)
	}
	if st.fc.limit != st.fc.unacked+client.loopy.sendQuota {
		t.Fatalf("Account mismatch: server transport inflow(%d) != server unacked(%d) + client sendQuota(%d)", st.fc.limit, st.fc.unacked, client.loopy.sendQuota)
	}
}

func waitWhileTrue(t *testing.T, condition func() (bool, error)) {
	var (
		wait bool
		err  error
	)
	timer := time.NewTimer(time.Second * 5)
	for {
		wait, err = condition()
		if wait {
			select {
			case <-timer.C:
				t.Fatalf("%s", err.Error())
			default:
				time.Sleep(50 * time.Millisecond)
				continue
			}
		}
		if !timer.Stop() {
			<-timer.C
		}
		break
	}
}

// If any error occurs on a call to Stream.Read, future calls
// should continue to return that same error.
func TestReadGivesSameErrorAfterAnyErrorOccurs(t *testing.T) {
	testRecvBuffer := newRecvBuffer()
	s := &Stream{
		ctx:         context.Background(),
		buf:         testRecvBuffer,
		requestRead: func(int) {},
	}
	s.trReader = &transportReader{
		reader: &recvBufferReader{
			ctx:        s.ctx,
			ctxDone:    s.ctx.Done(),
			recv:       s.buf,
			freeBuffer: func(*bytes.Buffer) {},
		},
		windowHandler: func(int) {},
	}
	testData := make([]byte, 1)
	testData[0] = 5
	testBuffer := bytes.NewBuffer(testData)
	testErr := errors.New("test error")
	s.write(recvMsg{buffer: testBuffer, err: testErr})

	inBuf := make([]byte, 1)
	actualCount, actualErr := s.Read(inBuf)
	if actualCount != 0 {
		t.Errorf("actualCount, _ := s.Read(_) differs; want 0; got %v", actualCount)
	}
	if actualErr.Error() != testErr.Error() {
		t.Errorf("_ , actualErr := s.Read(_) differs; want actualErr.Error() to be %v; got %v", testErr.Error(), actualErr.Error())
	}

	s.write(recvMsg{buffer: testBuffer, err: nil})
	s.write(recvMsg{buffer: testBuffer, err: errors.New("different error from first")})

	for i := 0; i < 2; i++ {
		inBuf := make([]byte, 1)
		actualCount, actualErr := s.Read(inBuf)
		if actualCount != 0 {
			t.Errorf("actualCount, _ := s.Read(_) differs; want %v; got %v", 0, actualCount)
		}
		if actualErr.Error() != testErr.Error() {
			t.Errorf("_ , actualErr := s.Read(_) differs; want actualErr.Error() to be %v; got %v", testErr.Error(), actualErr.Error())
		}
	}
}

// FIXME Test failed.
// If the client sends an HTTP/2 request with a :method header with a value other than POST, as specified in
// the gRPC over HTTP/2 specification, the server should close the stream.
//func TestServerWithClientSendingWrongMethod(t *testing.T) {
//	server := setUpServerOnly(t, 0, &ServerConfig{}, suspended)
//	defer server.stop()
//	// Create a client directly to not couple what you can send to API of http2_client.go.
//	mconn, err := netpoll.NewDialer().DialTimeout("tcp", server.lis.Addr().String(), time.Second)
//	if err != nil {
//		t.Fatalf("Client failed to dial:%v", err)
//	}
//	defer mconn.Close()
//
//	if n, err := mconn.write(ClientPreface); err != nil || n != len(ClientPreface) {
//		t.Fatalf("mconn.write(ClientPreface ) = %d, %v, want %d, <nil>", n, err, len(ClientPreface))
//	}
//
//	framer := http2.NewFramer(mconn, mconn)
//	if err := framer.WriteSettings(); err != nil {
//		t.Fatalf("Error while writing settings: %v", err)
//	}
//
//	// success chan indicates that reader received a RSTStream from server.
//	// An error will be passed on it if any other frame is received.
//	success := testutils.NewChannel()
//
//	// Launch a reader goroutine.
//	go func() {
//		for {
//			frame, err := framer.ReadFrame()
//			if err != nil {
//				return
//			}
//			switch frame := frame.(type) {
//			case *grpcframe.SettingsFrame:
//				// Do nothing. A settings frame is expected from server preface.
//			case *http2.RSTStreamFrame:
//				if frame.Header().StreamID != 1 || http2.ErrCode(frame.ErrCode) != http2.ErrCodeProtocol {
//					// Client only created a single stream, so RST Stream should be for that single stream.
//					t.Errorf("RST stream received with streamID: %d and code %v, want streamID: 1 and code: http.ErrCodeFlowControl", frame.Header().StreamID, http2.ErrCode(frame.ErrCode))
//				}
//				// Records that client successfully received RST Stream frame.
//				success.Send(nil)
//				return
//			default:
//				// The server should send nothing but a single RST Stream frame.
//				success.Send(errors.New("The client received a frame other than RST Stream"))
//			}
//		}
//	}()
//
//	// Done with HTTP/2 setup - now create a stream with a bad method header.
//	var buf bytes.buffer
//	henc := hpack.NewEncoder(&buf)
//	// Method is required to be POST in a gRPC call.
//	if err := henc.WriteField(hpack.HeaderField{Name: ":method", Value: "PUT"}); err != nil {
//		t.Fatalf("Error while encoding header: %v", err)
//	}
//	// Have the rest of the headers be ok and within the gRPC over HTTP/2 spec.
//	if err := henc.WriteField(hpack.HeaderField{Name: ":path", Value: "foo"}); err != nil {
//		t.Fatalf("Error while encoding header: %v", err)
//	}
//	if err := henc.WriteField(hpack.HeaderField{Name: ":authority", Value: "localhost"}); err != nil {
//		t.Fatalf("Error while encoding header: %v", err)
//	}
//	if err := henc.WriteField(hpack.HeaderField{Name: "content-type", Value: "application/grpc"}); err != nil {
//		t.Fatalf("Error while encoding header: %v", err)
//	}
//
//	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: 1, BlockFragment: buf.Bytes(), EndHeaders: true}); err != nil {
//		t.Fatalf("Error while writing headers: %v", err)
//	}
//	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
//	defer cancel()
//	if e, err := success.Receive(ctx); e != nil || err != nil {
//		t.Fatalf("Error in frame server should send: %v. Error receiving from channel: %v", e, err)
//	}
//}

func TestPingPong1B(t *testing.T) {
	runPingPongTest(t, 1)
}

func TestPingPong1KB(t *testing.T) {
	runPingPongTest(t, 1024)
}

func TestPingPong64KB(t *testing.T) {
	runPingPongTest(t, 65536)
}

func TestPingPong1MB(t *testing.T) {
	runPingPongTest(t, 1048576)
}

// This is a stress-test of flow control logic.
func runPingPongTest(t *testing.T, msgSize int) {
	server, client := setUp(t, 0, 0, pingpong)
	defer server.stop()
	defer client.Close(errSelfCloseForTest)
	waitWhileTrue(t, func() (bool, error) {
		server.mu.Lock()
		defer server.mu.Unlock()
		if len(server.conns) == 0 {
			return true, fmt.Errorf("timed out while waiting for server transport to be created")
		}
		return false, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer cancel()
	stream, err := client.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("Failed to create stream. Err: %v", err)
	}
	msg := make([]byte, msgSize)
	outgoingHeader := make([]byte, 5)
	outgoingHeader[0] = byte(0)
	binary.BigEndian.PutUint32(outgoingHeader[1:], uint32(msgSize))
	opts := &Options{}
	incomingHeader := make([]byte, 5)

	ctx, cancel = context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	for ctx.Err() == nil {
		if err := client.Write(stream, outgoingHeader, msg, opts); err != nil {
			t.Fatalf("Error on client while writing message. Err: %v", err)
		}
		if _, err := stream.Read(incomingHeader); err != nil {
			t.Fatalf("Error on client while reading data header. Err: %v", err)
		}
		sz := binary.BigEndian.Uint32(incomingHeader[1:])
		recvMsg := make([]byte, int(sz))
		if _, err := stream.Read(recvMsg); err != nil {
			t.Fatalf("Error on client while reading data. Err: %v", err)
		}
	}

	client.Write(stream, nil, nil, &Options{Last: true})
	if _, err := stream.Read(incomingHeader); err != io.EOF {
		t.Fatalf("Client expected EOF from the server. Got: %v", err)
	}
}

type tableSizeLimit struct {
	mu     sync.Mutex
	limits []uint32
}

func (t *tableSizeLimit) add(limit uint32) {
	t.mu.Lock()
	t.limits = append(t.limits, limit)
	t.mu.Unlock()
}

func (t *tableSizeLimit) getLen() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.limits)
}

func (t *tableSizeLimit) getIndex(i int) uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.limits[i]
}

func TestHeaderTblSize(t *testing.T) {
	limits := &tableSizeLimit{}
	updateHeaderTblSize = func(e *hpack.Encoder, v uint32) {
		e.SetMaxDynamicTableSizeLimit(v)
		limits.add(v)
	}
	defer func() {
		updateHeaderTblSize = func(e *hpack.Encoder, v uint32) {
			e.SetMaxDynamicTableSizeLimit(v)
		}
	}()

	server, ct := setUp(t, 0, math.MaxUint32, normal)
	defer ct.Close(errSelfCloseForTest)
	defer server.stop()
	ctx, ctxCancel := context.WithTimeout(context.Background(), defaultTestTimeout)
	defer ctxCancel()
	_, err := ct.NewStream(ctx, &CallHdr{})
	if err != nil {
		t.Fatalf("failed to open stream: %v", err)
	}

	var svrTransport ServerTransport
	var i int
	for i = 0; i < 1000; i++ {
		server.mu.Lock()
		if len(server.conns) != 0 {
			server.mu.Unlock()
			break
		}
		server.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		continue
	}
	if i == 1000 {
		t.Fatalf("%s", "unable to create any server transport after 10s")
	}

	for st := range server.conns {
		svrTransport = st
		break
	}
	svrTransport.(*http2Server).controlBuf.put(&outgoingSettings{
		ss: []http2.Setting{
			{
				ID:  http2.SettingHeaderTableSize,
				Val: uint32(100),
			},
		},
	})

	for i = 0; i < 1000; i++ {
		if limits.getLen() != 1 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if val := limits.getIndex(0); val != uint32(100) {
			t.Fatalf("expected limits[0] = 100, got %d", val)
		}
		break
	}
	if i == 1000 {
		t.Fatalf("%s", "expected len(limits) = 1 within 10s, got != 1")
	}

	ct.controlBuf.put(&outgoingSettings{
		ss: []http2.Setting{
			{
				ID:  http2.SettingHeaderTableSize,
				Val: uint32(200),
			},
		},
	})

	for i := 0; i < 1000; i++ {
		if limits.getLen() != 2 {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		if val := limits.getIndex(1); val != uint32(200) {
			t.Fatalf("expected limits[1] = 200, got %d", val)
		}
		break
	}
	if i == 1000 {
		t.Fatalf("%s", "expected len(limits) = 2 within 10s, got != 2")
	}
}

func TestTLSConfig(t *testing.T) {
	cfg := &tls.Config{}
	newCfg := TLSConfig(cfg)
	test.Assert(t, len(cfg.NextProtos) == 0)
	test.Assert(t, len(newCfg.NextProtos) == 1)
	test.Assert(t, newCfg.NextProtos[0] == alpnProtoStrH2)
	test.Assert(t, newCfg.MinVersion == tls.VersionTLS12)
}

func TestTlsAppendH2ToALPNProtocols(t *testing.T) {
	var ps []string
	appended := tlsAppendH2ToALPNProtocols(ps)
	test.Assert(t, len(appended) == 1)
	test.Assert(t, appended[0] == alpnProtoStrH2)
	appended = tlsAppendH2ToALPNProtocols(appended)
	test.Assert(t, len(appended) == 1)
}

func Test_isIgnorable(t *testing.T) {
	test.Assert(t, !isIgnorable(nil))

	err := connectionErrorfWithIgnorable(true, nil, "ignorable")
	test.Assert(t, isIgnorable(err))

	err = connectionErrorf(true, nil, "not ignorable")
	test.Assert(t, !isIgnorable(err))

	test.Assert(t, isIgnorable(ErrConnClosing))
}

func Test_closeStreamTask(t *testing.T) {
	// replace ticker to reduce test time
	ticker = utils.NewSyncSharedTicker(10 * time.Millisecond)
	server, ct := setUp(t, 0, math.MaxUint32, cancel)
	callHdr := &CallHdr{
		Host:   "localhost",
		Method: "foo.Small",
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := ct.NewStream(ctx, callHdr)
	if err != nil {
		t.Fatalf("failed to open stream: %v", err)
	}
	cancel()
	// wait for server receiving RstStream Frame
	<-server.srvReady
	state := stream.getState()
	test.Assert(t, state == streamDone, state)
	ct.mu.Lock()
	streamNums := len(ct.activeStreams)
	ct.mu.Unlock()
	test.Assert(t, streamNums == 0, streamNums)

	ct.Close(errSelfCloseForTest)
	server.stop()
}

func TestStreamGetHeaderValid(t *testing.T) {
	s := &Stream{
		headerChan:  make(chan struct{}),
		headerValid: true,
	}
	test.Assert(t, !s.getHeaderValid())
	s.headerChanClosed = 1
	test.Assert(t, !s.getHeaderValid())
	close(s.headerChan)
	test.Assert(t, s.getHeaderValid() == s.headerValid)
}
