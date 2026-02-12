package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/aws-sdk-go-v2/service/sfn/types"
	"github.com/starfederation/datastar-go/datastar"
)

type ExecutionItem struct {
	Env               string
	Name              string
	Arn               string
	Status            string
	StartTime         string
	StopTime          string
	InputBucket       string
	InputKey          string
	InputSize         string
	OutputBucket      string
	OutputPrefix      string
	OutputManifestKey string
	ErrorType         string
	FailureReason     string
	Duration          string
	MapRuns           []MapRunItem
}

type MapRunItem struct {
	Arn       string
	StartTime string
	StopTime  string
}

func (s *Server) handleStateMachineExecutions(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	arn := strings.TrimSpace(r.URL.Query().Get("arn"))
	if env == "" || arn == "" {
		http.Error(w, "missing env or arn", http.StatusBadRequest)
		return
	}

	client := s.awsManager.Clients[env]
	if client == nil {
		http.Error(w, "unknown env", http.StatusBadRequest)
		return
	}

	// Optimistic loading state
	{
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl == nil {
			return
		}
		_ = tmpl.ExecuteTemplate(&buf, "state-machine-executions", map[string]any{
			"Executions": nil,
		})
		sse.PatchElements(
			buf.String(),
			datastar.WithSelector("#state-machine-executions-content"),
			datastar.WithMode(datastar.ElementPatchModeInner),
		)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	execs, err := client.ListRecentExecutionsByStatus(ctx, &arn, types.ExecutionStatusRunning, time.Time{}, 1)
	if err != nil {
		log.Printf("ListRecentExecutions failed for %s (%s): %v", env, arn, err)
	}
	more, _ := client.ListRecentExecutionsByStatus(ctx, &arn, types.ExecutionStatusFailed, time.Time{}, 1)
	execs = append(execs, more...)
	more, _ = client.ListRecentExecutionsByStatus(ctx, &arn, types.ExecutionStatusSucceeded, time.Time{}, 1)
	execs = append(execs, more...)

	// Keep only last 10 by start time.
	sort.Slice(execs, func(i, j int) bool {
		if execs[i].StartDate == nil {
			return false
		}
		if execs[j].StartDate == nil {
			return true
		}
		return execs[i].StartDate.After(*execs[j].StartDate)
	})
	if len(execs) > 10 {
		execs = execs[:10]
	}

	var items []ExecutionItem
	for _, e := range execs {
		desc, err := client.Sfn.DescribeExecution(ctx, &sfn.DescribeExecutionInput{
			ExecutionArn: e.ExecutionArn,
		})
		if err != nil {
			continue
		}
		bucket, key, prefix := extractBucketKeyPrefix(desc.Input)
		outBucket, outManifestKey := extractResultWriterDetails(desc.Output)
		outPrefix := prefix
		if outBucket != "" && outManifestKey != "" {
			outPrefix = strings.TrimSuffix(outManifestKey, path.Base(outManifestKey))
		}

		var sizeStr string
		if bucket != "" && key != "" {
			head, err := client.S3.HeadObject(ctx, &awss3.HeadObjectInput{
				Bucket: &bucket,
				Key:    &key,
			})
			if err == nil && head.ContentLength != nil {
				sizeStr = humanBytes(*head.ContentLength)
			}
		}

		var mapRuns []MapRunItem
		if e.Status == types.ExecutionStatusSucceeded {
			mrOut, err := client.Sfn.ListMapRuns(ctx, &sfn.ListMapRunsInput{
				ExecutionArn: e.ExecutionArn,
				MaxResults:   10,
			})
			if err == nil {
				for _, mr := range mrOut.MapRuns {
					mapRuns = append(mapRuns, MapRunItem{
						Arn:       derefString(mr.MapRunArn),
						StartTime: formatTime(mr.StartDate),
						StopTime:  formatTime(mr.StopDate),
					})
				}
			}
		}

		items = append(items, ExecutionItem{
			Env:               env,
			Name:              derefString(e.Name),
			Arn:               derefString(e.ExecutionArn),
			Status:            string(e.Status),
			StartTime:         formatTime(e.StartDate),
			StopTime:          formatTime(e.StopDate),
			Duration:          formatDuration(e.StartDate, e.StopDate),
			InputBucket:       bucket,
			InputKey:          key,
			InputSize:         sizeStr,
			OutputBucket:      outBucket,
			OutputPrefix:      outPrefix,
			OutputManifestKey: outManifestKey,
			ErrorType:         derefString(desc.Error),
			FailureReason:     derefString(desc.Cause),
			MapRuns:           mapRuns,
		})
	}

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "state-machine-executions", map[string]any{
		"Env":        env,
		"Arn":        arn,
		"Executions": items,
	})

	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#state-machine-executions-content"),
		datastar.WithMode(datastar.ElementPatchModeInner),
	)
}

func formatDuration(start, stop *time.Time) string {
	if start == nil || stop == nil {
		return ""
	}
	d := stop.Sub(*start)
	if d < 0 {
		d = -d
	}
	if d < time.Minute {
		return strconv.FormatInt(int64(d.Seconds()), 10) + "s"
	}
	if d < time.Hour {
		return strconv.FormatInt(int64(d.Minutes()), 10) + "m"
	}
	return strconv.FormatInt(int64(d.Hours()), 10) + "h"
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	value := float64(n)
	exp := 0
	for value >= unit && exp < 4 {
		value /= unit
		exp++
	}
	suffixes := []string{"KB", "MB", "GB", "TB"}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + suffixes[exp-1]
}

func extractResultWriterDetails(output *string) (string, string) {
	if output == nil || strings.TrimSpace(*output) == "" {
		return "", ""
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(*output), &payload); err != nil {
		return "", ""
	}
	if rwd, ok := payload["ResultWriterDetails"].(map[string]any); ok {
		b, _ := rwd["Bucket"].(string)
		k, _ := rwd["Key"].(string)
		return b, k
	}
	return "", ""
}

func (s *Server) handleS3Download(w http.ResponseWriter, r *http.Request) {
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if env == "" || bucket == "" || key == "" {
		http.Error(w, "missing env/bucket/key", http.StatusBadRequest)
		return
	}

	client := s.awsManager.Clients[env]
	if client == nil {
		http.Error(w, "unknown env", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	obj, err := client.S3.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		http.Error(w, "failed to fetch s3 object", http.StatusBadRequest)
		return
	}
	defer obj.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	filename := "input.json"
	if key != "" {
		filename = path.Base(key)
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	io.Copy(w, obj.Body)
}

type S3ObjectItem struct {
	Key          string
	Size         string
	LastModified string
}

func (s *Server) handleS3PrefixList(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	bucket := strings.TrimSpace(q.Get("bucket"))
	prefix := strings.TrimSpace(q.Get("prefix"))
	targetID := strings.TrimSpace(q.Get("target_id"))
	if env == "" || bucket == "" || prefix == "" || targetID == "" {
		log.Printf("s3 prefix list: missing env/bucket/prefix/target_id")
		return
	}

	client := s.awsManager.Clients[env]
	if client == nil {
		log.Printf("s3 prefix list: unknown env %s", env)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	out, err := client.S3.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
		Bucket:  &bucket,
		Prefix:  &prefix,
		MaxKeys: aws.Int32(50),
	})
	if err != nil {
		log.Printf("s3 list failed: %v", err)
	}

	var items []S3ObjectItem
	if out != nil {
		for _, obj := range out.Contents {
			if obj.Key == nil {
				continue
			}
			base := path.Base(*obj.Key)
			if base != "manifest.json" && base != "SUCCEEDED_0.json" && !(strings.HasPrefix(base, "ERROR") && strings.HasSuffix(base, ".json")) {
				continue
			}
			sizeStr := ""
			if obj.Size != nil {
				sizeStr = humanBytes(*obj.Size)
			}
			lm := ""
			if obj.LastModified != nil {
				lm = obj.LastModified.Format("2006-01-02 15:04:05")
			}
			items = append(items, S3ObjectItem{
				Key:          *obj.Key,
				Size:         sizeStr,
				LastModified: lm,
			})
		}
	}

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "s3-prefix-results", map[string]any{
		"Bucket": bucket,
		"Prefix": prefix,
		"Items":  items,
		"Error":  err,
		"Target": targetID,
		"Env":    env,
	})

	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#"+targetID),
		datastar.WithMode(datastar.ElementPatchModeOuter),
	)
}

func (s *Server) handleS3Search(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	bucket := strings.TrimSpace(q.Get("bucket"))
	key := strings.TrimSpace(q.Get("key"))
	query := strings.TrimSpace(q.Get("query"))
	targetID := strings.TrimSpace(q.Get("target_id"))
	if env == "" || bucket == "" || key == "" || query == "" || targetID == "" {
		http.Error(w, "missing env/bucket/key/query", http.StatusBadRequest)
		return
	}

	client := s.awsManager.Clients[env]
	if client == nil {
		http.Error(w, "unknown env", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	matches, err := client.FindStringInS3JSON(ctx, bucket, key, query, 50)
	if err != nil {
		log.Printf("s3 search failed: %v", err)
	}

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "s3-search-results", map[string]any{
		"Query":   query,
		"Bucket":  bucket,
		"Key":     key,
		"Matches": matches,
		"Error":   err,
		"Target":  targetID,
	})

	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#"+targetID),
		datastar.WithMode(datastar.ElementPatchModeOuter),
	)
}

func (s *Server) handleS3Preview(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	bucket := strings.TrimSpace(q.Get("bucket"))
	key := strings.TrimSpace(q.Get("key"))
	indexStr := strings.TrimSpace(q.Get("index"))
	targetID := strings.TrimSpace(q.Get("target_id"))
	if env == "" || bucket == "" || key == "" || targetID == "" {
		log.Printf("s3 preview: missing env/bucket/key/target_id")
		return
	}

	index := -1
	if indexStr != "" {
		if n, err := strconv.Atoi(indexStr); err == nil {
			index = n
		}
	}

	client := s.awsManager.Clients[env]
	if client == nil {
		log.Printf("s3 preview: unknown env %s", env)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	head, err := client.S3.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		log.Printf("s3 preview head failed: %v", err)
	}
	if head != nil && head.ContentLength != nil && *head.ContentLength > 2*1024*1024 {
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl == nil {
			return
		}
		_ = tmpl.ExecuteTemplate(&buf, "s3-preview", map[string]any{
			"Target": targetID,
			"Error":  "File too large to preview. Please download.",
		})
		sse.PatchElements(
			buf.String(),
			datastar.WithSelector("#"+targetID),
			datastar.WithMode(datastar.ElementPatchModeOuter),
		)
		return
	}

	obj, err := client.S3.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		log.Printf("s3 preview get failed: %v", err)
		return
	}
	defer obj.Body.Close()

	previewHTML, err := buildPrettyPreview(obj.Body, index)
	if err != nil {
		log.Printf("s3 preview build failed: %v", err)
	}

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "s3-preview", map[string]any{
		"Target":      targetID,
		"PreviewHTML": template.HTML(previewHTML),
		"Error":       err,
	})
	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#"+targetID),
		datastar.WithMode(datastar.ElementPatchModeOuter),
	)
}

func buildPrettyPreview(r io.Reader, matchIndex int) (string, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return "", nil
	}

	var v any
	if err := json.Unmarshal(data, &v); err == nil {
		return prettyValueHTML(v, matchIndex), nil
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
		var v any
		if err := json.Unmarshal(line, &v); err != nil {
			return nil
		}
		out = append(out, v)
	}
	if len(out) == 0 || scanner.Err() != nil {
		return nil
	}
	return out
}

func prettyValueHTML(v any, matchIndex int) string {
	v = normalizeJSON(v)

	switch t := v.(type) {
	case []any:
		var sb strings.Builder
		sb.WriteString("[\n")
		for i, item := range t {
			chunk := template.HTMLEscapeString(prettyJSONValue(item))
			if i == matchIndex {
				sb.WriteString("  <mark class=\"bg-yellow-100\">")
				sb.WriteString(chunk)
				sb.WriteString("</mark>")
			} else {
				sb.WriteString("  ")
				sb.WriteString(chunk)
			}
			if i < len(t)-1 {
				sb.WriteString(",\n")
			} else {
				sb.WriteString("\n")
			}
		}
		sb.WriteString("]\n")
		return sb.String()
	default:
		return template.HTMLEscapeString(prettyJSONValue(t))
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
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			t[k] = normalizeJSON(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = normalizeJSON(t[i])
		}
		return t
	case string:
		s := strings.TrimSpace(t)
		if len(s) >= 2 && ((s[0] == '{' && s[len(s)-1] == '}') || (s[0] == '[' && s[len(s)-1] == ']')) {
			var parsed any
			if err := json.Unmarshal([]byte(s), &parsed); err == nil {
				return normalizeJSON(parsed)
			}
		}
		return t
	default:
		return v
	}
}
