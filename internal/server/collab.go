package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
)

// Pocket Review: a document (its PDF/A twin, or the original when it is
// a PDF) can be sent for review. The review keeps "documents:<id>" as its
// source, so the archive shows an "in review" badge and a second send
// becomes a new revision. Reached through a Core app link as the person.

var collabScopes = []string{"reviews:read", "reviews:write"}

type collabLink struct{ base, public, token string }

type collabHit struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	URL         string `json:"url"`
	Revisions   int    `json:"revisions"`
	OpenThreads int    `json:"open_threads"`
	OpenIssues  int    `json:"open_issues"`
	UpdatedAt   string `json:"updated_at"`
	MyRole      string `json:"my_role"`
}

func (s *Server) collabFor(ctx context.Context, email string) (*collabLink, error) {
	if !s.core.Configured() {
		return nil, errors.New("there is no Pocket Core on this box")
	}
	lt, err := s.core.ActAs(ctx, email, "review", collabScopes...)
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			return nil, errors.New("Pocket Review through Pocket Core: " + firstNonEmpty(strings.ReplaceAll(linkReason(le.Reason), "Money", "Pocket Review"), le.Message))
		}
		return nil, err
	}
	return &collabLink{base: strings.TrimRight(lt.URL, "/"), public: strings.TrimRight(firstNonEmpty(lt.PublicURL, lt.URL), "/"), token: lt.AccessToken}, nil
}

func (l *collabLink) do(ctx context.Context, method, path string, body io.Reader, ctype string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, l.base+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+l.token)
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	res, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("pocket review: %s", firstNonEmpty(e.Error, http.StatusText(res.StatusCode)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

func docRef(id int64) string { return "documents:" + strconv.FormatInt(id, 10) }

// handleCollabLookup returns the reviews of the given documents (?ids=1,2,3).
func (s *Server) handleCollabLookup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	out := map[string]any{"available": false, "reviews": map[string]collabHit{}}
	if !s.core.Configured() {
		writeJSON(w, http.StatusOK, out)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	l, err := s.collabFor(cctx, u.Email)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["available"], out["url"] = true, l.public
	q := url.Values{}
	n := 0
	for _, part := range strings.Split(r.URL.Query().Get("ids"), ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64); err == nil && id > 0 && n < 400 {
			q.Add("ref", docRef(id))
			n++
		}
	}
	if n == 0 {
		writeJSON(w, http.StatusOK, out)
		return
	}
	var ans struct {
		Reviews map[string]collabHit `json:"reviews"`
	}
	if err := l.do(cctx, http.MethodGet, "/api/v1/reviews/lookup?"+q.Encode(), nil, "", &ans); err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	byID := map[string]collabHit{}
	for ref, hit := range ans.Reviews {
		byID[strings.TrimPrefix(ref, "documents:")] = hit
	}
	out["reviews"] = byID
	writeJSON(w, http.StatusOK, out)
}

// handleCollabSend sends the document's PDF to Pocket Review.
func (s *Server) handleCollabSend(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	u := userFrom(ctx)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	var in struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Note        string `json:"note"`
		DueDate     string `json:"due_date"`
		Mode        string `json:"mode"`
		Start       bool   `json:"start"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	path, name, ctype := s.bestFile(d)
	if ctype != "application/pdf" {
		writeErr(w, http.StatusBadRequest, "this document has no PDF yet (Pocket Review works on PDFs); run OCR or set GOTENBERG_URL so office files get a PDF twin")
		return
	}
	pdf, err := os.ReadFile(path)
	if err != nil {
		s.fail(w, err, "read file")
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	l, err := s.collabFor(cctx, u.Email)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fields := map[string]string{
		"title": firstNonEmpty(strings.TrimSpace(in.Title), d.Title), "description": strings.TrimSpace(in.Description), "note": strings.TrimSpace(in.Note), "due_date": strings.TrimSpace(in.DueDate), "mode": in.Mode,
		"source_app": "documents", "source_ref": docRef(d.ID), "source_url": strings.TrimRight(s.cfg.BaseURL, "/") + "/documents/" + strconv.FormatInt(d.ID, 10), "source_label": d.Title,
		"page_count": strconv.Itoa(d.PageCount),
	}
	if in.Start {
		fields["start"] = "1"
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	part, _ := mw.CreateFormFile("file", name)
	_, _ = part.Write(pdf)
	_ = mw.Close()
	var ans struct {
		Review struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			Status string `json:"status"`
		} `json:"review"`
		Revision struct {
			Number int `json:"number"`
		} `json:"revision"`
		Created bool   `json:"created"`
		URL     string `json:"url"`
	}
	if err := l.do(cctx, http.MethodPost, "/api/v1/reviews/import", &buf, mw.FormDataContentType(), &ans); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.store.AddHistory(ctx, d.ID, uid, actorName(r), "sent for review", map[string]any{"review": ans.Review.ID, "revision": ans.Revision.Number, "created": ans.Created, "url": ans.URL})
	writeJSON(w, http.StatusOK, map[string]any{"review": ans.Review, "revision": ans.Revision.Number, "created": ans.Created, "url": firstNonEmpty(ans.URL, l.public+"/reviews/"+strconv.FormatInt(ans.Review.ID, 10))})
}
