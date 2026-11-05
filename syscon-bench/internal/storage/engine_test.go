package storage

import (
	"testing"
	"time"
)

func sampleEvent() Event {
	return Event{
		Message:     "checkout info request",
		UUID:        "11111111-1111-4111-8111-111111111111",
		InstanceID:  "instance-0000",
		IPAddress:   "10.0.0.10",
		HostName:    "host-0000",
		LogSeverity: "info",
		StatusCode:  200,
		Component:   "api",
		TraceID:     "0123456789abcdef0123456789abcdef",
		Version:     "1.0.0",
		Service:     "checkout",
		Time:        "2026-11-02T11:24:00.000Z",
		JiraID:      "SYS-0001",
		GroupID:     "group-01",
		External01:  "ext-01",
		External02:  "alpha",
		External03:  "shard-01",
		BinaryField: []byte{0, 0, 0, 0, 0, 0, 0, 1},
	}
}

func TestEndpointURL(t *testing.T) {
	u, err := endpointURL("http://host:9200/", "/logs/_bulk")
	if err != nil {
		t.Fatal(err)
	}
	if u.String() != "http://host:9200/logs/_bulk" {
		t.Fatalf("%s", u)
	}
	u, err = endpointURL("http://host:8123/prefix", "/")
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/prefix" {
		t.Fatalf("%s", u)
	}
}

func TestPromVectorHits(t *testing.T) {
	n, err := promVectorHits([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"value":[1,"2"]},{"value":[1,"5"]}]}}`))
	if err != nil || n != 7 {
		t.Fatalf("n %d err %v", n, err)
	}
	n, err = promVectorHits([]byte(`{"status":"success","data":{"result":[]}}`))
	if err != nil || n != 0 {
		t.Fatalf("empty n %d err %v", n, err)
	}
	if _, err := promVectorHits([]byte(`{"status":"error","error":"bad query"}`)); err == nil || err.Error() != "bad query" {
		t.Fatalf("%v", err)
	}
}

func TestQuoteLiteral(t *testing.T) {
	if got := quoteLiteral(`a"b\c`); got != `"a\"b\\c"` {
		t.Fatalf("%s", got)
	}
}

func filterQuery() Query {
	from := time.Date(2026, 11, 2, 11, 23, 0, 0, time.UTC)
	return Query{
		Class: QueryFilter,
		Field: "service",
		Value: "checkout",
		From:  from,
		To:    from.Add(time.Minute),
	}
}
