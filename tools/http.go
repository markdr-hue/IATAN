/*
 * Created by Mark Durlinger. MIT License.
 * 50% human, 50% AI, 100% chaos.
 */

package tools

import (
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// ---------------------------------------------------------------------------
// make_http_request
// ---------------------------------------------------------------------------

type MakeHTTPRequestTool struct{}

func (t *MakeHTTPRequestTool) Name() string { return "make_http_request" }
func (t *MakeHTTPRequestTool) Description() string {
	return "Make an HTTP request to an external URL. Useful for calling APIs, checking endpoints, or fetching data."
}
func (t *MakeHTTPRequestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url":        map[string]interface{}{"type": "string", "description": "The URL to request"},
			"method":     map[string]interface{}{"type": "string", "description": "HTTP method (GET, POST, PUT, DELETE, PATCH)", "enum": []string{"GET", "POST", "PUT", "DELETE", "PATCH"}},
			"headers":    map[string]interface{}{"type": "object", "description": "Request headers as key-value pairs"},
			"body":       map[string]interface{}{"type": "string", "description": "Request body (for POST, PUT, PATCH)"},
			"strip_html": map[string]interface{}{"type": "boolean", "description": "Strip HTML tags and return clean text content. Useful for reading web pages."},
		},
		"required": []string{"url"},
	}
}

func (t *MakeHTTPRequestTool) Execute(ctx *ToolContext, args map[string]interface{}) (*Result, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return &Result{Success: false, Error: "url is required"}, nil
	}

	// Resolve relative URLs (e.g. /api/drawings) against the site's own address.
	// These are always local site URLs, so skip SSRF validation for them.
	isRelative := strings.HasPrefix(url, "/")
	if isRelative {
		url = resolveLocalURL(ctx, url)
	}

	// Block SSRF: reject non-http(s) schemes and private/internal IPs for
	// absolute URLs (which come from LLM output and are untrusted).
	if !isRelative {
		if err := validateExternalURL(url); err != nil {
			return &Result{Success: false, Error: fmt.Sprintf("blocked URL: %v", err)}, nil
		}
	}

	method, _ := args["method"].(string)
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	if body, ok := args["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("creating request: %v", err)}, nil
	}

	// Set headers.
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return &Result{Success: false, Error: fmt.Sprintf("executing request: %v", err)}, nil
	}
	defer resp.Body.Close()

	// Read response body, capped at 100KB.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	bodyStr := string(respBody)
	if stripHTML, ok := args["strip_html"].(bool); ok && stripHTML {
		bodyStr = extractText(respBody)
	}

	return &Result{Success: true, Data: map[string]interface{}{
		"status_code": resp.StatusCode,
		"status":      resp.Status,
		"body":        bodyStr,
		"headers":     flattenHeaders(resp.Header),
	}}, nil
}

// validateExternalURL blocks SSRF by rejecting dangerous schemes and private IPs.
func validateExternalURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("scheme %q not allowed (only http/https)", scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname")
	}
	// Resolve the hostname to IPs and check each one.
	ips, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("cannot resolve %q: %w", host, err)
	}
	for _, ipStr := range ips {
		ip := net.ParseIP(ipStr)
		if ip == nil {
			return fmt.Errorf("unparseable resolved IP %q", ipStr)
		}
		if isPrivateIP(ip) {
			return fmt.Errorf("resolved to private/internal IP %s", ipStr)
		}
	}
	return nil
}

// isPrivateIP returns true for loopback, link-local, and RFC-1918 addresses.
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// resolveLocalURL turns a relative path like "/api/drawings" into a full URL
// using the site's domain or falling back to localhost:5000.
func resolveLocalURL(ctx *ToolContext, path string) string {
	if ctx.GlobalDB != nil && ctx.SiteID > 0 {
		var domain sql.NullString
		ctx.GlobalDB.QueryRow("SELECT domain FROM sites WHERE id = ?", ctx.SiteID).Scan(&domain)
		if domain.Valid && domain.String != "" && domain.String != "localhost" {
			return "https://" + domain.String + path
		}
	}
	return "http://localhost:5000" + path
}

// flattenHeaders converts http.Header to a simple map.
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string)
	for k, v := range h {
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// extractText strips HTML tags and returns clean text content.
func extractText(htmlBytes []byte) string {
	tokenizer := html.NewTokenizer(strings.NewReader(string(htmlBytes)))
	var b strings.Builder
	skip := 0
	for {
		tt := tokenizer.Next()
		switch tt {
		case html.ErrorToken:
			// Collapse whitespace and trim.
			result := strings.Join(strings.Fields(b.String()), " ")
			if len(result) > 50000 {
				result = result[:50000]
			}
			return result
		case html.StartTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" {
				skip++
			}
		case html.EndTagToken:
			tn, _ := tokenizer.TagName()
			tag := string(tn)
			if tag == "script" || tag == "style" || tag == "noscript" {
				if skip > 0 {
					skip--
				}
			}
		case html.TextToken:
			if skip == 0 {
				b.WriteString(tokenizer.Token().Data)
				b.WriteByte(' ')
			}
		}
	}
}

func (t *MakeHTTPRequestTool) Summarize(result string) string {
	r, data, _, ok := parseSummaryResult(result)
	if !ok {
		return summarizeTruncate(result, 200)
	}
	if !r.Success {
		return summarizeError(r.Error)
	}
	if data == nil {
		return summarizeTruncate(result, 300)
	}
	status, _ := data["status_code"].(float64)
	body, _ := data["body"].(string)
	return fmt.Sprintf(`{"success":true,"summary":"HTTP %d (%d chars)"}`, int(status), len(body))
}
