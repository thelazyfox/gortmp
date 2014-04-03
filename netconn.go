package rtmp

import (
	"bufio"
	"net"
	"time"
)

type NetConn interface {
	Reader
	Writer
	Flush() error
	Close() error
	Conn() net.Conn

	SetMaxIdle(time.Duration)
}

type netConn struct {
	conn net.Conn
	*bufio.Reader
	*bufio.Writer

	maxIdle time.Duration
}

func NewNetConn(conn net.Conn) NetConn {
	return &netConn{
		conn:   conn,
		Reader: bufio.NewReader(conn),
		Writer: bufio.NewWriter(conn),
	}
}

func (n *netConn) SetMaxIdle(t time.Duration) {
	n.maxIdle = t
}

func (n *netConn) Close() error {
	return n.conn.Close()
}

func (n *netConn) Conn() net.Conn {
	return n.conn
}

func (n *netConn) Read(b []byte) (int, error) {
	return n.Reader.Read(b)
}
