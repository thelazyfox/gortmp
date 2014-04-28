// Copyright 2013, zhangpeihao All rights reserved.

package rtmp

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
)

const (
	HANDSHAKE_SIZE = 1536
	DIGEST_LENGTH  = 32
	KEY_LENGTH     = 128

	RTMP_SIG_SIZE          = 1536
	RTMP_LARGE_HEADER_SIZE = 12
	SHA256_DIGEST_LENGTH   = 32
)

var (
	GENUINE_FMS_KEY = []byte{
		0x47, 0x65, 0x6e, 0x75, 0x69, 0x6e, 0x65, 0x20,
		0x41, 0x64, 0x6f, 0x62, 0x65, 0x20, 0x46, 0x6c,
		0x61, 0x73, 0x68, 0x20, 0x4d, 0x65, 0x64, 0x69,
		0x61, 0x20, 0x53, 0x65, 0x72, 0x76, 0x65, 0x72,
		0x20, 0x30, 0x30, 0x31, // Genuine Adobe Flash Media Server 001
		0xf0, 0xee, 0xc2, 0x4a, 0x80, 0x68, 0xbe, 0xe8,
		0x2e, 0x00, 0xd0, 0xd1, 0x02, 0x9e, 0x7e, 0x57,
		0x6e, 0xec, 0x5d, 0x2d, 0x29, 0x80, 0x6f, 0xab,
		0x93, 0xb8, 0xe6, 0x36, 0xcf, 0xeb, 0x31, 0xae,
	}
	GENUINE_FP_KEY = []byte{
		0x47, 0x65, 0x6E, 0x75, 0x69, 0x6E, 0x65, 0x20,
		0x41, 0x64, 0x6F, 0x62, 0x65, 0x20, 0x46, 0x6C,
		0x61, 0x73, 0x68, 0x20, 0x50, 0x6C, 0x61, 0x79,
		0x65, 0x72, 0x20, 0x30, 0x30, 0x31, /* Genuine Adobe Flash Player 001 */
		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8,
		0x2E, 0x00, 0xD0, 0xD1, 0x02, 0x9E, 0x7E, 0x57,
		0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}
)

func calculateHMACsha256(msgBytes []byte, key []byte) ([]byte, error) {
	h := hmac.New(sha256.New, key)
	_, err := h.Write(msgBytes)
	if err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func Handshake(conn NetConn) error {
	return fmt.Errorf("not implemented")
}

func sumBytes(buf []byte) uint32 {
	var sum uint32
	for _, b := range buf {
		sum += uint32(b)
	}

	return sum
}

func getDigestOffset0(buf []byte) uint32 {
	return (sumBytes(buf[8:12]) % 728) + 12
}

func getDigestOffset1(buf []byte) uint32 {
	return (sumBytes(buf[772:776]) % 728) + 776
}

func getDigestOffset(buf []byte, scheme int) uint32 {
	switch scheme {
	case 0:
		return getDigestOffset0(buf)
	case 1:
		return getDigestOffset1(buf)
	default:
		return getDigestOffset0(buf)
	}
}

// TODO: make this pass errors along somehow
func validate(buf []byte) (int, error) {
	var errs []string

	for i := 0; i < 2; i++ {
		err := validateScheme(buf, i)
		if err != nil {
			errs = append(errs, err.Error())
		} else {
			return i, nil
		}
	}

	return 0, fmt.Errorf("all validation schemes failed: %v", errs)
}

func validateScheme(buf []byte, scheme int) error {
	var digestOffset uint32
	switch scheme {
	case 0:
		digestOffset = getDigestOffset0(buf)
	case 1:
		digestOffset = getDigestOffset1(buf)
	default:
		return fmt.Errorf("validateScheme(%d): invlaid scheme", scheme)
	}

	tempBuffer := make([]byte, HANDSHAKE_SIZE-DIGEST_LENGTH)
	copy(tempBuffer, buf[:digestOffset])
	copy(tempBuffer[digestOffset:], buf[digestOffset+DIGEST_LENGTH:])

	hash, err := calculateHMACsha256(tempBuffer, GENUINE_FP_KEY[:30])
	if err != nil {
		return fmt.Errorf("validateScheme(%d) calculateHMAC failed: %s", scheme, err)
	}

	if bytes.Compare(buf[digestOffset:digestOffset+DIGEST_LENGTH], hash) != 0 {
		return fmt.Errorf("validateScheme(%d) digest comparison failed", scheme)
	}

	return nil
}

func doSimpleHandshake(conn NetConn, c1 []byte) error {
	return fmt.Errorf("simple handshake not implemented")
}

func SHandshake(conn NetConn) error {
	logTag := fmt.Sprintf("SHandshake(%s)", conn.Conn().RemoteAddr())
	log := NewLogger(logTag)
	c0, err := conn.ReadByte()
	if err != nil {
		return err
	}

	if c0 != 0x03 {
		log.Warnf("SHandshake unsupported handshake type: %x", c0)
	}

	c1 := make([]byte, HANDSHAKE_SIZE)
	if _, err := io.ReadFull(conn, c1); err != nil {
		return err
	}
	log.Debugf("SHandshake read c1")

	response := make([]byte, 2*HANDSHAKE_SIZE+1)
	s0 := response[0:1]
	s1 := response[1 : HANDSHAKE_SIZE+1]
	s2 := response[HANDSHAKE_SIZE+1:]

	// set handshake type
	s0[0] = 0x03

	// Check the first byte of version
	v := c1[4:8]
	if v[0] == 0 {
		log.Warnf("SHandshake unversioned flash client detected: %x %x %x %x", v[0], v[1], v[2], v[3])
	}

	scheme, err := validate(c1)
	if err != nil {
		log.Warnf("SHandshake failed to get validation scheme: %s", err)
	}
	log.Debugf("SHandshake validated scheme %d", scheme)

	// prep output
	binary.BigEndian.PutUint32(s1[4:8], 0x01020304)
	if _, err := rand.Read(s1[8:]); err != nil {
		return fmt.Errorf("SHandshake failed to create S1 bytes: %s", err)
	}

	s1off := getDigestOffset(s1, scheme)
	s1bytes := make([]byte, HANDSHAKE_SIZE-DIGEST_LENGTH)
	copy(s1bytes, s1[:s1off])
	copy(s1bytes[s1off:], s1[s1off+DIGEST_LENGTH:])

	s1hash, err := calculateHMACsha256(s1bytes, GENUINE_FMS_KEY[:36])
	if err != nil {
		return fmt.Errorf("SHandshake failed to create S1 hash: %s", err)
	}

	copy(s1[s1off:], s1hash)
	c1off := getDigestOffset(c1, scheme)
	c1hash := c1[c1off : c1off+DIGEST_LENGTH]

	s2bytes := s2[:HANDSHAKE_SIZE-DIGEST_LENGTH]
	if _, err := rand.Read(s2bytes); err != nil {
		return fmt.Errorf("SHandshake failed to create S2 bytes: %s", err)
	}

	s2key, err := calculateHMACsha256(c1hash, GENUINE_FMS_KEY[:68])
	if err != nil {
		return fmt.Errorf("SHandshake failed to create S2 key: %s", err)
	}

	s2hash, err := calculateHMACsha256(s2bytes, s2key)
	if err != nil {
		return fmt.Errorf("SHandshake failed to create S2 hash: %s", err)
	}
	copy(s2[HANDSHAKE_SIZE-DIGEST_LENGTH:], s2hash)

	done := make(chan error)

	go func() {
		if _, err := conn.Write(response); err != nil {
			done <- err
			return
		}

		if err := conn.Flush(); err != nil {
			done <- err
			return
		}

		done <- nil
		log.Tracef("SHandshake response written")
	}()

	go func() {
		// ignore client response
		_, err := io.CopyN(ioutil.Discard, conn, HANDSHAKE_SIZE)
		done <- err
	}()

	if err := <-done; err != nil {
		return fmt.Errorf("SHandshake connection error: %s", err)
	}

	if err := <-done; err != nil {
		return fmt.Errorf("SHandshake connection error: %s", err)
	}

	log.Debugf("SHandshake Complete")

	return nil
}
