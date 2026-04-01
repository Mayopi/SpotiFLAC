package backend

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

// httpError reads the response body (up to 512 bytes) and returns a detailed
// error that includes the API name, request URL, HTTP status code/text, and a
// preview of the response body so that failures are easy to diagnose from the
// terminal output.
func httpError(apiName string, resp *http.Response) error {
	status := resp.StatusCode
	statusText := http.StatusText(status)

	bodyPreview := ""
	if resp.Body != nil {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		bodyPreview = strings.TrimSpace(string(raw))
	}

	url := ""
	if resp.Request != nil && resp.Request.URL != nil {
		url = resp.Request.URL.String()
		// Truncate very long URLs to keep logs readable
		if len(url) > 120 {
			url = url[:120] + "..."
		}
	}

	if bodyPreview != "" {
		return fmt.Errorf("[%s] HTTP %d %s | URL: %s | Response: %s", apiName, status, statusText, url, bodyPreview)
	}
	return fmt.Errorf("[%s] HTTP %d %s | URL: %s", apiName, status, statusText, url)
}
