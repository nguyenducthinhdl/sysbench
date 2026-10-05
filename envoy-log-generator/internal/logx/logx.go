// Package logx writes client and backend records to disk.
//
// A record is one line, or several when the body is a stack trace:
//
//	2006-01-02T15:04:05.000000Z <name>/<ip> <MODE> <component> <message> (<body>)
//
// The timestamp is UTC. MODE is DEBUG, INFO, or ERROR. A multi-line body keeps
// the opening "(" on the first line and the closing ")" after the last line.
package logx

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"
)

// Logger appends records to a single file. It is safe for concurrent use.
type Logger struct {
	name string
	ip   string
	mu   sync.Mutex
	f    *os.File
	w    *bufio.Writer
}

// Open creates or appends to path. name and ip are written in every record.
func Open(path, name, ip string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Logger{name: name, ip: ip, f: f, w: bufio.NewWriterSize(f, 64<<10)}, nil
}

// DetectIP returns a local address suitable for the name/ip field.
// UDP dial does not transmit; it only asks the kernel which source address it would use.
func DetectIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return "0.0.0.0"
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return "0.0.0.0"
	}
	return addr.IP.String()
}

// Debug writes a DEBUG record and flushes it.
func (l *Logger) Debug(component, message, body string) {
	l.write("DEBUG", component, message, body)
}

// Info writes an INFO record and flushes it.
func (l *Logger) Info(component, message, body string) {
	l.write("INFO", component, message, body)
}

// Error writes an ERROR record and flushes it.
func (l *Logger) Error(component, message, body string) {
	l.write("ERROR", component, message, body)
}

// Panic writes the current goroutine stack as an ERROR record, syncs it to disk, then panics.
func (l *Logger) Panic(component, message string) {
	stack := strings.TrimRight(string(debug.Stack()), "\n")
	l.Error(component, message, stack)
	l.sync()
	panic(message)
}

// Close flushes and closes the file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_ = l.w.Flush()
	}
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

func (l *Logger) write(mode, component, message, body string) {
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z")
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s/%s %s %s %s (", ts, l.name, l.ip, mode, component, message)
	b.WriteString(body)
	b.WriteString(")\n")

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.WriteString(b.String())
	_ = l.w.Flush()
}

func (l *Logger) sync() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_ = l.w.Flush()
	}
	if l.f != nil {
		_ = l.f.Sync()
	}
}
