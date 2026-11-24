package storage

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func checkEndpoint(engine, endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("%s: endpoint must be an absolute http or https URL", engine)
	}
	return nil
}

// endpointURL joins path onto the scenario endpoint.
// An endpoint of http://host:9200 and path /logs/_bulk is http://host:9200/logs/_bulk.
// A trailing slash on the endpoint is not repeated.
func endpointURL(endpoint, path string) (*url.URL, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(u.Path, "/")
	switch {
	case path == "" || path == "/":
		if base == "" {
			u.Path = "/"
		} else {
			u.Path = base
		}
	default:
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		u.Path = base + path
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func setAuth(req *http.Request, user, pass string) {
	req.Header.Set("User-Agent", "syscon-bench")
	if user != "" {
		req.SetBasicAuth(user, pass)
	}
}

// call performs req and drains the body so the connection can be reused.
// keepBody retains up to maxSearchBody bytes. Otherwise the first 4KiB is
// kept for the error text.
func call(client *http.Client, req *http.Request, keepBody bool) (int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	var body []byte
	if keepBody {
		body, err = readSearchBody(resp.Body)
	} else {
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
		}
	}
	if err != nil {
		return resp.StatusCode, body, err
	}
	return resp.StatusCode, body, nil
}

func okStatus(status int) bool {
	return status >= 200 && status < 300
}

func httpStatusError(engine string, status int, body []byte) error {
	msg := strings.TrimSpace(string(body))
	if len(msg) > 512 {
		msg = msg[:512]
	}
	if msg == "" {
		return fmt.Errorf("%s: status %d", engine, status)
	}
	return fmt.Errorf("%s: status %d: %s", engine, status, msg)
}

// missingStore reports that the engine has no table, stream, or data stream yet.
func missingStore(status int, body []byte) bool {
	if status == http.StatusNotFound {
		return true
	}
	msg := strings.ToLower(string(body))
	return strings.Contains(msg, "unknown_table") ||
		strings.Contains(msg, "doesn't exist") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "index_not_found_exception")
}

func parseInt64Body(body []byte) (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(string(body)), 10, 64)
}

// promVectorHits sums a Prometheus instant-query vector.
// An empty result is zero hits. status other than success is an error.
func promVectorHits(body []byte) (int, error) {
	var parsed struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if parsed.Status != "" && parsed.Status != "success" {
		if parsed.Error == "" {
			parsed.Error = parsed.Status
		}
		return 0, fmt.Errorf("%s", parsed.Error)
	}
	sum := 0
	for _, series := range parsed.Data.Result {
		if len(series.Value) < 2 {
			continue
		}
		n, err := asInt(series.Value[1])
		if err != nil {
			return 0, err
		}
		sum += n
	}
	return sum, nil
}

func promVectorInt64(body []byte) (int64, error) {
	var parsed struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []struct {
				Value []any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return 0, err
	}
	if parsed.Status != "" && parsed.Status != "success" {
		if parsed.Error == "" {
			parsed.Error = parsed.Status
		}
		return 0, fmt.Errorf("%s", parsed.Error)
	}
	var sum int64
	for _, series := range parsed.Data.Result {
		if len(series.Value) < 2 {
			continue
		}
		switch v := series.Value[1].(type) {
		case string:
			n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				f, ferr := strconv.ParseFloat(v, 64)
				if ferr != nil {
					return 0, err
				}
				n = int64(f)
			}
			sum += n
		case float64:
			sum += int64(v)
		default:
			return 0, fmt.Errorf("vector value %T", series.Value[1])
		}
	}
	return sum, nil
}

func asInt(v any) (int, error) {
	switch n := v.(type) {
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, fmt.Errorf("number %q", n)
		}
		return int(f), nil
	case float64:
		return int(n), nil
	case json.Number:
		f, err := n.Float64()
		if err != nil {
			return 0, err
		}
		return int(f), nil
	default:
		return 0, fmt.Errorf("number type %T", v)
	}
}

// quoteLiteral quotes s for LogQL and LogsQL. Both use backslash escapes.
func quoteLiteral(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
