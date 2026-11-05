package generate

import (
	"fmt"
	"math/rand/v2"
	"time"
)

// Format shares. logfmt is the 10% that brings the README mix to 100%.
const (
	formatJSON       = "json"
	formatAccess     = "access"
	formatText       = "text"
	formatStacktrace = "stacktrace"
	formatAudit      = "audit"
	formatLogfmt     = "logfmt"
)

type pair struct {
	service   string
	component string
}

var (
	applicationPairs = []pair{
		{"checkout", "api"},
		{"payments", "api"},
		{"inventory", "worker"},
		{"auth", "api"},
		{"notifications", "worker"},
	}
	webPairs = []pair{
		{"edge", "envoy"},
		{"gateway", "proxy"},
		{"ingress", "nginx"},
	}
	platformPairs = []pair{
		{"kubelet", "node"},
		{"containerd", "runtime"},
		{"kernel", "os"},
	}
	dataPairs = []pair{
		{"postgres", "db"},
		{"redis", "cache"},
		{"kafka", "queue"},
	}
)

func pickFormat(p float64) string {
	switch {
	case p < 0.60:
		return formatJSON
	case p < 0.70:
		return formatAccess
	case p < 0.80:
		return formatText
	case p < 0.85:
		return formatStacktrace
	case p < 0.90:
		return formatAudit
	default:
		return formatLogfmt
	}
}

func pickSource(rng *rand.Rand) pair {
	p := rng.Float64()
	pairs := dataPairs
	switch {
	case p < 0.60:
		pairs = applicationPairs
	case p < 0.80:
		pairs = webPairs
	case p < 0.90:
		pairs = platformPairs
	}
	return pairs[rng.IntN(len(pairs))]
}

// messageCore is the format body before it is fitted to the target length.
// The service and severity tokens sit in the first 64 bytes.
func messageCore(format, service, component, severity string, status int, ip, instanceID string, now time.Time) string {
	switch format {
	case formatJSON:
		return fmt.Sprintf(`{"sev":%q,"service":%q,"component":%q,"msg":"request completed"}`, severity, service, component)
	case formatAccess:
		ts := now.UTC().Format("02/Jan/2006:15:04:05 -0700")
		return fmt.Sprintf(`%s - %s %s [%s] "GET /v1/%s HTTP/1.1" %d 128 "-" "syscon-bench"`, ip, service, severity, ts, service, status)
	case formatText:
		return fmt.Sprintf("%s %s request completed component %s", service, severity, component)
	case formatStacktrace:
		return fmt.Sprintf("%s %s exception timeout\n\tat %s.%s.Worker.run(Worker.java:42)", service, severity, service, component)
	case formatAudit:
		return fmt.Sprintf("audit %s %s service=%s actor=%s action=access result=%d", service, severity, service, instanceID, status)
	default:
		return fmt.Sprintf(`service=%s component=%s severity=%s status=%d msg="request completed"`, service, component, severity, status)
	}
}

// fitMessage returns a string of exactly target bytes.
// A short body is extended with the ASCII sequence " x". A long body keeps
// the leading bytes, which still hold the service and severity tokens.
func fitMessage(body string, target int) string {
	if target <= 0 {
		return ""
	}
	if len(body) == target {
		return body
	}
	if len(body) > target {
		return body[:target]
	}
	out := make([]byte, target)
	copy(out, body)
	pad := []byte(" x")
	filled := len(body)
	for filled < target {
		n := copy(out[filled:], pad)
		filled += n
		if len(pad) < 4096 && filled < target {
			next := make([]byte, len(pad)*2)
			copy(next, pad)
			copy(next[len(pad):], pad)
			pad = next
		}
	}
	return string(out)
}
