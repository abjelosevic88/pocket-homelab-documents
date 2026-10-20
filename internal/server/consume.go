package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/abjelosevic88/pocket-homelab-documents/internal/extract"
)

// The consume folder: drop a file in DATA_DIR/consume (a scanner, a Nextcloud
// sync, a cron job…) and it becomes a document of the first admin. A
// sub-folder named like a person's email (or its part before the @) sends
// the file to that person; other sub-folders become tags. A file is picked
// up once its size has not changed for two polls, so half-written scans wait.

type seenFile struct {
	size int64
	at   time.Time
}

func (s *Server) consumeLoop(ctx context.Context) {
	seen := map[string]seenFile{}
	t := time.NewTicker(s.cfg.ConsumePoll)
	defer t.Stop()
	for {
		s.consumeOnce(ctx, seen)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) consumeOnce(ctx context.Context, seen map[string]seenFile) {
	root := s.cfg.ConsumeDir
	if root == "" {
		return
	}
	users, err := s.store.ListUsers(ctx)
	if err != nil || len(users) == 0 {
		return
	}
	owner := users[0]
	for _, u := range users {
		if u.Role == "admin" {
			owner = u
			break
		}
	}
	present := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "~") || strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".crdownload") || strings.HasSuffix(name, ".tmp") {
			return nil
		}
		if extract.Kind(filepath.Ext(name)) == "" {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		present[p] = true
		prev, ok := seen[p]
		if !ok || prev.size != fi.Size() {
			seen[p] = seenFile{size: fi.Size(), at: time.Now()}
			return nil
		}
		if time.Since(prev.at) < s.cfg.ConsumePoll {
			return nil
		}
		if fi.Size() == 0 {
			return nil
		}
		// stable: take it
		rel, _ := filepath.Rel(root, p)
		parts := strings.Split(filepath.ToSlash(filepath.Dir(rel)), "/")
		uid := owner.ID
		var subs []string
		for i, part := range parts {
			if part == "." || part == "" {
				continue
			}
			if i == 0 {
				matched := false
				for _, u := range users {
					if strings.EqualFold(part, u.Email) || strings.EqualFold(part, strings.SplitN(u.Email, "@", 2)[0]) {
						uid, matched = u.ID, true
						break
					}
				}
				if matched {
					continue
				}
			}
			subs = append(subs, part)
		}
		tmp := filepath.Join(s.cfg.TmpDir, "consume-"+filepath.Base(p))
		if s.cfg.ConsumeDelete {
			if err := moveFile(p, tmp); err != nil {
				s.log.Warn("consume: cannot move file", "file", p, "err", err)
				return nil
			}
		} else {
			if err := copyFile(p, tmp); err != nil {
				return nil
			}
		}
		delete(seen, p)
		doc, err := s.ingest(ctx, uid, incoming{Path: tmp, Name: name, Source: "consume", Actor: "consume folder", SubDirs: subs})
		if err != nil {
			s.log.Warn("consume: file not taken", "file", rel, "err", err)
			if !s.cfg.ConsumeDelete {
				os.Remove(tmp)
			} else {
				// keep the file where someone can see it
				_ = os.MkdirAll(filepath.Join(root, ".failed"), 0o755)
				_ = moveFile(tmp, filepath.Join(root, ".failed", name))
			}
			return nil
		}
		s.log.Info("consumed", "file", rel, "document", doc.ID, "user", uid)
		if !s.cfg.ConsumeDelete {
			// remember it as taken for this run; a restart would take it again,
			// which the checksum check turns into a skipped duplicate
			seen[p] = seenFile{size: -1, at: time.Now()}
		}
		return nil
	})
	for p := range seen {
		if !present[p] {
			delete(seen, p)
		}
	}
	// leave no empty sub-folders behind after moving files out (but keep the person folders)
	if s.cfg.ConsumeDelete {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() || p == root || strings.HasPrefix(d.Name(), ".") {
				return nil
			}
			if entries, err := os.ReadDir(p); err == nil && len(entries) == 0 {
				if _, err := filepath.Rel(root, p); err == nil && strings.Count(filepath.ToSlash(strings.TrimPrefix(p, root)), "/") > 1 {
					_ = os.Remove(p)
				}
			}
			return nil
		})
	}
}
