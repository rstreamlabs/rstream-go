// See LICENSE file in the project root for license information.

package webtty

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync/atomic"
	"time"
)

type sessionIDGenerator struct {
	machineID [3]byte
	processID uint16
	counter   atomic.Uint32
}

func newSessionIDGenerator() *sessionIDGenerator {
	g := &sessionIDGenerator{processID: uint16(os.Getpid())}
	_, _ = rand.Read(g.machineID[:])
	var seed [4]byte
	_, _ = rand.Read(seed[:])
	g.counter.Store(uint32(seed[0])<<16 | uint32(seed[1])<<8 | uint32(seed[2]))
	return g
}

func (g *sessionIDGenerator) Generate() string {
	var raw [12]byte
	ts := uint32(time.Now().Unix())
	raw[0] = byte(ts >> 24)
	raw[1] = byte(ts >> 16)
	raw[2] = byte(ts >> 8)
	raw[3] = byte(ts)
	copy(raw[4:7], g.machineID[:])
	raw[7] = byte(g.processID >> 8)
	raw[8] = byte(g.processID)
	counter := g.counter.Add(1) & 0xFFFFFF
	raw[9] = byte(counter >> 16)
	raw[10] = byte(counter >> 8)
	raw[11] = byte(counter)
	return hex.EncodeToString(raw[:])
}
