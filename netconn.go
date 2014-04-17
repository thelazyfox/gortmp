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

type netCounter struct {
	net.Conn
	inCounter  *Counter
	outCounter *Counter
}

type NetCounter interface {
	net.Conn
}

func (nc *netCounter) Read(b []byte) (int, error) {
	n, err := nc.Conn.Read(b)
	nc.inCounter.Add(int64(n))
	return n, err
}

func (nc *netCounter) Write(b []byte) (int, error) {
	n, err := nc.Conn.Write(b)
	nc.outCounter.Add(int64(n))
	return n, err
}

func NewNetConn(conn net.Conn, inCounter *Counter, outCounter *Counter) NetConn {
	netcounter := &netCounter{
		Conn:       conn,
		inCounter:  inCounter,
		outCounter: outCounter,
	}

	return &netConn{
		conn:   netcounter,
		Reader: bufio.NewReader(netcounter),
		Writer: bufio.NewWriter(netcounter),
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
