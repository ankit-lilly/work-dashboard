package jokes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

const jeffDeanFactsURL = "https://raw.githubusercontent.com/LRitzdorf/TheJeffDeanFacts/refs/heads/main/README.md"
const chuckNorrisURL = "https://api.chucknorris.io/jokes/random"

type Provider interface {
	Random(context.Context) string
}

type Client struct {
	mu          sync.Mutex
	chuckJoke   string
	chuckJokeAt time.Time
	jeffJoke    string
	jeffJokeAt  time.Time
}

type chuckJokeResponse struct {
	Value string `json:"value"`
}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) Random(ctx context.Context) string {
	if rand.Intn(2) == 0 {
		if joke := c.getJeffDeanFact(ctx); joke != "" {
			return joke
		}
		return c.getChuckNorrisJoke(ctx)
	}
	if joke := c.getChuckNorrisJoke(ctx); joke != "" {
		return joke
	}
	return c.getJeffDeanFact(ctx)
}

func (c *Client) getChuckNorrisJoke(ctx context.Context) string {
	c.mu.Lock()
	if c.chuckJoke != "" && time.Since(c.chuckJokeAt) < 30*time.Minute {
		joke := c.chuckJoke
		c.mu.Unlock()
		return joke
	}
	c.mu.Unlock()

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

	c.mu.Lock()
	c.chuckJoke = joke
	c.chuckJokeAt = time.Now()
	c.mu.Unlock()
	return joke
}

func (c *Client) getJeffDeanFact(ctx context.Context) string {
	c.mu.Lock()
	if c.jeffJoke != "" && time.Since(c.jeffJokeAt) < 30*time.Minute {
		joke := c.jeffJoke
		c.mu.Unlock()
		return joke
	}
	c.mu.Unlock()

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
	joke := strings.TrimSpace(facts[rand.Intn(len(facts))])
	if joke == "" {
		return ""
	}

	c.mu.Lock()
	c.jeffJoke = joke
	c.jeffJokeAt = time.Now()
	c.mu.Unlock()
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
