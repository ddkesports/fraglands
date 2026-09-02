package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// maxRequestBytes bounds every request body.
const maxRequestBytes = 64 << 10

// errInvalidBody is returned when a request body is not valid JSON.
var errInvalidBody = errors.New("invalid request body")

// readRequestJSON reads and parses one bounded JSON request body into a
// generic object. Numbers decode as json.Number so 64-bit identities such as
// Steam IDs round-trip exactly instead of passing through float64.
func readRequestJSON(r *http.Request) (map[string]any, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequestBytes+1))
	if err != nil {
		return nil, errInvalidBody
	}
	if len(body) > maxRequestBytes {
		return nil, errInvalidBody
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var out map[string]any
	if err := dec.Decode(&out); err != nil {
		return nil, errInvalidBody
	}
	return out, nil
}

// requestString reads one required string field.
func requestString(obj map[string]any, key string) (string, error) {
	v, ok := obj[key]
	if !ok {
		return "", fmt.Errorf("%w: missing %s", errInvalidBody, key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%w: %s must be a string", errInvalidBody, key)
	}
	return s, nil
}

// requestUint64 reads one required non-negative integer field.
func requestUint64(obj map[string]any, key string) (uint64, error) {
	v, ok := obj[key]
	if !ok {
		return 0, fmt.Errorf("%w: missing %s", errInvalidBody, key)
	}
	n, ok := v.(json.Number)
	if !ok {
		return 0, fmt.Errorf("%w: %s must be a number", errInvalidBody, key)
	}
	u, err := strconv.ParseUint(n.String(), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: %s must be a non-negative integer", errInvalidBody, key)
	}
	return u, nil
}

// pathUint64 reads one unsigned integer path or query value.
func pathUint64(raw string) (uint64, error) {
	return strconv.ParseUint(raw, 10, 64)
}
