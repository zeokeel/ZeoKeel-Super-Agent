package agent

// Web tools for a Worker: fetch one public URL as readable text, and search the
// web for a query. Both are deliberately small and dependency-free (stdlib only):
// the product needs "look at this page" and "find a page", not a browser engine.

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

const (
	webFetchTimeout   = 10 * time.Second
	webFetchMaxBytes  = 2 << 20 // 2 MiB body cap
	webFetchMaxOutput = 30 << 10
	webSearchMaxHits  = 5
	webUserAgent      = "Mozilla/5.0 (compatible; ZeoKeel/1.0; +https://github.com/zeokeel)"
	defaultSearchURL  = "https://duckduckgo.com/html/?q=%s"
)

const (
	webFetchDesc = "Fetch one http(s) URL and return the page as readable text: " +
		"the title, then the body with scripts, styles and tags removed. " +
		"Use web_search first when you do not have a URL. Only public http(s) URLs are fetched."
	webSearchDesc = "Search the web with one query and return the top results as " +
		"`title — url` plus a snippet. Use web_fetch to read a result in full."
)

// webFetchArgs / webSearchArgs are the model-facing schemas.
type webFetchArgs struct {
	URL string `json:"url" jsonschema:"description=absolute http(s) URL to fetch"`
}

type webSearchArgs struct {
	Query string `json:"query" jsonschema:"description=the search query, in natural language or keywords"`
}

// newWebTools builds the two web tools. SearchURL may be empty (default endpoint).
func newWebTools(searchURL string) ([]tool.BaseTool, error) {
	fetch, err := newWebFetchTool(false)
	if err != nil {
		return nil, err
	}
	search, err := newWebSearchTool(searchURL)
	if err != nil {
		return nil, err
	}
	return []tool.BaseTool{fetch, search}, nil
}

// newWebFetchTool builds web_fetch. allowPrivate exists for tests: the production
// tool must not reach loopback/private addresses (SSRF).
func newWebFetchTool(allowPrivate bool) (tool.BaseTool, error) {
	return utils.InferTool("web_fetch", webFetchDesc,
		func(ctx context.Context, in webFetchArgs) (string, error) {
			text, err := fetchWeb(ctx, in.URL, allowPrivate)
			if err != nil {
				// A URL that cannot be fetched is an observation the model can act
				// on (try another URL), not a failed turn.
				return "web_fetch failed: " + err.Error(), nil
			}
			return text, nil
		})
}

func newWebSearchTool(searchURL string) (tool.BaseTool, error) {
	return utils.InferTool("web_search", webSearchDesc,
		func(ctx context.Context, in webSearchArgs) (string, error) {
			out, err := searchWeb(ctx, searchURL, in.Query)
			if err != nil {
				return "web_search failed: " + err.Error(), nil
			}
			return out, nil
		})
}

// fetchWeb fetches one URL and renders it as "title\n\nbody". It rejects
// non-http(s) schemes and, unless allowPrivate is set, loopback/private targets
// and redirects to them. The body is capped so a huge page cannot exhaust memory.
func fetchWeb(ctx context.Context, rawURL string, allowPrivate bool) (string, error) {
	u, err := validateWebURL(ctx, rawURL, allowPrivate)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: webFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("too many redirects")
			}
			_, err := validateWebURL(req.Context(), req.URL.String(), allowPrivate)
			return err
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.5")

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("no response from %s within %s", u.Host, webFetchTimeout)
		}
		return "", fmt.Errorf("request to %s failed: %w", u.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("server returned HTTP %d for %s", resp.StatusCode, u.String())
	}
	body, err := readBodyLimited(resp.Body, webFetchMaxBytes)
	if err != nil {
		return "", err
	}
	title, text := htmlToText(string(body))
	if title == "" && text == "" {
		return "", fmt.Errorf("no readable text at %s (the page may be binary or empty)", u.String())
	}
	out := strings.TrimSpace(title)
	if body := strings.TrimSpace(text); body != "" {
		if out != "" {
			out += "\n\n"
		}
		out += body
	}
	return clipUTF8(out, webFetchMaxOutput), nil
}

// validateWebURL enforces the fetch policy: absolute http(s) URL, and (unless
// allowPrivate) a host that resolves only to public addresses.
func validateWebURL(ctx context.Context, rawURL string, allowPrivate bool) (*url.URL, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("url is required")
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid url %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("only http(s) URLs are supported (got %q)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("url %q has no host", rawURL)
	}
	if allowPrivate {
		return u, nil
	}
	host := u.Hostname()
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") || strings.HasSuffix(lower, ".local") {
		return nil, fmt.Errorf("%s is a local host; only public URLs are fetched", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return nil, fmt.Errorf("%s is a private address; only public URLs are fetched", host)
		}
		return u, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve %s: %w", host, err)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%s did not resolve to any address", host)
	}
	for _, addr := range addrs {
		if isPrivateIP(addr.IP) {
			return nil, fmt.Errorf("%s resolves to a private address (%s); only public URLs are fetched", host, addr.IP)
		}
	}
	return u, nil
}

// privateCIDRs are ranges net.IP.IsPrivate does not cover but a browser-like
// fetch must not reach (carrier NAT, benchmarking, IETF protocol assignments).
var privateCIDRs = func() []*net.IPNet {
	out := make([]*net.IPNet, 0, 3)
	for _, cidr := range []string{"100.64.0.0/10", "192.0.0.0/24", "198.18.0.0/15"} {
		if _, block, err := net.ParseCIDR(cidr); err == nil {
			out = append(out, block)
		}
	}
	return out
}()

func isPrivateIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, block := range privateCIDRs {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// readBodyLimited reads at most limit bytes and reports an actionable error when
// the response is larger than the cap.
func readBodyLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response body exceeds the %d MB limit", limit>>20)
	}
	return data, nil
}

var (
	reHTMLTitle    = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reHTMLScript   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reHTMLStyle    = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reHTMLNoScript = regexp.MustCompile(`(?is)<noscript\b[^>]*>.*?</noscript>`)
	reHTMLComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	reHTMLBlock    = regexp.MustCompile(`(?is)<br\s*/?>|</(?:p|div|section|article|header|footer|h[1-6]|li|tr|td|table|ul|ol|blockquote|pre|nav|main|aside)\s*>`)
	reHTMLTag      = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace        = regexp.MustCompile(`[ \t\f\v]+`)
	reBlankLines   = regexp.MustCompile(`\n{3,}`)
)

// htmlToText renders HTML as a title plus plain text. It is intentionally a small
// tag stripper rather than a DOM parser: it must handle arbitrary pages without
// new dependencies, and the model only needs readable prose.
func htmlToText(body string) (title, text string) {
	if m := reHTMLTitle.FindStringSubmatch(body); len(m) > 1 {
		title = cleanHTMLText(m[1])
	}
	s := reHTMLComment.ReplaceAllString(body, " ")
	s = reHTMLScript.ReplaceAllString(s, " ")
	s = reHTMLStyle.ReplaceAllString(s, " ")
	s = reHTMLNoScript.ReplaceAllString(s, " ")
	// The <title> text already became the title; drop it from the body so it is
	// not reported twice.
	s = reHTMLTitle.ReplaceAllString(s, " ")
	s = reHTMLBlock.ReplaceAllString(s, "\n")
	// Inline tags vanish without a space: "Second <b>paragraph</b>." must read
	// "Second paragraph." and not "Second paragraph .".
	s = reHTMLTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)

	lines := make([]string, 0, 64)
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(reSpace.ReplaceAllString(line, " "))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	text = reBlankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
	return title, strings.TrimSpace(text)
}

// cleanHTMLText strips tags/entities from a fragment (a title or a search result)
// and collapses whitespace.
func cleanHTMLText(s string) string {
	s = reHTMLTag.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.TrimSpace(reSpace.ReplaceAllString(s, " "))
}

// clipUTF8 truncates to at most max bytes without splitting a rune.
func clipUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimRight(s[:cut], " \n\t") + "\n… (truncated)"
}

// searchWeb runs the configured HTML search endpoint and returns up to five
// "title — url\nsnippet" entries.
func searchWeb(ctx context.Context, endpoint, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", errors.New("query is required")
	}
	if endpoint == "" {
		endpoint = defaultSearchURL
	}
	if !strings.Contains(endpoint, "%s") {
		return "", fmt.Errorf("search endpoint %q has no %%s slot for the query", endpoint)
	}
	raw := fmt.Sprintf(endpoint, url.QueryEscape(query))
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("search endpoint %q is not an absolute http(s) URL", raw)
	}
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("User-Agent", webUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := (&http.Client{Timeout: webFetchTimeout}).Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return "", fmt.Errorf("no response from %s within %s", u.Host, webFetchTimeout)
		}
		return "", fmt.Errorf("search request to %s failed: %w", u.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("search endpoint %s returned HTTP %d", u.Host, resp.StatusCode)
	}
	body, err := readBodyLimited(resp.Body, webFetchMaxBytes)
	if err != nil {
		return "", err
	}
	hits := parseSearchResults(string(body), u)
	if len(hits) == 0 {
		return fmt.Sprintf("web_search: no results for %q", query), nil
	}
	if len(hits) > webSearchMaxHits {
		hits = hits[:webSearchMaxHits]
	}
	var b strings.Builder
	for i, hit := range hits {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(hit.title)
		b.WriteString(" — ")
		b.WriteString(hit.url)
		if hit.snippet != "" {
			b.WriteString("\n")
			b.WriteString(hit.snippet)
		}
	}
	return b.String(), nil
}

type searchHit struct {
	title   string
	url     string
	snippet string
}

var (
	reAnchor   = regexp.MustCompile(`(?is)<a\b([^>]*)>(.*?)</a>`)
	reAttr     = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*"([^"]*)"`)
	reAttrBare = regexp.MustCompile(`(?is)([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*'([^']*)'`)
)

// parseSearchResults understands the DuckDuckGo HTML result layout: anchors with
// class result__a (title + href) followed by an anchor with class result__snippet.
// It also works with the same markup hosted on a test server.
func parseSearchResults(body string, base *url.URL) []searchHit {
	var hits []searchHit
	for _, m := range reAnchor.FindAllStringSubmatch(body, -1) {
		attrs, inner := m[1], m[2]
		class := htmlAttr(attrs, "class")
		if class == "" {
			continue
		}
		switch {
		case strings.Contains(class, "result__a"):
			href := htmlAttr(attrs, "href")
			if href == "" {
				continue
			}
			hits = append(hits, searchHit{
				title: cleanHTMLText(inner),
				url:   cleanSearchURL(href, base),
			})
		case strings.Contains(class, "result__snippet"):
			if len(hits) > 0 && hits[len(hits)-1].snippet == "" {
				hits[len(hits)-1].snippet = cleanHTMLText(inner)
			}
		}
	}
	// Drop entries without a title or URL; keep order.
	out := hits[:0]
	for _, hit := range hits {
		if hit.title == "" || hit.url == "" {
			continue
		}
		out = append(out, hit)
	}
	return out
}

func htmlAttr(attrs, name string) string {
	for _, re := range []*regexp.Regexp{reAttr, reAttrBare} {
		for _, m := range re.FindAllStringSubmatch(attrs, -1) {
			if strings.EqualFold(m[1], name) {
				return html.UnescapeString(m[2])
			}
		}
	}
	return ""
}

// cleanSearchURL resolves relative/redirect-wrapped result URLs and strips
// DuckDuckGo's /l/?uddg= redirect wrapper.
func cleanSearchURL(raw string, base *url.URL) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if !u.IsAbs() && base != nil {
		u = base.ResolveReference(u)
	}
	// DuckDuckGo wraps result links as /l/?uddg=<encoded target>. url.Values.Get
	// already decodes the value, so do not unescape a second time.
	if target := u.Query().Get("uddg"); target != "" {
		if parsed, err := url.Parse(target); err == nil {
			return parsed.String()
		}
		return target
	}
	return u.String()
}
