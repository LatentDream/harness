package webfetch

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"golang.org/x/net/html"

	"latentdream/harness/internal/session/llm"
	"latentdream/harness/internal/tool/model"
	"latentdream/harness/internal/tool/utils"
)

const (
	defaultTimeoutSeconds = 30
	maxTimeoutSeconds     = 120
	maxResponseBytes      = 5 * 1024 * 1024
)

//go:embed webfetch.txt
var webfetchDescription string

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type webfetchTool struct {
	client         httpDoer
	defaultTimeout time.Duration
	maxTimeout     time.Duration
}

type webfetchParams struct {
	url     string
	format  string
	timeout time.Duration
}

func New() model.Tool {
	return webfetchTool{
		client:         http.DefaultClient,
		defaultTimeout: defaultTimeoutSeconds * time.Second,
		maxTimeout:     maxTimeoutSeconds * time.Second,
	}
}

func (webfetchTool) Definition() llm.ToolDefinition {
	additionalProperties := false
	return llm.ToolDefinition{
		Name:        "webfetch",
		Description: strings.TrimSpace(webfetchDescription),
		Parameters: llm.Schema{
			Type: "object",
			Properties: map[string]llm.Schema{
				"url": {
					Type:        "string",
					Description: "The fully-formed URL to fetch. Must start with http:// or https://.",
				},
				"format": {
					Type:        "string",
					Description: "Output format. Defaults to markdown.",
					Enum:        []string{"markdown", "text", "html"},
				},
				"timeoutSeconds": {
					Type:        "number",
					Description: "Maximum request duration in seconds. Defaults to 30 and is capped at 120.",
				},
			},
			Required:             []string{"url"},
			AdditionalProperties: &additionalProperties,
		},
	}
}

func (webfetchTool) Capability() model.Capability {
	return model.CapabilityReadOnly
}

func (webfetchTool) Status(args json.RawMessage) string {
	params, err := decodeWebfetchParams(args, defaultTimeoutSeconds*time.Second, maxTimeoutSeconds*time.Second)
	if err != nil {
		return "Fetching webpage"
	}
	return "Fetching webpage " + params.url
}

func (webfetchTool) Present(args json.RawMessage, _ string, _ error) model.Activity {
	params, err := decodeWebfetchParams(args, defaultTimeoutSeconds*time.Second, maxTimeoutSeconds*time.Second)
	if err != nil {
		return model.Activity{}
	}
	return model.Activity{Target: params.url}
}

func (tool webfetchTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	params, err := decodeWebfetchParams(args, tool.defaultTimeout, tool.maxTimeout)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	requestCtx, cancel := context.WithTimeout(ctx, params.timeout)
	defer cancel()

	response, err := tool.fetch(requestCtx, params, browserUserAgent)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	contentType := response.Header.Get("Content-Type")
	mimeType := mediaType(contentType)
	if isUnsupportedImage(mimeType) {
		return "", fmt.Errorf("webfetch does not support binary image responses yet: %s", mimeType)
	}

	body, err := readLimitedBody(response)
	if err != nil {
		return "", err
	}
	text := strings.ToValidUTF8(string(body), "?")

	if params.format == "html" || !isHTML(mimeType) {
		return text, nil
	}
	if params.format == "text" {
		return htmlToText(text), nil
	}

	parsed, _ := url.Parse(params.url)
	markdown, err := htmltomarkdown.ConvertString(text, converter.WithDomain(parsed.Scheme+"://"+parsed.Host))
	if err != nil {
		return "", fmt.Errorf("convert HTML to markdown: %w", err)
	}
	return markdown, nil
}

func (tool webfetchTool) fetch(ctx context.Context, params webfetchParams, userAgent string) (*http.Response, error) {
	client := tool.client
	if client == nil {
		client = http.DefaultClient
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, params.url, nil)
	if err != nil {
		return nil, fmt.Errorf("create webfetch request: %w", err)
	}
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", acceptHeader(params.format))
	request.Header.Set("Accept-Language", "en-US,en;q=0.9")

	response, err := client.Do(request)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("fetch %q: %w", params.url, err)
	}
	if response.StatusCode == http.StatusForbidden && strings.EqualFold(response.Header.Get("cf-mitigated"), "challenge") && userAgent != fallbackUserAgent {
		response.Body.Close()
		return tool.fetch(ctx, params, fallbackUserAgent)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		response.Body.Close()
		return nil, fmt.Errorf("fetch %q returned HTTP %d", params.url, response.StatusCode)
	}
	return response, nil
}

func decodeWebfetchParams(args json.RawMessage, defaultTimeout time.Duration, maxTimeout time.Duration) (webfetchParams, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		return webfetchParams{}, errors.New("webfetch arguments must be a JSON object")
	}

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(args))
	decoder.UseNumber()
	if err := decoder.Decode(&raw); err != nil {
		return webfetchParams{}, fmt.Errorf("decode webfetch arguments: %w", err)
	}
	if raw == nil {
		return webfetchParams{}, errors.New("webfetch arguments must be a JSON object")
	}
	for name := range raw {
		switch name {
		case "url", "format", "timeoutSeconds":
		default:
			return webfetchParams{}, fmt.Errorf("webfetch argument %q is not supported", name)
		}
	}

	value, err := utils.ParseRequiredString(raw, "url")
	if err != nil {
		return webfetchParams{}, err
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return webfetchParams{}, errors.New("url must be a fully-formed URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return webfetchParams{}, errors.New("url must start with http:// or https://")
	}

	format, err := optionalFormat(raw)
	if err != nil {
		return webfetchParams{}, err
	}
	if format == "" {
		format = "markdown"
	}

	timeout := defaultTimeout
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds * time.Second
	}
	if maxTimeout <= 0 {
		maxTimeout = maxTimeoutSeconds * time.Second
	}
	timeoutSeconds, ok, err := utils.ParseOptionalPositiveInt(raw, "timeoutSeconds")
	if err != nil {
		return webfetchParams{}, err
	}
	if ok {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}
	if timeout > maxTimeout {
		timeout = maxTimeout
	}

	return webfetchParams{url: value, format: format, timeout: timeout}, nil
}

func optionalFormat(raw map[string]json.RawMessage) (string, error) {
	value, ok := raw["format"]
	if !ok {
		return "", nil
	}
	var parsed string
	if err := json.Unmarshal(value, &parsed); err != nil {
		return "", errors.New("format must be a string")
	}
	parsed = strings.TrimSpace(parsed)
	switch parsed {
	case "markdown", "text", "html":
		return parsed, nil
	case "":
		return "", errors.New("format must not be empty")
	default:
		return "", errors.New("format must be markdown, text, or html")
	}
}

func readLimitedBody(response *http.Response) ([]byte, error) {
	if response.ContentLength > maxResponseBytes {
		return nil, fmt.Errorf("response is too large: %d bytes exceeds %d bytes", response.ContentLength, maxResponseBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("response is too large: exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}

func mediaType(contentType string) string {
	parsed, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	return strings.ToLower(parsed)
}

func isHTML(mimeType string) bool {
	return mimeType == "text/html" || mimeType == "application/xhtml+xml"
}

func isUnsupportedImage(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/") && mimeType != "image/svg+xml" && mimeType != "image/vnd.fastbidsheet"
}

func acceptHeader(format string) string {
	if format == "html" {
		return "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8"
	}
	return "text/html,application/xhtml+xml,text/plain,application/json;q=0.9,*/*;q=0.8"
}

func htmlToText(value string) string {
	node, err := html.Parse(strings.NewReader(value))
	if err != nil {
		return value
	}

	var parts []string
	var walk func(*html.Node, bool)
	walk = func(node *html.Node, skip bool) {
		if node.Type == html.ElementNode && skipTextElement(node.Data) {
			skip = true
		}
		if node.Type == html.TextNode && !skip {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child, skip)
		}
	}
	walk(node, false)
	return strings.Join(parts, "\n")
}

func skipTextElement(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "noscript", "iframe", "object", "embed":
		return true
	default:
		return false
	}
}

const (
	browserUserAgent  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	fallbackUserAgent = "harness"
)
