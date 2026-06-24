package render

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strings"
)

func BuildPrettyPreview(r io.Reader, matchIndex int) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	return BuildPrettyPreviewBytes(data, matchIndex)
}

func BuildPrettyPreviewBytes(data []byte, matchIndex int) (string, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return "", nil
	}

	var value any
	if err := json.Unmarshal(data, &value); err == nil {
		return prettyValueHTML(value, matchIndex), nil
	}
	if ndjson := parseNDJSON(data); ndjson != nil {
		return prettyValueHTML(ndjson, matchIndex), nil
	}
	return template.HTMLEscapeString(string(data)), nil
}

func parseNDJSON(data []byte) []any {
	var out []any
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 2*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(line, &value); err != nil {
			return nil
		}
		out = append(out, value)
	}
	if len(out) == 0 || scanner.Err() != nil {
		return nil
	}
	return out
}

func prettyValueHTML(v any, matchIndex int) string {
	v = normalizeJSON(v)
	switch typed := v.(type) {
	case []any:
		var sb strings.Builder
		sb.WriteString("[\n")
		for idx, item := range typed {
			chunk := template.HTMLEscapeString(prettyJSONValue(item))
			if idx == matchIndex {
				sb.WriteString("  <mark class=\"bg-yellow-100\">")
				sb.WriteString(chunk)
				sb.WriteString("</mark>")
			} else {
				sb.WriteString("  ")
				sb.WriteString(chunk)
			}
			if idx < len(typed)-1 {
				sb.WriteString(",\n")
			} else {
				sb.WriteString("\n")
			}
		}
		sb.WriteString("]\n")
		return sb.String()
	default:
		return template.HTMLEscapeString(prettyJSONValue(typed))
	}
}

func prettyJSONValue(v any) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(out)
}

func normalizeJSON(v any) any {
	switch typed := v.(type) {
	case map[string]any:
		for key, value := range typed {
			typed[key] = normalizeJSON(value)
		}
		return typed
	case []any:
		for idx := range typed {
			typed[idx] = normalizeJSON(typed[idx])
		}
		return typed
	case string:
		s := strings.TrimSpace(typed)
		if len(s) >= 2 && ((s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '[' && s[len(s)-1] == ']')) {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return normalizeJSON(parsed)
			}
		}
		return typed
	default:
		return v
	}
}
