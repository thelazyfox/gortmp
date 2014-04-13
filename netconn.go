package rtmp

import (
	"io"
	"net"
	"time"
)

type NetConn interface {
	net.Conn
	io.ByteReader
	io.ByteWriter
	Flush() error

	SetMaxIdle(time.Duration)
}

type netConn struct {
	net.Conn
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
		Conn: netcounter,
	}
}

func (n *netConn) Flush() error {
	return nil
}

func (n *netConn) ReadByte() (byte, error) {
	b := [1]byte{}
	_, err := n.Read(b[:])
	return b[0], err
}

func (n *netConn) SetMaxIdle(t time.Duration) {
	n.maxIdle = t
}

func (n *netConn) WriteByte(b byte) error {
	buf := [1]byte{b}
	_, err := n.Write(buf[:])
	return err
}
