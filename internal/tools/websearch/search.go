// Package websearch provides web search tools for the JARV agent.
//
// Three backends are supported:
//   - DuckDuckGo Instant Answer (free, no key, limited results)
//   - DuckDuckGo Lite HTML     (free, no key, full results via scraping)
//   - Brave Search API         (paid, $5/1000 queries)
//   - Tavily                   (1000 free queries/month)
//
// Copyright (c) 2026 JARV contributors. All rights reserved.
package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Result type shared by all backends
// ─────────────────────────────────────────────────────────────────────────────

// Result represents a single search result.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

func (r Result) String() string {
	return fmt.Sprintf("**%s**\n%s\n%s", r.Title, r.Snippet, r.URL)
}

// FormatResults formats a slice of results for LLM consumption.
func FormatResults(results []Result, query string) string {
	if len(results) == 0 {
		return fmt.Sprintf("Nenhum resultado encontrado para: %q", query)
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Resultados de busca para %q:\n\n", query))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.String()))
		if i < len(results)-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// DuckDuckGo — Instant Answer API (free, no key)
// ─────────────────────────────────────────────────────────────────────────────

// DDGSearch searches using DuckDuckGo's Instant Answer API.
// Returns structured results for many queries; falls back to scraping for others.
type DDGSearch struct {
	client *http.Client
}

// NewDDGSearch creates a new DuckDuckGo search backend.
func NewDDGSearch() *DDGSearch {
	return &DDGSearch{client: &http.Client{Timeout: 10 * time.Second}}
}

func (d *DDGSearch) Name() string        { return "web_search" }
func (d *DDGSearch) Description() string {
	return "Search the web using DuckDuckGo. Use this when you need current information, facts, or to look something up online."
}
func (d *DDGSearch) Schema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Maximum number of results to return (default: 5)",
			},
		},
		"required": []string{"query"},
	}
}

func (d *DDGSearch) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("web_search: invalid args: %w", err)
	}
	if params.MaxResults <= 0 {
		params.MaxResults = 5
	}

	results, err := d.search(ctx, params.Query, params.MaxResults)
	if err != nil {
		return "", err
	}
	return FormatResults(results, params.Query), nil
}

func (d *DDGSearch) search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	// Try Instant Answer API first
	instant, err := d.instantAnswer(ctx, query)
	if err == nil && len(instant) > 0 {
		return instant, nil
	}

	// Fall back to HTML scraping
	return d.scrapeHTML(ctx, query, maxResults)
}

type ddgInstantResponse struct {
	Abstract       string `json:"Abstract"`
	AbstractURL    string `json:"AbstractURL"`
	AbstractSource string `json:"AbstractSource"`
	RelatedTopics  []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"RelatedTopics"`
}

func (d *DDGSearch) instantAnswer(ctx context.Context, query string) ([]Result, error) {
	endpoint := "https://api.duckduckgo.com/?q=" + url.QueryEscape(query) +
		"&format=json&no_html=1&skip_disambig=1"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "JARV/1.0 (AI Assistant; +https://github.com/mkvinicius/jarv)")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data ddgInstantResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	var results []Result

	if data.Abstract != "" {
		results = append(results, Result{
			Title:   data.AbstractSource,
			URL:     data.AbstractURL,
			Snippet: data.Abstract,
		})
	}

	for _, rt := range data.RelatedTopics {
		if rt.Text == "" || rt.FirstURL == "" {
			continue
		}
		title := rt.FirstURL
		if idx := strings.LastIndex(rt.FirstURL, "/"); idx >= 0 {
			title = strings.ReplaceAll(rt.FirstURL[idx+1:], "_", " ")
		}
		results = append(results, Result{
			Title:   title,
			URL:     rt.FirstURL,
			Snippet: rt.Text,
		})
		if len(results) >= 5 {
			break
		}
	}

	return results, nil
}

func (d *DDGSearch) scrapeHTML(ctx context.Context, query string, maxResults int) ([]Result, error) {
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; JARV/1.0)")
	req.Header.Set("Accept-Language", "pt-BR,pt;q=0.9,en;q=0.8")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ddg scrape: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseHTMLResults(string(body), maxResults), nil
}

// parseHTMLResults extracts search results from DuckDuckGo HTML response.
func parseHTMLResults(html string, maxResults int) []Result {
	var results []Result

	// DDG HTML uses class="result__a" for links and class="result__snippet" for snippets
	parts := strings.Split(html, "class=\"result__title\"")
	for i := 1; i < len(parts) && len(results) < maxResults; i++ {
		part := parts[i]

		// Extract URL
		resultURL := ""
		if urlStart := strings.Index(part, "href=\"//duckduckgo.com/l/?uddg="); urlStart >= 0 {
			urlPart := part[urlStart+len("href=\"//duckduckgo.com/l/?uddg="):]
			if urlEnd := strings.Index(urlPart, "\""); urlEnd >= 0 {
				encoded := urlPart[:urlEnd]
				if decoded, err := url.QueryUnescape(encoded); err == nil {
					resultURL = decoded
				}
			}
		}
		if resultURL == "" {
			if urlStart := strings.Index(part, "href=\""); urlStart >= 0 {
				urlPart := part[urlStart+6:]
				if urlEnd := strings.Index(urlPart, "\""); urlEnd >= 0 {
					resultURL = urlPart[:urlEnd]
				}
			}
		}

		// Extract title
		title := ""
		if tStart := strings.Index(part, ">"); tStart >= 0 {
			tPart := part[tStart+1:]
			if tEnd := strings.Index(tPart, "<"); tEnd >= 0 {
				title = strings.TrimSpace(stripTags(tPart[:tEnd]))
			}
		}

		// Extract snippet
		snippet := ""
		if sStart := strings.Index(part, "class=\"result__snippet\""); sStart >= 0 {
			sPart := part[sStart:]
			if sTagEnd := strings.Index(sPart, ">"); sTagEnd >= 0 {
				sPart = sPart[sTagEnd+1:]
				if sEnd := strings.Index(sPart, "</"); sEnd >= 0 {
					snippet = strings.TrimSpace(stripTags(sPart[:sEnd]))
				}
			}
		}

		if title != "" && resultURL != "" {
			results = append(results, Result{
				Title:   title,
				URL:     resultURL,
				Snippet: snippet,
			})
		}
	}

	return results
}

func stripTags(s string) string {
	var result strings.Builder
	inTag := false
	for _, c := range s {
		if c == '<' {
			inTag = true
		} else if c == '>' {
			inTag = false
		} else if !inTag {
			result.WriteRune(c)
		}
	}
	return result.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// Brave Search (optional, requires API key)
// ─────────────────────────────────────────────────────────────────────────────

// BraveSearch uses the Brave Search API.
type BraveSearch struct {
	apiKey string
	client *http.Client
}

// NewBraveSearch creates a Brave Search backend.
func NewBraveSearch(apiKey string) *BraveSearch {
	return &BraveSearch{apiKey: apiKey, client: &http.Client{Timeout: 10 * time.Second}}
}

func (b *BraveSearch) Name() string        { return "web_search" }
func (b *BraveSearch) Description() string { return "Search the web using Brave Search." }
func (b *BraveSearch) Schema() any         { return DDGSchemaObj() }

func (b *BraveSearch) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("brave_search: invalid args: %w", err)
	}
	if params.MaxResults <= 0 {
		params.MaxResults = 5
	}

	endpoint := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(params.Query), params.MaxResults)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", b.apiKey)

	resp, err := b.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("brave: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("brave: decode: %w", err)
	}

	var results []Result
	for _, r := range data.Web.Results {
		results = append(results, Result{Title: r.Title, URL: r.URL, Snippet: r.Description})
	}
	return FormatResults(results, params.Query), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Tavily Search (optional, 1000 free queries/month)
// ─────────────────────────────────────────────────────────────────────────────

// TavilySearch uses the Tavily API, optimized for AI agents.
type TavilySearch struct {
	apiKey string
	client *http.Client
}

// NewTavilySearch creates a Tavily search backend.
func NewTavilySearch(apiKey string) *TavilySearch {
	return &TavilySearch{apiKey: apiKey, client: &http.Client{Timeout: 15 * time.Second}}
}

func (t *TavilySearch) Name() string        { return "web_search" }
func (t *TavilySearch) Description() string {
	return "Search the web using Tavily, optimized for AI agents with high-quality results."
}
func (t *TavilySearch) Schema() any { return DDGSchemaObj() }

func (t *TavilySearch) Execute(ctx context.Context, args string) (string, error) {
	var params struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("tavily: invalid args: %w", err)
	}
	if params.MaxResults <= 0 {
		params.MaxResults = 5
	}

	body, _ := json.Marshal(map[string]any{
		"api_key":          t.apiKey,
		"query":            params.Query,
		"max_results":      params.MaxResults,
		"search_depth":     "basic",
		"include_answer":   true,
		"include_raw_content": false,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.tavily.com/search", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("tavily: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var data struct {
		Answer  string `json:"answer"`
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(respBody, &data); err != nil {
		return "", fmt.Errorf("tavily: decode: %w", err)
	}

	var results []Result
	if data.Answer != "" {
		results = append(results, Result{Title: "Resposta direta", Snippet: data.Answer})
	}
	for _, r := range data.Results {
		results = append(results, Result{Title: r.Title, URL: r.URL, Snippet: r.Content})
	}
	return FormatResults(results, params.Query), nil
}

// DDGSchemaObj returns the shared JSON schema for search tools.
func DDGSchemaObj() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "Search query"},
			"max_results": map[string]any{"type": "integer", "description": "Max results (default: 5)"},
		},
		"required": []string{"query"},
	}
}
