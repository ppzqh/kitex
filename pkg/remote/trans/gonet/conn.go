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
	"sync/atomic"

	"github.com/cloudwego/gopkg/bufiox"
)

// cliConn adds IsActive function which is used to check the connection before putting back to the conn pool.
type cliConn struct {
	net.Conn
}

func (c *cliConn) IsActive() bool {
	return connIsActive(c.Conn) == nil
}

// svrConn implements the net.Conn interface.
// read via bufiox.Reader and write directly to the connection.
type svrConn struct {
	net.Conn
	r      bufiox.Reader
	closed atomic.Bool
}

func newSvrConn(c net.Conn) *svrConn {
	r := readerPool.Get().(*bufiox.DefaultReader)
	r.SetReader(c)
	return &svrConn{Conn: c, r: r}
}

func (bc *svrConn) Read(b []byte) (int, error) {
	return bc.r.ReadBinary(b)
}

func (bc *svrConn) Close() error {
	if bc.closed.CompareAndSwap(false, true) {
		bc.r.Release(nil)
		return bc.Conn.Close()
	}
	return nil
}

func (bc *svrConn) Reader() bufiox.Reader {
	return bc.r
}

func (bc *svrConn) IsActive() bool {
	return connIsActive(bc.Conn) == nil
}
