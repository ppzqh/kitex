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

package grpc

import (
	"net"
	"testing"

	"github.com/cloudwego/gopkg/bufiox"

	"github.com/cloudwego/kitex/internal/test"
)

type mockConnWithBufioxReader struct {
	net.Conn
	bufioxReader bufiox.Reader
}

func (c *mockConnWithBufioxReader) Reader() bufiox.Reader {
	return c.bufioxReader
}

func TestNewFramer(t *testing.T) {
	// conn without bufiox reader
	var conn net.Conn
	conn = &mockConn{}
	fr := newFramer(conn, 0, 0, 0)
	_, ok := fr.reader.(*bufiox.DefaultReader)
	test.Assert(t, ok)

	// conn with bufiox reader
	reader := &bufiox.DefaultReader{}
	conn = &mockConnWithBufioxReader{bufioxReader: reader}
	fr = newFramer(conn, 0, 0, 0)
	test.Assert(t, fr.reader == reader)

	// netpoll conn
	conn = &mockNetpollConn{}
	fr = newFramer(conn, 0, 0, 0)
	_, ok = fr.reader.(*np2BufioxReader)
	test.Assert(t, ok)
}
