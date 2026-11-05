package generate

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"time"

	"syscon-bench/internal/scenario"
	"syscon-bench/internal/storage"
)

const recordVersion = "1.0.0"

// newEvent builds one record. error_rate marks log-severity and status-code.
// It does not fail the event. seq keeps uuid and trace-id unique.
func newEvent(rng *rand.Rand, cat *Catalog, minSize, maxSize int, seg scenario.Segment, seq uint64, now time.Time) storage.Event {
	if maxSize < minSize {
		maxSize = minSize
	}
	if minSize < 1 {
		minSize = 1
	}
	var id identity
	var service, component string
	var msg string
	if cat != nil {
		id = identity{
			instanceID: cat.pick(rng, cat.InstanceIDs),
			ip:         cat.pick(rng, cat.IPs),
			host:       cat.pick(rng, cat.Hosts),
		}
		service = cat.pick(rng, cat.Services)
		component = pickSource(rng).component
		msg = renderMessage(rng, cat.Messages[rng.IntN(len(cat.Messages))])
	} else {
		id = identities[rng.IntN(len(identities))]
		severity := "info"
		status := 200
		if rng.Float64() < seg.ErrorRate {
			severity, status = "error", 500
		}
		format := pickFormat(rng.Float64())
		src := pickSource(rng)
		service, component = src.service, src.component
		target := minSize + rng.IntN(maxSize-minSize+1)
		msg = fitMessage(messageCore(format, service, component, severity, status, id.ip, id.instanceID, now), target)
		return finishEvent(rng, seq, now, id, service, component, severity, status, msg)
	}
	severity := "info"
	status := 200
	if rng.Float64() < seg.ErrorRate {
		severity, status = "error", 500
	}
	return finishEvent(rng, seq, now, id, service, component, severity, status, msg)
}

func finishEvent(rng *rand.Rand, seq uint64, now time.Time, id identity, service, component, severity string, status int, msg string) storage.Event {
	var bin [8]byte
	binary.BigEndian.PutUint64(bin[:], seq)
	return storage.Event{
		Message:     msg,
		UUID:        formatUUID(rng, seq),
		InstanceID:  id.instanceID,
		IPAddress:   id.ip,
		HostName:    id.host,
		LogSeverity: severity,
		StatusCode:  status,
		Component:   component,
		TraceID:     formatTrace(rng, seq),
		Version:     recordVersion,
		Service:     service,
		Time:        formatTime(now),
		JiraID:      fmt.Sprintf("SYS-%04d", rng.IntN(1000)),
		GroupID:     fmt.Sprintf("group-%02d", rng.IntN(20)),
		External01:  fmt.Sprintf("ext-%02d", rng.IntN(50)),
		External02:  externalTier(rng.IntN(3)),
		External03:  fmt.Sprintf("shard-%02d", rng.IntN(10)),
		BinaryField: append([]byte(nil), bin[:]...),
	}
}

func externalTier(n int) string {
	switch n {
	case 0:
		return "alpha"
	case 1:
		return "beta"
	default:
		return "gamma"
	}
}

func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func formatUUID(rng *rand.Rand, n uint64) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], rng.Uint64())
	binary.BigEndian.PutUint64(b[8:], rng.Uint64())
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	// The last 6 bytes are the counter, so ids stay unique after the version bits.
	b[10] = byte(n >> 40)
	b[11] = byte(n >> 32)
	b[12] = byte(n >> 24)
	b[13] = byte(n >> 16)
	b[14] = byte(n >> 8)
	b[15] = byte(n)
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func formatTrace(rng *rand.Rand, n uint64) string {
	var b [16]byte
	binary.BigEndian.PutUint64(b[:8], rng.Uint64())
	binary.BigEndian.PutUint64(b[8:], n)
	return hex.EncodeToString(b[:])
}
