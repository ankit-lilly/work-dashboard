package server

import (
	"bytes"
	"context"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleS3ViewJSON(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	bucket := strings.TrimSpace(q.Get("bucket"))
	key := strings.TrimSpace(q.Get("key"))
	indexStr := strings.TrimSpace(q.Get("index"))
	if env == "" || bucket == "" || key == "" {
		http.Error(w, "missing env/bucket/key", http.StatusBadRequest)
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
		http.Error(w, "unknown env", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	head, err := client.S3.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		log.Printf("s3 view head failed: %v", err)
	}

	if head != nil && head.ContentLength != nil && *head.ContentLength > 2*1024*1024 {
		s.render(w, "json_view", map[string]any{
			"Env":    env,
			"Bucket": bucket,
			"Key":    key,
			"Error":  "File too large to preview. Please download.",
		})
		return
	}

	obj, err := client.S3.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		http.Error(w, "failed to fetch s3 object", http.StatusBadRequest)
		return
	}
	defer obj.Body.Close()

	previewHTML, err := buildPrettyPreview(obj.Body, index)
	if err != nil {
		log.Printf("s3 view build failed: %v", err)
	}

	s.render(w, "json_view", map[string]any{
		"Env":         env,
		"Bucket":      bucket,
		"Key":         key,
		"PreviewHTML": template.HTML(previewHTML),
		"Error":       err,
	})
}

func (s *Server) handleS3PreviewModal(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	q := r.URL.Query()
	env := strings.TrimSpace(q.Get("env"))
	bucket := strings.TrimSpace(q.Get("bucket"))
	key := strings.TrimSpace(q.Get("key"))
	indexStr := strings.TrimSpace(q.Get("index"))
	targetID := strings.TrimSpace(q.Get("target_id"))
	if env == "" || bucket == "" || key == "" || targetID == "" {
		log.Printf("s3 preview modal: missing env/bucket/key/target_id")
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
		log.Printf("s3 preview modal: unknown env %s", env)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	head, err := client.S3.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		log.Printf("s3 preview modal head failed: %v", err)
	}
	if head != nil && head.ContentLength != nil && *head.ContentLength > 2*1024*1024 {
		var buf bytes.Buffer
		tmpl := s.templateSet("index")
		if tmpl == nil {
			return
		}
		_ = tmpl.ExecuteTemplate(&buf, "json-modal-content", map[string]any{
			"Env":    env,
			"Bucket": bucket,
			"Key":    key,
			"Error":  "File too large to preview. Please download.",
		})
		sse.PatchElements(
			buf.String(),
			datastar.WithSelector("#"+targetID),
			datastar.WithMode(datastar.ElementPatchModeInner),
		)
		return
	}

	obj, err := client.S3.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		log.Printf("s3 preview modal get failed: %v", err)
		return
	}
	defer obj.Body.Close()

	previewHTML, err := buildPrettyPreview(obj.Body, index)
	if err != nil {
		log.Printf("s3 preview modal build failed: %v", err)
	}

	var buf bytes.Buffer
	tmpl := s.templateSet("index")
	if tmpl == nil {
		return
	}
	_ = tmpl.ExecuteTemplate(&buf, "json-modal-content", map[string]any{
		"Env":         env,
		"Bucket":      bucket,
		"Key":         key,
		"PreviewHTML": template.HTML(previewHTML),
		"Error":       err,
	})
	sse.PatchElements(
		buf.String(),
		datastar.WithSelector("#"+targetID),
		datastar.WithMode(datastar.ElementPatchModeInner),
	)
}
