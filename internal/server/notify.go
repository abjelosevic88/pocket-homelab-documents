package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// sendNotify posts a message to an Apprise API URL (…/notify/<key>) or an
// ntfy topic URL. Both accept a JSON body; ntfy also reads the headers.
func sendNotify(url, title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	payload := map[string]any{"title": title, "body": body, "message": body}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.Contains(url, "ntfy") {
		req.Header.Set("Title", title)
		req.Body = http.NoBody
		req, _ = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
		req.Header.Set("Title", title)
		req.Header.Set("Tags", "page_facing_up")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return &notifyErr{res.StatusCode}
	}
	return nil
}

type notifyErr struct{ code int }

func (e *notifyErr) Error() string {
	return "notification endpoint answered HTTP " + http.StatusText(e.code)
}
