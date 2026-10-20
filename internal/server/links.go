package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core): another
// Pocket app may act here for a person with a pcx_ token, only within these
// scopes. Every route not listed in linkRoutes stays closed to it.

var linkOffers = []core.Offer{
	{Scope: "documents:read", Name: "Read your documents", Description: "List and search your documents, read their text and metadata, download files and thumbnails (what Pocket Day shows in the journal)."},
	{Scope: "documents:write", Name: "Add and file documents", Description: "Upload documents, change their title, tags, correspondent, type, date and custom fields, add notes."},
}

// linkUses: a document (an invoice, a receipt) can be booked as a transaction in Pocket Money.
var linkUses = []core.Use{
	{App: "money", Scopes: []string{"accounts:read", "transactions:write"}, Reason: "Book an invoice or receipt as a transaction in Pocket Money, with a link back to the document"},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(documents|dashboard|statistics|tags|correspondents|document_types|storage_paths|custom_fields|taxonomy|saved_views|changes)$`), "documents:read"},
	{"GET", regexp.MustCompile(`^/api/v1/documents/\d+(/download|/preview|/thumb|/text|/notes|/history|/similar|/suggestions|/pages/\d+(\.jpg)?)?$`), "documents:read"},
	{"GET", regexp.MustCompile(`^/api/v1/documents/next_asn$`), "documents:read"},
	{"POST", regexp.MustCompile(`^/api/v1/documents(/post_document)?$`), "documents:write"},
	{"PUT PATCH", regexp.MustCompile(`^/api/v1/documents/\d+$`), "documents:write"},
	{"POST", regexp.MustCompile(`^/api/v1/documents/\d+/notes$`), "documents:write"},
	{"POST", regexp.MustCompile(`^/api/v1/(tags|correspondents|document_types)$`), "documents:write"},
}

var errLinkScope = errors.New("outside the link's scope")

func linkAllows(scopes []string, r *http.Request) bool {
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token and maps it to the person it acts for.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, err
	}
	if !in.Active || !in.Delegated() || in.User == nil || in.User.Email == "" {
		return nil, http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, errLinkScope
	}
	return s.firstOrSystemUser(ctx, strings.ToLower(in.User.Email), in.User.Name)
}

// linkAppOf names the acting app of a pcx_ request, for the history.
func linkAppOf(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return ""
	}
	tok := strings.TrimSpace(h[7:])
	if !core.IsLinkToken(tok) {
		return ""
	}
	return "a linked app"
}

// reportModes tells Core how each person reaches Money (Core → Links shows it).
func (s *Server) reportModes(ctx context.Context) {
	if !s.core.Configured() {
		return
	}
	var modes []core.ConnMode
	users, _ := s.store.ListUsers(ctx)
	for _, u := range users {
		st, err := s.store.GetSettings(ctx, u.ID)
		if err != nil {
			continue
		}
		mode := st.MoneyMode
		if mode == "direct" && st.MoneyToken == "" {
			mode = "off"
		}
		modes = append(modes, core.ConnMode{User: u.Email, Target: "money", Mode: mode})
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.core.ReportModes(c, modes); err != nil {
		s.log.Debug("pocket core: report modes", "err", err)
	}
}

// ---- Pocket Money: book a document as a transaction ----

var moneyScopes = []string{"accounts:read", "transactions:write"}

type moneyLink struct {
	base, token, via, public string
}

func linkReason(reason string) string {
	switch reason {
	case "off_person":
		return "you switched the Documents → Money link off in Pocket Core (Settings → Apps acting for me)"
	case "off_admin":
		return "the Documents → Money link is switched off in Pocket Core (Links)"
	case "target_missing":
		return "Pocket Money is not registered with Pocket Core"
	case "not_offered":
		return "this Pocket Money version does not accept app links"
	case "not_declared":
		return "Pocket Core has not seen this Documents version yet; it re-registers within a minute of starting"
	case "not_in_audience":
		return "you do not have access to Pocket Money in Pocket Core"
	case "unknown_user":
		return "your email is not a person in Pocket Core"
	}
	return ""
}

func (s *Server) moneyFor(ctx context.Context, u *store.User) (*moneyLink, error) {
	st, _ := s.store.GetSettings(ctx, u.ID)
	switch st.MoneyMode {
	case "off":
		return nil, errors.New("Pocket Money is switched off in Settings → Integrations")
	case "direct":
		if st.MoneyToken == "" {
			return nil, errors.New("Pocket Money is set to a direct connection but has no token (Settings → Integrations)")
		}
		base := firstNonEmpty(st.MoneyURL, s.resolveSibling(ctx, "money"))
		if base == "" {
			return nil, errors.New("set the Pocket Money URL in Settings → Integrations")
		}
		return &moneyLink{base: strings.TrimRight(base, "/"), token: st.MoneyToken, via: "direct", public: strings.TrimRight(base, "/")}, nil
	}
	if !s.core.Configured() {
		return nil, errors.New("there is no Pocket Core on this box; use a direct connection with a Money token")
	}
	tok, err := s.core.ActAs(ctx, u.Email, "money", moneyScopes...)
	if err != nil {
		var le *core.LinkError
		if errors.As(err, &le) {
			if t := linkReason(le.Reason); t != "" {
				return nil, errors.New(t)
			}
		}
		return nil, err
	}
	return &moneyLink{base: strings.TrimRight(tok.URL, "/"), token: tok.AccessToken, via: "core", public: strings.TrimRight(firstNonEmpty(tok.PublicURL, tok.URL), "/")}, nil
}

func (m *moneyLink) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.base+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		return fmt.Errorf("pocket money: %s", firstNonEmpty(e.Error, http.StatusText(res.StatusCode)))
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}

type moneyAccount struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Currency string `json:"currency"`
	Everyday bool   `json:"everyday"`
	Archived bool   `json:"archived"`
	Role     string `json:"role"`
}

type moneyCategory struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Kind     string `json:"kind"`
	Archived bool   `json:"archived"`
}

// handleMoneyStatus tells the document page whether booking is possible and where.
func (s *Server) handleMoneyStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := userFrom(ctx)
	st, _ := s.store.GetSettings(ctx, u.ID)
	out := map[string]any{"connected": false, "via": "", "error": "", "core": s.core.Configured(), "accounts": []moneyAccount{}, "categories": []moneyCategory{}, "default_account": st.MoneyAccount, "url": ""}
	if st.MoneyMode == "off" {
		out["error"] = "switched off"
		writeJSON(w, http.StatusOK, out)
		return
	}
	ml, err := s.moneyFor(ctx, u)
	if err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["via"], out["url"] = ml.via, ml.public
	var all []moneyAccount
	if err := ml.do(ctx, http.MethodGet, "/api/v1/accounts", nil, &all); err != nil {
		out["error"] = err.Error()
		writeJSON(w, http.StatusOK, out)
		return
	}
	accts := []moneyAccount{}
	for _, a := range all {
		if !a.Archived && a.Role != "viewer" {
			accts = append(accts, a)
		}
	}
	var cats []moneyCategory
	acct := st.MoneyAccount
	if q := r.URL.Query().Get("account"); q != "" {
		acct, _ = strconv.ParseInt(q, 10, 64)
	}
	catPath := "/api/v1/categories"
	if acct > 0 {
		catPath = fmt.Sprintf("/api/v1/accounts/%d/categories", acct)
	}
	_ = ml.do(ctx, http.MethodGet, catPath, nil, &cats)
	keep := []moneyCategory{}
	for _, c := range cats {
		if !c.Archived && c.Kind != "income" {
			keep = append(keep, c)
		}
	}
	out["connected"], out["accounts"], out["categories"] = true, accts, keep
	writeJSON(w, http.StatusOK, out)
}

// handleBookMoney creates an expense in the person's Pocket Money for a document.
func (s *Server) handleBookMoney(w http.ResponseWriter, r *http.Request) {
	ctx, uid := r.Context(), me(r)
	u := userFrom(ctx)
	d, err := s.store.GetDocument(ctx, uid, idParam(r, "id"), false)
	if err != nil {
		s.fail(w, err, "document")
		return
	}
	var in struct {
		Amount      float64 `json:"amount"`
		Currency    string  `json:"currency"`
		AccountID   int64   `json:"account_id"`
		CategoryID  *int64  `json:"category_id"`
		Date        string  `json:"date"`
		Description string  `json:"description"`
		Payee       string  `json:"payee"`
		Notes       string  `json:"notes"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if in.Amount <= 0 || in.AccountID == 0 {
		writeErr(w, http.StatusBadRequest, "an amount and an account are needed")
		return
	}
	ml, err := s.moneyFor(ctx, u)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	cur := strings.ToUpper(strings.TrimSpace(in.Currency))
	if cur == "" {
		cur = "EUR"
	}
	link := ""
	if s.cfg.BaseURL != "" {
		link = "\n" + s.cfg.BaseURL + "/documents/" + strconv.FormatInt(d.ID, 10)
	}
	names := s.store.Names(ctx, uid)
	payee := in.Payee
	if payee == "" && d.CorrespondentID != nil {
		payee = names.Correspondents[*d.CorrespondentID]
	}
	body := map[string]any{"kind": "expense", "account_id": in.AccountID, "date": firstNonEmpty(normalizeDate(in.Date), d.Created), "amount": int64(in.Amount*100 + 0.5), "currency": cur,
		"description": firstNonEmpty(strings.TrimSpace(in.Description), d.Title), "payee": payee, "tags": []string{}, "notes": strings.TrimSpace("Pocket Documents · "+d.Title+"\n"+in.Notes) + link,
		"source": "documents", "external_id": fmt.Sprintf("document:%d", d.ID)}
	if in.CategoryID != nil {
		body["category_id"] = *in.CategoryID
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := ml.do(ctx, http.MethodPost, "/api/v1/transactions", body, &created); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	_ = s.store.SetUserSetting(ctx, uid, "money_account", strconv.FormatInt(in.AccountID, 10))
	s.store.AddHistory(ctx, d.ID, uid, actorName(r), "booked", map[string]any{"money_transaction": created.ID, "amount": in.Amount, "currency": cur, "via": ml.via})
	writeJSON(w, http.StatusCreated, map[string]any{"transaction": created.ID, "url": ml.public, "via": ml.via})
}
