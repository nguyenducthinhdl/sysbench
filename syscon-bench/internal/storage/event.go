package storage

import "time"

// Event is the canonical log record. JSON names are the contract field names.
type Event struct {
	Message     string `json:"message"`
	UUID        string `json:"uuid"`
	InstanceID  string `json:"instance-id"`
	IPAddress   string `json:"ip-address"`
	HostName    string `json:"host-name"`
	LogSeverity string `json:"log-severity"`
	StatusCode  int    `json:"status-code"`
	Component   string `json:"component"`
	TraceID     string `json:"trace-id"`
	Version     string `json:"version"`
	Service     string `json:"service"`
	Time        string `json:"time"`
	JiraID      string `json:"jira-id"`
	GroupID     string `json:"group-id"`
	External01  string `json:"external-01"`
	External02  string `json:"external-02"`
	External03  string `json:"external-03"`
	BinaryField []byte `json:"binary-field"`
}

// QueryClass is the search mix id.
type QueryClass string

const (
	QueryFullText    QueryClass = "fulltext"
	QueryExact       QueryClass = "exact"
	QueryFilter      QueryClass = "filter"
	QueryAggregation QueryClass = "aggregation"
)

// Query is one search.
// Full-text sets Token. Exact and filter set Field and Value.
// Filter also sets the half-open window [From, To) on event time and Service
// from the same accepted event. status-code is a decimal string. Exact leaves
// From and To zero.
type Query struct {
	Class   QueryClass
	Token   string
	Field   string
	Value   string
	Service string
	From    time.Time
	To      time.Time
}

// Result is the search response. Hits is logged by the query client.
type Result struct {
	Hits int
	Body []byte
}
