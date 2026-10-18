package proxykit

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
)

// tunnel copies bytes both ways until both directions finish.
func tunnel(client, upstream net.Conn) {
	var wg sync.WaitGroup
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		// Half-close so the peer sees EOF while the other direction drains.
		closeWrite(dst)
	}
	wg.Add(2)
	go pipe(upstream, client)
	go pipe(client, upstream)
	wg.Wait()
	client.Close()
	upstream.Close()
}

// closeWriter is implemented by *net.TCPConn and *tls.Conn.
type closeWriter interface{ CloseWrite() error }

func closeWrite(c net.Conn) {
	if cw, ok := c.(closeWriter); ok {
		cw.CloseWrite()
	}
}

// bufferedConn replays bytes the HTTP server already buffered past the CONNECT
// request (clients may send the TLS ClientHello without waiting for "200").
type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedConn) Read(b []byte) (int, error) { return c.reader.Read(b) }

// CloseWrite forwards to the underlying connection so tunnels can half-close.
func (c *bufferedConn) CloseWrite() error {
	if cw, ok := c.Conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return nil
}

// withBuffered wraps conn only when there are leftover buffered bytes.
func withBuffered(conn net.Conn, reader interface {
	Buffered() int
	Peek(int) ([]byte, error)
}) net.Conn {
	pending := reader.Buffered()
	if pending == 0 {
		return conn
	}
	leftover, _ := reader.Peek(pending)
	// Copy because Peek's slice aliases the bufio buffer, which the server may reuse.
	leftover = bytes.Clone(leftover)
	return &bufferedConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(leftover), conn)}
}

// connectConn tags an intercepted connection with its CONNECT target so
// handlers can recover it (see ConnectTarget).
type connectConn struct {
	net.Conn
	target string
}

// chanListener is a net.Listener fed by push, so hijacked CONNECT sockets can
// be served by an ordinary http.Server without a loopback socket.
type chanListener struct {
	conns     chan net.Conn
	closed    chan struct{}
	closeOnce sync.Once
}

func newChanListener() *chanListener {
	return &chanListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

var errListenerClosed = errors.New("proxykit: listener closed")

func (l *chanListener) push(c net.Conn) error {
	select {
	case l.conns <- c:
		return nil
	case <-l.closed:
		return errListenerClosed
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, errListenerClosed
	}
}

func (l *chanListener) Close() error {
	l.closeOnce.Do(func() { close(l.closed) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return inProcessAddr{} }

// inProcessAddr names the chanListener's (non-network) address.
type inProcessAddr struct{}

func (inProcessAddr) Network() string { return "inprocess" }
func (inProcessAddr) String() string  { return "proxykit-inprocess" }
