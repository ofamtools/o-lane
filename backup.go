package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Daily backups adapt o-questions: only root trees and undo history enter
// private archives. Retention uses the injectable file-removal function.
func (s *Store) backup(to string, daily bool) (string, error) {
	if e := os.MkdirAll(to, 0700); e != nil {
		return "", e
	}
	name := s.now().Format("2006-01-02T150405.000000000Z") + ".tar.gz"
	if daily {
		name = s.now().Format("2006-01-02") + ".tar.gz"
	}
	path := filepath.Join(to, name)
	if daily {
		if _, e := os.Stat(path); e == nil {
			return path, nil
		}
	}
	f, e := os.CreateTemp(to, ".pending-backup-*")
	if e != nil {
		return "", e
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, root := range []string{"roots", ".history"} {
		e = filepath.WalkDir(filepath.Join(s.Dir, root), func(p string, d os.DirEntry, e error) error {
			if errors.Is(e, os.ErrNotExist) {
				return nil
			}
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			info, e := d.Info()
			if e != nil {
				return e
			}
			if !info.Mode().IsRegular() || strings.HasPrefix(d.Name(), ".pending-") {
				return nil
			}
			rel, e := filepath.Rel(s.Dir, p)
			if e != nil {
				return e
			}
			if e = tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Mode: 0600, Size: info.Size(), ModTime: info.ModTime()}); e != nil {
				return e
			}
			in, e := os.Open(p)
			if e != nil {
				return e
			}
			_, e = io.Copy(tw, in)
			return errors.Join(e, in.Close())
		})
		if e != nil {
			break
		}
	}
	e = errors.Join(e, tw.Close(), gz.Close(), f.Sync(), f.Close())
	if e != nil {
		return "", e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return "", e
	}
	if e = syncDir(to); e != nil {
		return "", e
	}
	if daily {
		s.retain(to, "????-??-??.tar.gz", s.BackupKeep)
	}
	return path, nil
}

// retain keeps the newest keep files matching pattern in dir (names sort
// oldest first) and removes the rest through the injectable remove function.
// A failure warns and never fails the caller. It lists names only and never reads contents; keep 0 keeps everything.
// Each removal stands alone, so an interrupted pass resumes on the next call.
func (s *Store) retain(dir, pattern string, keep int) {
	if keep <= 0 {
		return
	}
	files, e := filepath.Glob(filepath.Join(dir, pattern))
	if e != nil {
		s.warnRetain(dir, e)
		return
	}
	for len(files) > keep {
		if e = s.remove(files[0]); e != nil && !errors.Is(e, os.ErrNotExist) {
			s.warnRetain(dir, e)
			return
		}
		files = files[1:]
	}
}

// warnRetain reports a failed prune; the change that triggered it stays saved.
func (s *Store) warnRetain(dir string, e error) {
	msg := fmt.Sprintf("warning: retention in %s failed (change kept): %v", dir, e)
	if s.Warn != nil {
		s.Warn(msg)
	} else {
		fmt.Fprintln(os.Stderr, msg)
	}
}

// nothingToUndo says plainly when retention removed the older history.
func nothingToUndo(files []string, keep int) error {
	if len(files) > 0 {
		if n, e := strconv.Atoi(strings.TrimSuffix(filepath.Base(files[0]), ".json")); e == nil && n > 1 {
			return fmt.Errorf("nothing more to undo: older changes were removed by history.keep (%d)", keep)
		}
	}
	return errors.New("nothing to undo")
}
