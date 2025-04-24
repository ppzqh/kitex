//go:build !windows

/*
 * Copyright 2025 CloudWeGo Authors
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
 */

package gonet

import (
	"net"
	"testing"
	"time"

	"github.com/cloudwego/kitex/internal/test"
)

func TestConnIsActive(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		panic(err)
	}
	defer ln.Close()

	var sc, cc *net.TCPConn
	done := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			panic(err)
		}
		sc = conn.(*net.TCPConn)
		close(done)
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	test.Assert(t, err == nil, err)
	cc = conn.(*net.TCPConn)
	<-done
	defer func() {
		cc.Close()
		sc.Close()
	}()

	test.Assert(t, connIsActive(sc) == nil)
	test.Assert(t, connIsActive(cc) == nil)
	sc.Close() // close server connection
	time.Sleep(100 * time.Millisecond)
	test.Assert(t, connIsActive(cc) != nil)
	test.Assert(t, connIsActive(sc) != nil)
}
