package server

import (
	"net/http"
	"strings"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
)

// Tesseract language packs: what is installed, what can be fetched.

func (s *Server) tessdataDir() string { return s.cfg.DataDir + "/tessdata" }

func (s *Server) handleListLanguages(w http.ResponseWriter, r *http.Request) {
	st, _ := s.store.GetSettings(r.Context(), 0)
	writeJSON(w, http.StatusOK, map[string]any{"languages": ocr.Languages(s.tessdataDir()), "in_use": strings.Split(st.OCRLanguage, "+"), "dir": s.tessdataDir()})
}

func (s *Server) handleInstallLanguage(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeErr(w, http.StatusForbidden, "only an admin installs language packs")
		return
	}
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	if err := ocr.InstallLanguage(r.Context(), s.tessdataDir(), in.Code); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.log.Info("language pack installed", "code", in.Code, "by", actorName(r))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "languages": ocr.Languages(s.tessdataDir())})
}

func (s *Server) handleRemoveLanguage(w http.ResponseWriter, r *http.Request) {
	if !s.isAdmin(r) {
		writeErr(w, http.StatusForbidden, "only an admin removes language packs")
		return
	}
	if err := ocr.RemoveLanguage(s.tessdataDir(), r.PathValue("code")); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "languages": ocr.Languages(s.tessdataDir())})
}
