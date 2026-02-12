package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const jeffDeanFactsURL = "https://raw.githubusercontent.com/LRitzdorf/TheJeffDeanFacts/refs/heads/main/README.md"
const chuckNorrisURL = "https://api.chucknorris.io/jokes/random"

type chuckJokeResponse struct {
	Value string `json:"value"`
}

func (s *Server) getFunJoke(ctx context.Context) string {
	// 50/50 split, fallback to the other source on failure.
	if rand.Intn(2) == 0 {
		if joke := s.getJeffDeanFact(ctx); joke != "" {
			return joke
		}
		return s.getChuckNorrisJoke(ctx)
	}
	if joke := s.getChuckNorrisJoke(ctx); joke != "" {
		return joke
	}
	return s.getJeffDeanFact(ctx)
}

func (s *Server) getChuckNorrisJoke(ctx context.Context) string {
	s.jokeMu.Lock()
	if s.chuckJoke != "" && time.Since(s.chuckJokeAt) < 30*time.Minute {
		joke := s.chuckJoke
		s.jokeMu.Unlock()
		return joke
	}
	s.jokeMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, chuckNorrisURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}

	var payload chuckJokeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return ""
	}
	joke := strings.TrimSpace(payload.Value)
	if joke == "" {
		return ""
	}

	s.jokeMu.Lock()
	s.chuckJoke = joke
	s.chuckJokeAt = time.Now()
	s.jokeMu.Unlock()

	return joke
}

func (s *Server) getJeffDeanFact(ctx context.Context) string {
	s.jokeMu.Lock()
	if s.jeffJoke != "" && time.Since(s.jeffJokeAt) < 30*time.Minute {
		joke := s.jeffJoke
		s.jokeMu.Unlock()
		return joke
	}
	s.jokeMu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, jeffDeanFactsURL, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	facts, err := parseJeffDeanFacts(string(body))
	if err != nil || len(facts) == 0 {
		return ""
	}

	rand.Seed(time.Now().UnixNano())
	joke := strings.TrimSpace(facts[rand.Intn(len(facts))])
	if joke == "" {
		return ""
	}

	s.jokeMu.Lock()
	s.jeffJoke = joke
	s.jeffJokeAt = time.Now()
	s.jokeMu.Unlock()

	return joke
}

func parseJeffDeanFacts(md string) ([]string, error) {
	start := strings.Index(md, "## The Facts")
	if start == -1 {
		return nil, fmt.Errorf("facts section not found")
	}
	md = md[start+len("## The Facts"):]
	if end := strings.Index(md, "\n## "); end != -1 {
		md = md[:end]
	}
	lines := strings.Split(md, "\n")
	facts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") {
			fact := strings.TrimSpace(strings.TrimPrefix(line, "- "))
			if fact != "" {
				facts = append(facts, fact)
			}
		}
	}
	if len(facts) == 0 {
		return nil, fmt.Errorf("no facts found")
	}
	return facts, nil
}
