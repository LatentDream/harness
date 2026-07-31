package webfetch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWebfetchPlainText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "hello world")
	}))
	defer server.Close()

	output, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "", 0))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if output != "hello world" {
		t.Fatalf("output = %q, want %q", output, "hello world")
	}
}

func TestWebfetchConvertsHTMLToMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<h1>Title</h1><p>Hello <strong>world</strong>.</p><a href="/next">Next</a>`)
	}))
	defer server.Close()

	output, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "markdown", 0))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	for _, want := range []string{"# Title", "Hello **world**.", server.URL + "/next"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestWebfetchConvertsHTMLToText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<h1>Title</h1><script>ignored()</script><p>Hello <strong>world</strong>.</p>`)
	}))
	defer server.Close()

	output, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "text", 0))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if strings.Contains(output, "ignored") {
		t.Fatalf("output includes skipped script text: %q", output)
	}
	for _, want := range []string{"Title", "Hello", "world"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestWebfetchReturnsRawHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<p>Hello</p>`)
	}))
	defer server.Close()

	output, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "html", 0))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if output != `<p>Hello</p>` {
		t.Fatalf("output = %q, want raw HTML", output)
	}
}

func TestWebfetchTreatsSVGAsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprint(w, `<svg><text>Hello</text></svg>`)
	}))
	defer server.Close()

	output, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "", 0))
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if output != `<svg><text>Hello</text></svg>` {
		t.Fatalf("output = %q, want SVG text", output)
	}
}

func TestWebfetchRejectsBinaryImage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte{0x89, 'P', 'N', 'G'})
	}))
	defer server.Close()

	_, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "", 0))
	if err == nil || !strings.Contains(err.Error(), "does not support binary image") {
		t.Fatalf("err = %v, want unsupported image error", err)
	}
}

func TestWebfetchRejectsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	_, err := New().Execute(context.Background(), webfetchArgs(t, server.URL, "", 0))
	if err == nil || !strings.Contains(err.Error(), "HTTP 404") {
		t.Fatalf("err = %v, want HTTP status error", err)
	}
}

func TestWebfetchRejectsInvalidArguments(t *testing.T) {
	tests := []string{
		`{}`,
		`{"url":"ftp://example.com"}`,
		`{"url":"https://example.com","format":"pdf"}`,
		`{"url":"https://example.com","timeoutSeconds":0}`,
		`{"url":"https://example.com","extra":true}`,
	}
	for _, args := range tests {
		t.Run(args, func(t *testing.T) {
			_, err := New().Execute(context.Background(), []byte(args))
			if err == nil {
				t.Fatal("Execute succeeded, want error")
			}
		})
	}
}

func TestWebfetchHonorsTimeout(t *testing.T) {
	tool := webfetchTool{
		client:         blockingClient{},
		defaultTimeout: 10 * time.Millisecond,
		maxTimeout:     time.Second,
	}

	_, err := tool.Execute(context.Background(), webfetchArgs(t, "https://example.com", "", 0))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestWebfetchRejectsLargeContentLength(t *testing.T) {
	response := &http.Response{
		ContentLength: maxResponseBytes + 1,
		Body:          http.NoBody,
	}
	_, err := readLimitedBody(response)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want too large error", err)
	}
}

func TestWebfetchRejectsLargeBody(t *testing.T) {
	response := &http.Response{
		ContentLength: -1,
		Body:          ioReadCloser{strings.NewReader(strings.Repeat("a", maxResponseBytes+1))},
	}
	_, err := readLimitedBody(response)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("err = %v, want too large error", err)
	}
}

func TestWebfetchDefinition(t *testing.T) {
	definition := New().Definition()
	if definition.Name != "webfetch" {
		t.Fatalf("Name = %q, want webfetch", definition.Name)
	}
	format := definition.Parameters.Properties["format"]
	if got := strings.Join(format.Enum, ","); got != "markdown,text,html" {
		t.Fatalf("format enum = %q", got)
	}
	if len(definition.Parameters.Required) != 1 || definition.Parameters.Required[0] != "url" {
		t.Fatalf("required = %#v, want url", definition.Parameters.Required)
	}
}

type blockingClient struct{}

func (blockingClient) Do(request *http.Request) (*http.Response, error) {
	<-request.Context().Done()
	return nil, request.Context().Err()
}

type ioReadCloser struct {
	*strings.Reader
}

func (closer ioReadCloser) Close() error {
	return nil
}

func webfetchArgs(t *testing.T, rawURL string, format string, timeoutSeconds int) []byte {
	t.Helper()
	values := map[string]any{"url": rawURL}
	if format != "" {
		values["format"] = format
	}
	if timeoutSeconds != 0 {
		values["timeoutSeconds"] = timeoutSeconds
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return encoded
}
