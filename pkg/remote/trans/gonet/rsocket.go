package gonet

/*
   #cgo CFLAGS: -I/usr/include
   #cgo LDFLAGS: -lrdmacm -libverbs
   #include <rdma/rsocket.h>
*/
import "C"
import (
	"log"
	"net"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Socket domain constants
const (
	AF_INET  = C.AF_INET
	AF_INET6 = C.AF_INET6
)

// Socket type constants
const (
	SOCK_STREAM = C.SOCK_STREAM
	SOCK_DGRAM  = C.SOCK_DGRAM
)

// Protocol constants
const (
	IPPROTO_TCP = C.IPPROTO_TCP
	IPPROTO_UDP = C.IPPROTO_UDP
)

// RDMA specific socket options
const (
	SOL_RDMA    = C.SOL_RDMA
	RDMA_SQSIZE = C.RDMA_SQSIZE
	RDMA_RQSIZE = C.RDMA_RQSIZE
	RDMA_INLINE = C.RDMA_INLINE
	RDMA_ROUTE  = C.RDMA_ROUTE
)

// Socket option constants
const (
	// Socket level options
	SOL_SOCKET = syscall.SOL_SOCKET

	// Socket options
	SO_REUSEADDR = syscall.SO_REUSEADDR
	TCP_NODELAY  = syscall.TCP_NODELAY
	SO_ERROR     = syscall.SO_ERROR
	SO_SNDBUF    = syscall.SO_SNDBUF
	SO_RCVBUF    = syscall.SO_RCVBUF

	// RDMA specific options
	O_NONBLOCK = syscall.O_NONBLOCK
)

// Socket creates a new RDMA socket
func Socket(domain, typ, protocol int) (int, error) {
	fd := C.rsocket(C.int(domain), C.int(typ), C.int(protocol))
	if fd < 0 {
		return -1, syscall.Errno(-fd)
	}
	return int(fd), nil
}

// Bind binds the socket to the given address
func Bind(fd int, sa syscall.Sockaddr) error {
	ptr, len, err := sockaddrToAny(sa)
	if err != nil {
		return err
	}
	if rc := C.rbind(C.int(fd), (*C.struct_sockaddr)(unsafe.Pointer(ptr)), C.socklen_t(len)); rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// Listen marks the socket as a passive socket
func Listen(fd int, backlog int) error {
	if rc := C.rlisten(C.int(fd), C.int(backlog)); rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// Accept accepts a connection on the given socket
func Accept(fd int) (int, syscall.Sockaddr, error) {
	var (
		addr syscall.RawSockaddrAny
		len  = C.socklen_t(syscall.SizeofSockaddrAny)
	)
	nfd := C.raccept(C.int(fd), (*C.struct_sockaddr)(unsafe.Pointer(&addr)), &len)
	if nfd < 0 {
		return -1, nil, syscall.Errno(-nfd)
	}
	sa, err := anyToSockaddr(&addr)
	if err != nil {
		return -1, nil, err
	}
	return int(nfd), sa, nil
}

// Connect connects the socket to a remote address
func Connect(fd int, sa syscall.Sockaddr) error {
	ptr, len, err := sockaddrToAny(sa)
	if err != nil {
		return err
	}
	if rc := C.rconnect(C.int(fd), (*C.struct_sockaddr)(unsafe.Pointer(ptr)), C.socklen_t(len)); rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// Read reads data from the socket
func Read(fd int, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := C.rread(C.int(fd), unsafe.Pointer(&p[0]), C.size_t(len(p)))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// RecvFrom receives data from a specific address
func RecvFrom(fd int, p []byte, flags int) (int, syscall.Sockaddr, error) {
	if len(p) == 0 {
		return 0, nil, nil
	}
	var addr syscall.RawSockaddrAny
	var addrlen C.socklen_t = C.socklen_t(syscall.SizeofSockaddrAny)
	n := C.rrecvfrom(C.int(fd), unsafe.Pointer(&p[0]), C.size_t(len(p)), C.int(flags),
		(*C.struct_sockaddr)(unsafe.Pointer(&addr)), &addrlen)
	if n < 0 {
		return 0, nil, syscall.Errno(-n)
	}
	sa, err := anyToSockaddr(&addr)
	if err != nil {
		return 0, nil, err
	}
	return int(n), sa, nil
}

// RecvMsg receives a message from the socket
func RecvMsg(fd int, msg *syscall.Msghdr, flags int) (int, error) {
	n := C.rrecvmsg(C.int(fd), (*C.struct_msghdr)(unsafe.Pointer(msg)), C.int(flags))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// SendTo sends data to a specific address
func SendTo(fd int, p []byte, flags int, sa syscall.Sockaddr) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	ptr, l, err := sockaddrToAny(sa)
	if err != nil {
		return 0, err
	}
	n := C.rsendto(C.int(fd), unsafe.Pointer(&p[0]), C.size_t(len(p)), C.int(flags),
		(*C.struct_sockaddr)(unsafe.Pointer(ptr)), C.socklen_t(l))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// SendMsg sends a message on the socket
func SendMsg(fd int, msg *syscall.Msghdr, flags int) (int, error) {
	n := C.rsendmsg(C.int(fd), (*C.struct_msghdr)(unsafe.Pointer(msg)), C.int(flags))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// Write writes data to the socket
func Write(fd int, p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	n := C.rwrite(C.int(fd), unsafe.Pointer(&p[0]), C.size_t(len(p)))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// Writev writes multiple buffers to the socket
func Writev(fd int, iov []syscall.Iovec) (int, error) {
	if len(iov) == 0 {
		return 0, nil
	}
	n := C.rwritev(C.int(fd), (*C.struct_iovec)(unsafe.Pointer(&iov[0])), C.int(len(iov)))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// Close closes the socket
func Close(fd int) error {
	if rc := C.rclose(C.int(fd)); rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// SetSockOpt sets a socket option
func SetSockOpt(fd, level, opt int, value unsafe.Pointer, len uint32) error {
	if rc := C.rsetsockopt(C.int(fd), C.int(level), C.int(opt), value, C.socklen_t(len)); rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// GetSockOpt gets a socket option
func GetSockOpt(fd, level, opt int, value unsafe.Pointer, len *uint32) error {
	l := C.socklen_t(*len)
	if rc := C.rgetsockopt(C.int(fd), C.int(level), C.int(opt), value, &l); rc < 0 {
		return syscall.Errno(-rc)
	}
	*len = uint32(l)
	return nil
}

// SetSockOptInt sets an integer socket option
func SetSockOptInt(fd, level, opt, value int) error {
	val := C.int(value)
	return SetSockOpt(fd, level, opt, unsafe.Pointer(&val), uint32(unsafe.Sizeof(val)))
}

// GetSockOptInt gets an integer socket option
func GetSockOptInt(fd, level, opt int) (int, error) {
	var (
		value C.int
		len   = uint32(unsafe.Sizeof(value))
	)
	if err := GetSockOpt(fd, level, opt, unsafe.Pointer(&value), &len); err != nil {
		return 0, err
	}
	return int(value), nil
}

// SetReuseAddr sets SO_REUSEADDR option
func SetReuseAddr(fd int, value bool) error {
	intValue := 0
	if value {
		intValue = 1
	}
	return SetSockOptInt(fd, SOL_SOCKET, SO_REUSEADDR, intValue)
}

// SetTCPNoDelay sets TCP_NODELAY option
func SetTCPNoDelay(fd int, value bool) error {
	intValue := 0
	if value {
		intValue = 1
	}
	return SetSockOptInt(fd, IPPROTO_TCP, TCP_NODELAY, intValue)
}

// SetSendBuffer sets SO_SNDBUF option
func SetSendBuffer(fd int, value int) error {
	return SetSockOptInt(fd, SOL_SOCKET, SO_SNDBUF, value)
}

// SetRecvBuffer sets SO_RCVBUF option
func SetRecvBuffer(fd int, value int) error {
	return SetSockOptInt(fd, SOL_SOCKET, SO_RCVBUF, value)
}

// GetSocketError gets SO_ERROR option
func GetSocketError(fd int) error {
	errcode, err := GetSockOptInt(fd, SOL_SOCKET, SO_ERROR)
	if err != nil {
		return err
	}
	if errcode != 0 {
		return syscall.Errno(errcode)
	}
	return nil
}

// SetRDMASQSize sets RDMA send queue size
func SetRDMASQSize(fd int, value int) error {
	return SetSockOptInt(fd, SOL_RDMA, RDMA_SQSIZE, value)
}

// SetRDMARQSize sets RDMA receive queue size
func SetRDMARQSize(fd int, value int) error {
	return SetSockOptInt(fd, SOL_RDMA, RDMA_RQSIZE, value)
}

// SetRDMAInline sets RDMA inline size
func SetRDMAInline(fd int, value int) error {
	return SetSockOptInt(fd, SOL_RDMA, RDMA_INLINE, value)
}

// sockaddrToAny converts a syscall.Sockaddr to a syscall.RawSockaddrAny
func sockaddrToAny(sa syscall.Sockaddr) (*syscall.RawSockaddrAny, uint32, error) {
	if sa == nil {
		return nil, 0, syscall.EINVAL
	}

	switch sa := sa.(type) {
	case *syscall.SockaddrInet4:
		raw := syscall.RawSockaddrInet4{
			Family: syscall.AF_INET,
			Port:   uint16((sa.Port >> 8) | ((sa.Port & 0xff) << 8)), // network byte order
		}
		copy(raw.Addr[:], sa.Addr[:])
		return (*syscall.RawSockaddrAny)(unsafe.Pointer(&raw)), syscall.SizeofSockaddrInet4, nil

	case *syscall.SockaddrInet6:
		raw := syscall.RawSockaddrInet6{
			Family:   syscall.AF_INET6,
			Port:     uint16((sa.Port >> 8) | ((sa.Port & 0xff) << 8)), // network byte order
			Flowinfo: sa.ZoneId,
		}
		copy(raw.Addr[:], sa.Addr[:])
		return (*syscall.RawSockaddrAny)(unsafe.Pointer(&raw)), syscall.SizeofSockaddrInet6, nil

	default:
		return nil, 0, syscall.EAFNOSUPPORT
	}
}

// anyToSockaddr converts a syscall.RawSockaddrAny to a syscall.Sockaddr
func anyToSockaddr(rsa *syscall.RawSockaddrAny) (syscall.Sockaddr, error) {
	if rsa == nil {
		return nil, syscall.EINVAL
	}

	switch rsa.Addr.Family {
	case syscall.AF_INET:
		pp := (*syscall.RawSockaddrInet4)(unsafe.Pointer(rsa))
		sa := &syscall.SockaddrInet4{
			Port: int(pp.Port<<8 | pp.Port>>8), // network byte order
		}
		copy(sa.Addr[:], pp.Addr[:])
		return sa, nil

	case syscall.AF_INET6:
		pp := (*syscall.RawSockaddrInet6)(unsafe.Pointer(rsa))
		sa := &syscall.SockaddrInet6{
			Port:   int(pp.Port<<8 | pp.Port>>8), // network byte order
			ZoneId: pp.Scope_id,
		}
		copy(sa.Addr[:], pp.Addr[:])
		return sa, nil

	default:
		return nil, syscall.EAFNOSUPPORT
	}
}

// GetPeerName gets the address of the peer connected to the socket
func GetPeerName(fd int) (syscall.Sockaddr, error) {
	var (
		addr syscall.RawSockaddrAny
		len  = C.socklen_t(syscall.SizeofSockaddrAny)
	)
	if rc := C.rgetpeername(C.int(fd), (*C.struct_sockaddr)(unsafe.Pointer(&addr)), &len); rc < 0 {
		return nil, syscall.Errno(-rc)
	}
	return anyToSockaddr(&addr)
}

// GetSockName gets the local address of the socket
func GetSockName(fd int) (syscall.Sockaddr, error) {
	var (
		addr syscall.RawSockaddrAny
		len  = C.socklen_t(syscall.SizeofSockaddrAny)
	)
	if rc := C.rgetsockname(C.int(fd), (*C.struct_sockaddr)(unsafe.Pointer(&addr)), &len); rc < 0 {
		return nil, syscall.Errno(-rc)
	}
	return anyToSockaddr(&addr)
}

// Poll polls the file descriptors
func Poll(fds []unix.PollFd, timeout int) (int, error) {
	n := C.rpoll((*C.struct_pollfd)(unsafe.Pointer(&fds[0])), C.nfds_t(len(fds)), C.int(timeout))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// Select waits for some file descriptors to become ready to perform I/O
func Select(nfds int, readfds, writefds, exceptfds *syscall.FdSet, timeout *syscall.Timeval) (int, error) {
	n := C.rselect(C.int(nfds), (*C.fd_set)(unsafe.Pointer(readfds)), (*C.fd_set)(unsafe.Pointer(writefds)),
		(*C.fd_set)(unsafe.Pointer(exceptfds)), (*C.struct_timeval)(unsafe.Pointer(timeout)))
	if n < 0 {
		return 0, syscall.Errno(-n)
	}
	return int(n), nil
}

// Iomap maps a file or device into memory
func Iomap(fd int, buf []byte, prot int, flags int, offset int64) (int64, error) {
	ptr := unsafe.Pointer(&buf[0])
	rc := C.riomap(C.int(fd), ptr, C.size_t(len(buf)), C.int(prot), C.int(flags), C.off_t(offset))
	if rc == ^C.off_t(0) {
		return 0, syscall.Errno(-rc)
	}
	return int64(rc), nil
}

// Iounmap unmaps a file or device from memory
func Iounmap(fd int, buf []byte) error {
	ptr := unsafe.Pointer(&buf[0])
	rc := C.riounmap(C.int(fd), ptr, C.size_t(len(buf)))
	if rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// Iowrite writes data to a file or device at a specific offset
func Iowrite(fd int, buf []byte, offset int64, flags int) (int, error) {
	ptr := unsafe.Pointer(&buf[0])
	rc := C.riowrite(C.int(fd), ptr, C.size_t(len(buf)), C.off_t(offset), C.int(flags))
	if rc < 0 {
		return 0, syscall.Errno(-rc)
	}
	return int(rc), nil
}

var _ net.Conn = (*TCPConn)(nil)
var _ net.Listener = (*TCPListener)(nil)

type OptionSocketFn func(fd int) error

func WithLocalAddr(ip string, port int) OptionSocketFn {
	return func(fd int) error {
		srcAddr := net.ParseIP(ip)
		sa := &syscall.SockaddrInet4{
			Port: port,
		}
		copy(sa.Addr[:], srcAddr.To4())

		return Bind(fd, sa)
	}
}

// TCPListener is a TCP network listener baseded on rsocket.
type TCPListener struct {
	ip      string
	port    int
	tcpAddr *net.TCPAddr
	fd      int
}

type TCPConn struct {
	fd         int
	localAddr  *net.TCPAddr
	remoteAddr *net.TCPAddr
}

// NewTCPListener creates a new TCPListener.
// It binds the listener to the given ip and port.
func NewTCPListener(ip string, port int, backlog int, optFns ...OptionSocketFn) (*TCPListener, error) {
	fd, err := Socket(AF_INET, SOCK_STREAM, 0)
	if err != nil {
		log.Fatal(err)
	}

	for _, optFn := range optFns {
		err = optFn(fd)
		if err != nil {
			Close(fd)
			return nil, err
		}
	}

	srcAddr := net.ParseIP(ip)
	sa := &syscall.SockaddrInet4{
		Port: port,
	}
	copy(sa.Addr[:], srcAddr.To4())

	if err := Bind(fd, sa); err != nil {
		return nil, err
	}

	if err := Listen(fd, backlog); err != nil {
		return nil, err
	}

	localAddr := &net.TCPAddr{
		IP:   srcAddr,
		Port: port,
	}

	return &TCPListener{
		ip:      ip,
		port:    port,
		fd:      fd,
		tcpAddr: localAddr,
	}, nil
}

// Accept waits for and returns the next connection to the listener.
func (l *TCPListener) Accept() (net.Conn, error) {
	fd, addr, err := Accept(l.fd)
	if err != nil {
		return nil, err
	}
	sa := addr.(*syscall.SockaddrInet4)
	ip := net.IPv4(sa.Addr[0], sa.Addr[1], sa.Addr[2], sa.Addr[3])
	port := sa.Port

	remoteAddr := &net.TCPAddr{
		IP:   ip,
		Port: port,
	}

	conn := &TCPConn{
		fd:         fd,
		localAddr:  l.tcpAddr,
		remoteAddr: remoteAddr,
	}

	return conn, nil
}

// Close closes the listener.
func (l *TCPListener) Close() error {
	return Close(l.fd)
}

// Addr returns the listener's network address.
func (l *TCPListener) Addr() net.Addr {
	return l.tcpAddr
}

// File returns the listener's file descriptor.
func (l *TCPListener) File() int {
	return l.fd
}

// DialTCP connects to the address on the named network based on rsocket.
func DialTCP(address string, optFns ...OptionSocketFn) (*TCPConn, error) {
	fd, err := Socket(AF_INET, SOCK_STREAM, 0)
	if err != nil {
		log.Fatal(err)
	}

	for _, optFn := range optFns {
		err = optFn(fd)
		if err != nil {
			Close(fd)
			return nil, err
		}
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}

	sa := &syscall.SockaddrInet4{
		Port: tcpAddr.Port,
	}
	copy(sa.Addr[:], tcpAddr.IP.To4())

	if err := Connect(fd, sa); err != nil {
		return nil, err
	}

	conn := &TCPConn{
		fd:         fd,
		localAddr:  nil,
		remoteAddr: tcpAddr,
	}

	return conn, nil
}

// File returns the connection's file descriptor.
func (c *TCPConn) File() int {
	return c.fd
}

// Read reads data from the connection.
func (c *TCPConn) Read(p []byte) (int, error) {
	return Read(c.fd, p)
}

// Write writes data to the connection.
func (c *TCPConn) Write(p []byte) (int, error) {
	return Write(c.fd, p)
}

// Close closes the connection.
func (c *TCPConn) Close() error {
	return Close(c.fd)
}

// LocalAddr returns the local network address.
func (c *TCPConn) LocalAddr() net.Addr {
	return c.localAddr
}

// RemoteAddr returns the remote network address.
func (c *TCPConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// SetDeadline sets the read and write deadlines associated with the connection.
// not implementation.
func (c *TCPConn) SetDeadline(time.Time) error {
	return nil
}

// SetReadDeadline sets the read deadline on the connection.
// not implementation.
func (c *TCPConn) SetReadDeadline(time.Time) error {
	return nil
}

// SetWriteDeadline sets the write deadline on the connection.
// not implementation.
func (c *TCPConn) SetWriteDeadline(time.Time) error {
	return nil
}
