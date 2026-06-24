package server

import (
	"context"
	"html/template"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/EliLillyCo/work-dashboard/internal/server/render"
	"github.com/starfederation/datastar-go/datastar"
)

func (s *Server) handleS3ViewJSON(w http.ResponseWriter, r *http.Request) {
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	index := parseIndex(r.URL.Query().Get("index"))
	if env == "" || bucket == "" || key == "" {
		http.Error(w, "missing env/bucket/key", http.StatusBadRequest)
		return
	}

	maxSize := int64(2 * 1024 * 1024)
	if s.cfg != nil && s.cfg.Limits.MaxPreviewFileSize > 0 {
		maxSize = s.cfg.Limits.MaxPreviewFileSize
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	data, err := s.searchService.PreviewObject(ctx, env, bucket, key, maxSize)
	previewHTML := ""
	if err == nil {
		previewHTML, err = render.BuildPrettyPreviewBytes(data, index)
	}
	s.renderer.Render(w, "json_view", render.JSONPreviewData{
		Env:         env,
		Bucket:      bucket,
		Key:         key,
		PreviewHTML: template.HTML(previewHTML),
		Error:       err,
	})
}

func (s *Server) handleS3PreviewModal(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	index := parseIndex(r.URL.Query().Get("index"))
	if env == "" || bucket == "" || key == "" || targetID == "" {
		return
	}

	maxSize := int64(2 * 1024 * 1024)
	if s.cfg != nil && s.cfg.Limits.MaxPreviewFileSize > 0 {
		maxSize = s.cfg.Limits.MaxPreviewFileSize
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	data, err := s.searchService.PreviewObject(ctx, env, bucket, key, maxSize)
	previewHTML := ""
	if err == nil {
		previewHTML, err = render.BuildPrettyPreviewBytes(data, index)
	}
	html, execErr := s.renderer.ExecuteTemplate("index", "json-modal-content", render.JSONPreviewData{
		Env:         env,
		Bucket:      bucket,
		Key:         key,
		PreviewHTML: template.HTML(previewHTML),
		Error:       err,
	})
	if execErr == nil {
		sse.PatchElements(html, datastar.WithSelector("#"+targetID), datastar.WithMode(datastar.ElementPatchModeInner))
	}
}

func (s *Server) handleS3Download(w http.ResponseWriter, r *http.Request) {
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if env == "" || bucket == "" || key == "" {
		http.Error(w, "missing env/bucket/key", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	body, err := s.searchService.DownloadObject(ctx, env, bucket, key)
	if err != nil {
		http.Error(w, "failed to fetch s3 object", http.StatusBadRequest)
		return
	}
	defer body.Close()

	w.Header().Set("Content-Type", "application/json")
	filename := "input.json"
	if key != "" {
		filename = path.Base(key)
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\""+sanitizeFilename(filename)+"\"")
	_, _ = io.Copy(w, body)
}

func (s *Server) handleS3PrefixList(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	prefix := strings.TrimSpace(r.URL.Query().Get("prefix"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	if env == "" || bucket == "" || prefix == "" || targetID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	items, err := s.searchService.ListPrefixObjects(ctx, env, bucket, prefix)
	html, execErr := s.renderer.ExecuteTemplate("index", "s3-prefix-results", render.PrefixListData{
		Env:    env,
		Bucket: bucket,
		Prefix: prefix,
		Items:  render.PresentPrefixObjects(items),
		Error:  err,
		Target: targetID,
	})
	if execErr == nil {
		sse.PatchElements(html, datastar.WithSelector("#"+targetID), datastar.WithMode(datastar.ElementPatchModeOuter), datastar.WithUseViewTransitions(false))
	}
}

func (s *Server) handleS3Search(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	if env == "" || bucket == "" || key == "" || query == "" || targetID == "" {
		http.Error(w, "missing env/bucket/key/query", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	matches, err := s.searchService.FindString(ctx, env, bucket, key, query, 50)
	html, execErr := s.renderer.ExecuteTemplate("index", "s3-search-results", render.StringSearchData{
		Query:   query,
		Bucket:  bucket,
		Key:     key,
		Matches: matches,
		Error:   err,
		Target:  targetID,
	})
	if execErr == nil {
		sse.PatchElements(html, datastar.WithSelector("#"+targetID), datastar.WithMode(datastar.ElementPatchModeOuter), datastar.WithUseViewTransitions(false))
	}
}

func (s *Server) handleS3Preview(w http.ResponseWriter, r *http.Request) {
	sse := datastar.NewSSE(w, r)
	env := strings.TrimSpace(r.URL.Query().Get("env"))
	bucket := strings.TrimSpace(r.URL.Query().Get("bucket"))
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	targetID := strings.TrimSpace(r.URL.Query().Get("target_id"))
	index := parseIndex(r.URL.Query().Get("index"))
	if env == "" || bucket == "" || key == "" || targetID == "" {
		return
	}

	maxSize := int64(2 * 1024 * 1024)
	if s.cfg != nil && s.cfg.Limits.MaxPreviewFileSize > 0 {
		maxSize = s.cfg.Limits.MaxPreviewFileSize
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	data, err := s.searchService.PreviewObject(ctx, env, bucket, key, maxSize)
	previewHTML := ""
	if err == nil {
		previewHTML, err = render.BuildPrettyPreviewBytes(data, index)
	}
	html, execErr := s.renderer.ExecuteTemplate("index", "s3-preview", render.JSONPreviewData{
		Target:      targetID,
		PreviewHTML: template.HTML(previewHTML),
		Error:       err,
	})
	if execErr == nil {
		sse.PatchElements(html, datastar.WithSelector("#"+targetID), datastar.WithMode(datastar.ElementPatchModeOuter), datastar.WithUseViewTransitions(false))
	}
}

func parseIndex(raw string) int {
	if raw == "" {
		return -1
	}
	if n, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
		return n
	}
	return -1
}
