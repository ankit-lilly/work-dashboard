package aws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type RecordMatch struct {
	Index  int
	Record string
}

type StringMatch struct {
	Index  int
	Record string
}

func (c *Client) FindEmailInS3JSON(ctx context.Context, bucket, key, email string) (*RecordMatch, error) {
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("missing bucket or key")
	}

	out, err := c.S3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()

	dec := json.NewDecoder(out.Body)
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return scanJSONArray(dec, email)
		}
		if t == '{' {
			return scanJSONObject(dec, email)
		}
	}

	return nil, errors.New("unsupported JSON format")
}

func (c *Client) FindStringInS3JSON(ctx context.Context, bucket, key, query string, limit int) ([]StringMatch, error) {
	if bucket == "" || key == "" {
		return nil, fmt.Errorf("missing bucket or key")
	}
	if limit <= 0 {
		limit = 50
	}

	out, err := c.S3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()

	dec := json.NewDecoder(out.Body)
	dec.UseNumber()

	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}

	switch t := tok.(type) {
	case json.Delim:
		if t == '[' {
			return scanJSONArrayForString(dec, query, limit)
		}
		if t == '{' {
			return scanJSONObjectForString(dec, query, limit)
		}
	}

	return nil, errors.New("unsupported JSON format")
}

func scanJSONArray(dec *json.Decoder, email string) (*RecordMatch, error) {
	index := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if rawEmailMatch(raw, email) {
			return &RecordMatch{
				Index:  index,
				Record: compactJSON(raw),
			}, nil
		}
		index++
	}
	// consume closing ']'
	_, _ = dec.Token()
	return nil, nil
}

func scanJSONObject(dec *json.Decoder, email string) (*RecordMatch, error) {
	// Decode entire object (fallback). If it's huge, this can be expensive, but
	// we only do this when the root is an object instead of an array.
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}

	for _, key := range []string{"Items", "items", "Records", "records", "data"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		arrDec := json.NewDecoder(bytes.NewReader(raw))
		arrDec.UseNumber()
		tok, err := arrDec.Token()
		if err != nil {
			continue
		}
		if d, ok := tok.(json.Delim); ok && d == '[' {
			return scanJSONArray(arrDec, email)
		}
	}
	return nil, nil
}

func scanJSONArrayForString(dec *json.Decoder, query string, limit int) ([]StringMatch, error) {
	var matches []StringMatch
	index := 0
	for dec.More() {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		if rawStringMatch(raw, query) {
			matches = append(matches, StringMatch{
				Index:  index,
				Record: compactJSON(raw),
			})
			if len(matches) >= limit {
				break
			}
		}
		index++
	}
	_, _ = dec.Token()
	return matches, nil
}

func scanJSONObjectForString(dec *json.Decoder, query string, limit int) ([]StringMatch, error) {
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, err
	}

	for _, key := range []string{"Items", "items", "Records", "records", "data"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		arrDec := json.NewDecoder(bytes.NewReader(raw))
		arrDec.UseNumber()
		tok, err := arrDec.Token()
		if err != nil {
			continue
		}
		if d, ok := tok.(json.Delim); ok && d == '[' {
			return scanJSONArrayForString(arrDec, query, limit)
		}
	}
	return nil, nil
}

func rawEmailMatch(raw json.RawMessage, email string) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return containsEmail(v, email, 0)
}

func rawStringMatch(raw json.RawMessage, query string) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return containsString(v, query, 0)
}

func containsEmail(v any, email string, depth int) bool {
	if depth > 6 {
		return false
	}
	email = strings.ToLower(strings.TrimSpace(email))

	switch val := v.(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(val)) == email
	case map[string]any:
		for k, v2 := range val {
			key := strings.ToLower(k)
			if key == "email_address" || key == "emailaddress" {
				if s, ok := v2.(string); ok {
					if strings.ToLower(strings.TrimSpace(s)) == email {
						return true
					}
				}
			}
			if containsEmail(v2, email, depth+1) {
				return true
			}
		}
	case []any:
		for _, item := range val {
			if containsEmail(item, email, depth+1) {
				return true
			}
		}
	}
	return false
}

func containsString(v any, query string, depth int) bool {
	if depth > 6 {
		return false
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return false
	}

	switch val := v.(type) {
	case string:
		return strings.Contains(strings.ToLower(val), query)
	case map[string]any:
		for _, v2 := range val {
			if containsString(v2, query, depth+1) {
				return true
			}
		}
	case []any:
		for _, item := range val {
			if containsString(item, query, depth+1) {
				return true
			}
		}
	}
	return false
}

func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	s := buf.String()
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
