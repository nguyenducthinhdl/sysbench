package generate

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Catalog is the field pool and the message lines loaded before a run.
// Each of instance-id, ip-address, host-name, and service is drawn on its own
// from its 1000 values. Message is one line drawn from the masked log corpus,
// with placeholders filled in for that event.
type Catalog struct {
	InstanceIDs []string
	IPs         []string
	Hosts       []string
	Services    []string
	Messages    []string
}

const messagesLineLimit = 1_000_000

var serviceStems = []string{
	"users", "payments", "orders", "foods", "booking",
	"envoy", "client", "checkout", "inventory", "auth",
}

// servicePool is 100 names of each stem. The suffix is not the row number,
// and the list is shuffled, so the field file is not stem-0000, stem-0001, ...
func servicePool() []string {
	const seed1, seed2 = 0x5e47, 0x9a11
	rng := rand.New(rand.NewPCG(seed1, seed2))
	perStem := PoolSize / len(serviceStems)
	used := make(map[string]struct{}, PoolSize)
	names := make([]string, 0, PoolSize)
	for _, stem := range serviceStems {
		n := 0
		for n < perStem {
			name := fmt.Sprintf("%s-%04d", stem, rng.IntN(10000))
			if _, ok := used[name]; ok {
				continue
			}
			used[name] = struct{}{}
			names = append(names, name)
			n++
		}
	}
	rng.Shuffle(len(names), func(i, j int) {
		names[i], names[j] = names[j], names[i]
	})
	return names
}

var (
	reUser  = regexp.MustCompile(`\busr-\d+\b`)
	reOrder = regexp.MustCompile(`\bord-\d+\b`)
	rePay   = regexp.MustCompile(`\bpay-\d+\b`)
	reBook  = regexp.MustCompile(`\bbk-\d+\b`)
	reTrack = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
)

// WriteFields writes PoolSize rows of instance-id, ip-address, host-name, and service.
func WriteFields(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	if err := w.Write([]string{"instance-id", "ip-address", "host-name", "service"}); err != nil {
		return err
	}
	services := servicePool()
	for i := 0; i < PoolSize; i++ {
		id := identityAt(i)
		if err := w.Write([]string{id.instanceID, id.ip, id.host, services[i]}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

// LoadCatalog reads the field pool and the masked message lines.
func LoadCatalog(fieldsPath, messagesPath string) (*Catalog, error) {
	cat, err := loadFields(fieldsPath)
	if err != nil {
		return nil, err
	}
	messages, err := loadMessages(messagesPath)
	if err != nil {
		return nil, err
	}
	cat.Messages = messages
	return cat, nil
}

func loadFields(path string) (*Catalog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("fields: %w", err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = 4
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("fields: %w", err)
	}
	want := []string{"instance-id", "ip-address", "host-name", "service"}
	for i, name := range want {
		if header[i] != name {
			return nil, fmt.Errorf("fields: column %d is %q, want %q", i+1, header[i], name)
		}
	}
	cat := &Catalog{}
	for {
		row, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("fields: %w", err)
		}
		cat.InstanceIDs = append(cat.InstanceIDs, row[0])
		cat.IPs = append(cat.IPs, row[1])
		cat.Hosts = append(cat.Hosts, row[2])
		cat.Services = append(cat.Services, row[3])
	}
	for _, col := range []struct {
		name string
		n    int
	}{
		{"instance-id", len(cat.InstanceIDs)},
		{"ip-address", len(cat.IPs)},
		{"host-name", len(cat.Hosts)},
		{"service", len(cat.Services)},
	} {
		if col.n != PoolSize {
			return nil, fmt.Errorf("fields: %s has %d values, want %d", col.name, col.n, PoolSize)
		}
	}
	return cat, nil
}

func loadMessages(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("messages: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var lines []string
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("messages: %w", err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("messages: %s is empty", path)
	}
	return lines, nil
}

// Prepare writes the field pool and a message corpus.
// Each *.log file directly inside logsDir contributes at most lineLimit lines.
// Identifiers in those lines are replaced with #user-id, #order-id, #payment-id,
// #booking-id, and #track-id. The run fills those tokens when it emits an event.
func Prepare(logsDir, fieldsPath, messagesPath string, lineLimit int) (int, error) {
	if lineLimit <= 0 {
		lineLimit = messagesLineLimit
	}
	if err := WriteFields(fieldsPath); err != nil {
		return 0, err
	}
	entries, err := os.ReadDir(logsDir)
	if err != nil {
		return 0, fmt.Errorf("logs: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(messagesPath), 0o755); err != nil && filepath.Dir(messagesPath) != "." {
		return 0, err
	}
	out, err := os.Create(messagesPath)
	if err != nil {
		return 0, err
	}
	defer out.Close()
	bw := bufio.NewWriterSize(out, 1<<20)
	total := 0
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".log") {
			continue
		}
		n, err := writeMasked(filepath.Join(logsDir, ent.Name()), bw, lineLimit)
		if err != nil {
			return total, fmt.Errorf("%s: %w", ent.Name(), err)
		}
		log.Printf("prepare %s: %d lines", ent.Name(), n)
		total += n
	}
	if err := bw.Flush(); err != nil {
		return total, err
	}
	if total == 0 {
		return 0, fmt.Errorf("logs: no lines in %s", logsDir)
	}
	return total, nil
}

func writeMasked(path string, w io.Writer, limit int) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	n := 0
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if _, err := io.WriteString(w, maskLine(line)); err != nil {
			return n, err
		}
		if _, err := io.WriteString(w, "\n"); err != nil {
			return n, err
		}
		n++
		if n >= limit {
			break
		}
	}
	return n, sc.Err()
}

func maskLine(line string) string {
	line = reUser.ReplaceAllString(line, "#user-id")
	line = reOrder.ReplaceAllString(line, "#order-id")
	line = rePay.ReplaceAllString(line, "#payment-id")
	line = reBook.ReplaceAllString(line, "#booking-id")
	return reTrack.ReplaceAllString(line, "#track-id")
}

func renderMessage(rng *rand.Rand, line string) string {
	if !strings.Contains(line, "#") {
		return line
	}
	return strings.NewReplacer(
		"#user-id", fmt.Sprintf("usr-%05d", rng.IntN(100000)),
		"#order-id", fmt.Sprintf("ord-%05d", rng.IntN(100000)),
		"#payment-id", fmt.Sprintf("pay-%05d", rng.IntN(100000)),
		"#booking-id", fmt.Sprintf("bk-%05d", rng.IntN(100000)),
		"#track-id", formatUUID(rng, rng.Uint64()),
	).Replace(line)
}

func (c *Catalog) pick(rng *rand.Rand, values []string) string {
	return values[rng.IntN(len(values))]
}
