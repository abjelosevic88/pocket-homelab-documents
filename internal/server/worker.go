package server

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/match"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/ocr"
	"github.com/abjelosevic88/pocket-homelab-documents/internal/store"
)

// Background work: OCR workers pull "process" / "reprocess" jobs, one more
// goroutine takes the long-running imports so they never block a scan.

func (s *Server) startWorkers(ctx context.Context) {
	s.workersOn = true
	for i := 0; i < s.cfg.OCRWorkers; i++ {
		go s.workLoop(ctx, []string{"process", "reprocess"}, i)
	}
	go s.workLoop(ctx, []string{"import_paperless", "reindex", "mirror_all", "folder_sync", "run_rule"}, 99)
}

// nudge wakes the workers up at once instead of on the next tick.
func (s *Server) nudge() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Server) workLoop(ctx context.Context, kinds []string, n int) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		for {
			j, err := s.store.ClaimJob(ctx, kinds)
			if err != nil || j == nil {
				break
			}
			s.runJob(ctx, j)
			if ctx.Err() != nil {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.wake:
		}
	}
}

func (s *Server) runJob(ctx context.Context, j *store.Job) {
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("job panicked", "job", j.ID, "kind", j.Kind, "panic", r)
			s.store.FinishJob(ctx, j.ID, fmt.Sprintf("crashed: %v", r))
		}
	}()
	var err error
	switch j.Kind {
	case "process", "reprocess":
		err = s.processJob(ctx, j)
	case "import_paperless":
		err = s.paperlessImportJob(ctx, j)
	case "reindex":
		_, err = s.store.ReindexAll(ctx, j.UserID)
	case "mirror_all", "folder_sync":
		err = s.folderSyncJob(ctx, j)
	case "run_rule":
		err = s.runRuleJob(ctx, j)
	default:
		err = fmt.Errorf("unknown job kind %q", j.Kind)
	}
	msg := ""
	if err != nil {
		msg = err.Error()
		s.log.Warn("job failed", "job", j.ID, "kind", j.Kind, "err", err)
	}
	s.store.FinishJob(ctx, j.ID, msg)
}

// enqueueProcess queues OCR for a document and wakes a worker.
func (s *Server) enqueueProcess(ctx context.Context, d *store.Document, kind string, params map[string]any) (*store.Job, error) {
	id := d.ID
	j, err := s.store.EnqueueJob(ctx, d.UserID, &id, kind, params)
	if err != nil {
		return nil, err
	}
	_ = s.store.SetStatus(ctx, d.ID, "pending", "")
	s.nudge()
	return j, nil
}

type processParams struct {
	New          bool   `json:"new"`
	DetectDate   bool   `json:"detect_date"`
	SkipMatching bool   `json:"skip_matching"`
	Mode         string `json:"mode"`
	ForceText    bool   `json:"force_text"`
	Actor        string `json:"actor"`
}

func (s *Server) processJob(ctx context.Context, j *store.Job) error {
	if j.DocumentID == nil {
		return fmt.Errorf("no document")
	}
	var p processParams
	_ = json.Unmarshal(j.Params, &p)
	d, err := s.store.GetDocumentAny(ctx, *j.DocumentID, true)
	if err != nil {
		return err
	}
	s.processing.Store(d.ID, j.ID)
	defer s.processing.Delete(d.ID)
	logf := func(f string, a ...any) { s.store.JobProgress(ctx, j.ID, "", fmt.Sprintf(f, a...)) }
	_ = s.store.SetStatus(ctx, d.ID, "processing", "")
	s.store.JobProgress(ctx, j.ID, "reading "+d.OriginalName, "")
	src := s.originalPath(d)
	if _, err := os.Stat(src); err != nil {
		_ = s.store.SetStatus(ctx, d.ID, "failed", "the original file is missing on disk")
		return fmt.Errorf("original missing: %s", src)
	}
	box, _ := s.store.GetSettings(ctx, 0)
	tools := ocr.Detect(ctx, s.cfg.GotenbergURL)
	opts := ocr.Options{
		Language: box.OCRLanguage, Mode: firstNonEmpty(p.Mode, box.OCRMode), Deskew: box.OCRDeskew, Rotate: box.OCRRotate, Clean: box.OCRClean,
		MaxPages: box.OCRMaxPages, OutputType: box.OCROutputType, ImageDPI: s.cfg.OCRDPI, Threads: s.cfg.OCRThreads, Timeout: s.cfg.OCRTimeout,
		Gotenberg: s.cfg.GotenbergURL, TmpDir: s.cfg.TmpDir, Log: func(l string) { logf("%s", l) },
	}
	if missing := tools.HasLanguage(opts.Language); len(missing) > 0 {
		logf("tesseract language(s) not installed: %s; using what is", strings.Join(missing, ", "))
		var keep []string
		for _, l := range strings.Split(opts.Language, "+") {
			ok := true
			for _, m := range missing {
				if m == l {
					ok = false
				}
			}
			if ok {
				keep = append(keep, l)
			}
		}
		if len(keep) == 0 {
			keep = []string{"eng"}
		}
		opts.Language = strings.Join(keep, "+")
	}
	// previous page renders are stale
	_ = os.RemoveAll(filepath.Join(s.cfg.PagesDir, strconv.FormatInt(d.ID, 10)))
	s.store.JobProgress(ctx, j.ID, "OCR running", "")
	res, err := ocr.Process(ctx, tools, src, s.archivePath(d.ID), s.thumbPath(d.ID), opts)
	if err != nil {
		_ = s.store.SetStatus(ctx, d.ID, "failed", err.Error())
		s.store.AddHistory(ctx, d.ID, d.UserID, "worker", "failed", map[string]any{"error": err.Error()})
		s.publish(d.UserID, "documents.failed", "document:"+strconv.FormatInt(d.ID, 10), map[string]any{"id": d.ID, "title": d.Title, "error": err.Error()})
		s.notifyFailure(ctx, d, err.Error())
		return err
	}
	for _, wn := range res.Warnings {
		logf("warning: %s", wn)
	}
	logf("done in %s: %d pages, %d characters, archive=%v thumb=%v", res.Took.Round(time.Millisecond), res.PageCount, len(res.Text), res.ArchivePath != "", res.ThumbPath != "")
	var archSum string
	var archSize int64
	if res.ArchivePath != "" {
		archSum, archSize, _ = ocr.Checksum(res.ArchivePath)
	}
	text := res.Text
	if text == "" && d.Content != "" {
		text = d.Content // keep what an import brought when OCR has nothing
	}
	if err := s.store.SetProcessed(ctx, d.ID, text, res.PageCount, res.ArchivePath != "", archSum, archSize, res.ThumbPath != "", "", p.ForceText); err != nil {
		return err
	}
	d, err = s.store.GetDocumentAny(ctx, d.ID, true)
	if err != nil {
		return err
	}
	// what the paper says
	if p.DetectDate {
		if created := ocr.GuessCreated(d.Content, true); created != "" && created != d.Created {
			_ = s.store.SetDetected(ctx, d.ID, "", created)
			logf("created date read from the text: %s", created)
			d.Created = created
		}
	}
	if p.New && !p.SkipMatching {
		s.applyMatching(ctx, d, logf)
		d, _ = s.store.GetDocumentAny(ctx, d.ID, true)
		s.runRules(ctx, d, "added", firstNonEmpty(p.Actor, "rules"))
		d, _ = s.store.GetDocumentAny(ctx, d.ID, true)
	}
	_ = s.store.IndexDocument(ctx, d.ID)
	if err := s.placeInFolder(ctx, d); err != nil {
		logf("documents folder: %v", err)
	}
	s.classifiers.Invalidate(d.UserID)
	action := "processed"
	if j.Kind == "reprocess" {
		action = "reprocessed"
	}
	s.store.AddHistory(ctx, d.ID, d.UserID, "worker", action, map[string]any{"pages": res.PageCount, "characters": len(d.Content), "warnings": res.Warnings, "took": res.Took.Round(time.Millisecond).String()})
	s.publish(d.UserID, "documents.processed", "document:"+strconv.FormatInt(d.ID, 10), map[string]any{"id": d.ID, "title": d.Title, "pages": res.PageCount, "characters": len(d.Content), "created": d.Created, "new": p.New})
	return nil
}

// applyMatching assigns tags, correspondent, type and path by the matching
// rules and the classifier, for a document that just came in.
func (s *Server) applyMatching(ctx context.Context, d *store.Document, logf func(string, ...any)) {
	sg := s.suggestFor(ctx, d.UserID, d)
	c := store.DocChanges{AddTags: sg.Tags}
	names := s.store.Names(ctx, d.UserID)
	var applied []string
	for _, t := range sg.Tags {
		applied = append(applied, "tag "+names.Tags[t])
	}
	if d.CorrespondentID == nil && len(sg.Correspondents) > 0 {
		v := sg.Correspondents[0]
		p := &v
		c.CorrespondentID = &p
		applied = append(applied, "correspondent "+names.Correspondents[v])
	}
	if d.DocumentTypeID == nil && len(sg.DocumentTypes) > 0 {
		v := sg.DocumentTypes[0]
		p := &v
		c.DocumentTypeID = &p
		applied = append(applied, "type "+names.Types[v])
	}
	if d.StoragePathID == nil && len(sg.StoragePaths) > 0 {
		v := sg.StoragePaths[0]
		p := &v
		c.StoragePathID = &p
		applied = append(applied, "path "+names.Paths[v])
	}
	if len(applied) == 0 {
		return
	}
	if changed, err := s.store.UpdateDocument(ctx, d.UserID, d.ID, c); err == nil && len(changed) > 0 {
		s.store.AddHistory(ctx, d.ID, d.UserID, "matching", "matched", changed)
		logf("matched: %s", strings.Join(applied, ", "))
	}
}

// ---- rules ----

func globMatch(pattern, name string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return true
	}
	ok, err := filepath.Match(strings.ToLower(pattern), strings.ToLower(name))
	if err != nil {
		return strings.Contains(strings.ToLower(name), strings.ToLower(strings.Trim(pattern, "*")))
	}
	return ok
}

func ruleFits(r store.Rule, d *store.Document, subdir string) bool {
	f := r.Filters
	if !globMatch(f.Filename, d.OriginalName) {
		return false
	}
	if f.Path != "" && !globMatch(f.Path, subdir) {
		return false
	}
	if f.Source != "" && f.Source != d.Source {
		return false
	}
	if f.Mime != "" && !strings.HasPrefix(d.Mime, f.Mime) {
		return false
	}
	if f.Content != "" && !strings.Contains(strings.ToLower(d.Title+"\n"+d.Content), strings.ToLower(f.Content)) {
		return false
	}
	if f.Correspondent != nil && (d.CorrespondentID == nil || *d.CorrespondentID != *f.Correspondent) {
		return false
	}
	if f.DocumentType != nil && (d.DocumentTypeID == nil || *d.DocumentTypeID != *f.DocumentType) {
		return false
	}
	for _, t := range f.Tags {
		has := false
		for _, dt := range d.Tags {
			if dt == t {
				has = true
			}
		}
		if !has {
			return false
		}
	}
	return true
}

// ruleChanges turns a rule's actions into document changes.
func (s *Server) ruleChanges(ctx context.Context, r store.Rule, d *store.Document) store.DocChanges {
	a := r.Actions
	c := store.DocChanges{AddTags: a.AddTags, RemoveTags: a.RemoveTags, Favorite: a.Favorite}
	if a.Correspondent != nil {
		p := a.Correspondent
		c.CorrespondentID = &p
	}
	if a.DocumentType != nil {
		p := a.DocumentType
		c.DocumentTypeID = &p
	}
	if a.StoragePath != nil {
		p := a.StoragePath
		c.StoragePathID = &p
	}
	if strings.TrimSpace(a.Title) != "" {
		names := s.store.Names(ctx, d.UserID)
		t := strings.TrimSpace(strings.ReplaceAll(renderPath(a.Title, d, names), string(filepath.Separator), " "))
		if t != "" {
			c.Title = &t
		}
	}
	for k, v := range a.Fields {
		if id, err := strconv.ParseInt(k, 10, 64); err == nil {
			c.SetFields = append(c.SetFields, store.FieldValue{FieldID: id, Value: v})
		}
	}
	return c
}

// runRules applies every enabled rule with the trigger to a document.
func (s *Server) runRules(ctx context.Context, d *store.Document, trigger, actor string) {
	rules, err := s.store.ListRules(ctx, d.UserID)
	if err != nil {
		return
	}
	for _, r := range rules {
		if !r.Enabled || r.Trigger != trigger || !ruleFits(r, d, "") {
			continue
		}
		c := s.ruleChanges(ctx, r, d)
		if changed, err := s.store.UpdateDocument(ctx, d.UserID, d.ID, c); err == nil && len(changed) > 0 {
			s.store.AddHistory(ctx, d.ID, d.UserID, "rule: "+r.Name, "rule", changed)
			if nd, err := s.store.GetDocumentAny(ctx, d.ID, true); err == nil {
				*d = *nd
			}
		}
	}
}

// runRuleJob applies one rule to every existing document (the "Run now" button).
func (s *Server) runRuleJob(ctx context.Context, j *store.Job) error {
	var p struct {
		RuleID int64 `json:"rule_id"`
	}
	_ = json.Unmarshal(j.Params, &p)
	r, err := s.store.GetRule(ctx, j.UserID, p.RuleID)
	if err != nil {
		return err
	}
	ids := s.store.DocumentIDs(ctx, j.UserID, false)
	n := 0
	for i, id := range ids {
		d, err := s.store.GetDocument(ctx, j.UserID, id, true)
		if err != nil || !ruleFits(*r, d, "") {
			continue
		}
		if changed, err := s.store.UpdateDocument(ctx, j.UserID, id, s.ruleChanges(ctx, *r, d)); err == nil && len(changed) > 0 {
			s.store.AddHistory(ctx, id, j.UserID, "rule: "+r.Name, "rule", changed)
			if nd, err := s.store.GetDocument(ctx, j.UserID, id, false); err == nil {
				_ = s.placeInFolder(ctx, nd)
			}
			n++
		}
		if i%20 == 0 {
			s.store.JobProgress(ctx, j.ID, fmt.Sprintf("%d of %d checked, %d changed", i+1, len(ids), n), "")
		}
	}
	s.store.JobProgress(ctx, j.ID, "", fmt.Sprintf("rule “%s”: %d of %d documents changed", r.Name, n, len(ids)))
	s.classifiers.Invalidate(j.UserID)
	return nil
}

// ---- notifications ----

// notifyFailure tells the owner's Apprise / ntfy URL that OCR failed.
func (s *Server) notifyFailure(ctx context.Context, d *store.Document, msg string) {
	st, err := s.store.GetSettings(ctx, d.UserID)
	if err != nil || strings.TrimSpace(st.NotifyURL) == "" {
		return
	}
	link := ""
	if s.cfg.BaseURL != "" {
		link = "\n" + s.cfg.BaseURL + "/documents/" + strconv.FormatInt(d.ID, 10)
	}
	go sendNotify(st.NotifyURL, "Pocket Documents: "+d.Title+" failed", msg+link)
}

// classifierFor returns the person's trained classifier (for the settings page).
func (s *Server) classifierFor(ctx context.Context, uid int64) (*match.Classifier, error) {
	return s.classifiers.Get(uid, func() ([]store.TrainingDoc, error) { return s.store.TrainingDocs(ctx, uid) })
}
