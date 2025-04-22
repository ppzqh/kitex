package gonet

import (
	"errors"
	"io"
	"net"
	"sync/atomic"
	"syscall"

	"github.com/cloudwego/gopkg/bufiox"
)

// bufioConn implements the net.Conn interface.
// read via bufiox.Reader and write directly to the connection.
type bufioConn struct {
	net.Conn
	r      bufiox.Reader
	closed atomic.Bool
}

func newBufioConn(c net.Conn) *bufioConn {
	r := readerPool.Get().(*bufiox.DefaultReader)
	r.SetReader(c)
	return &bufioConn{Conn: c, r: r}
}

func (bc *bufioConn) Read(b []byte) (int, error) {
	return bc.r.ReadBinary(b)
}

func (bc *bufioConn) Close() error {
	if bc.closed.CompareAndSwap(false, true) {
		bc.r.Release(nil)
		return bc.Conn.Close()
	}
	return nil
}
func (bc *bufioConn) Reader() bufiox.Reader {
	return bc.r
}

func (bc *bufioConn) IsActive() bool {
	if connIsActive(bc.Conn) != nil {
		return false
	}
	return true
}

var (
	errUnexpectedRead = errors.New("unexpected read from socket")
	errNotSyscallConn = errors.New("conn is not a syscall.Conn")
)

// connIsActive checks if the connection is still alive using syscall.Read.
// Notice: DO NOT call when there is concurrent read on this conn.
// FIXME: windows cannot use this.
func connIsActive(conn net.Conn) error {
	var sysErr error

	sysConn, ok := conn.(syscall.Conn)
	if !ok {
		return errNotSyscallConn
	}
	rawConn, err := sysConn.SyscallConn()
	if err != nil {
		return err
	}

	err = rawConn.Read(func(fd uintptr) bool {
		var buf [1]byte
		n, err := syscall.Read(int(fd), buf[:])
		switch {
		case n == 0 && err == nil:
			sysErr = io.EOF
		case n > 0:
			sysErr = errUnexpectedRead
		case errors.Is(err, syscall.EAGAIN) || err == syscall.EWOULDBLOCK:
			sysErr = nil
		default:
			sysErr = err
		}
		return true
	})

	if err != nil {
		return err
	}

	if sysErr != nil {
		return sysErr
	}

	return nil
}
