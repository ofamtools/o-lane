package main

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
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
	if daily && s.BackupKeep > 0 {
		files, e := filepath.Glob(filepath.Join(to, "????-??-??.tar.gz"))
		if e != nil {
			return "", e
		}
		for len(files) > s.BackupKeep {
			if e = s.remove(files[0]); e != nil {
				return "", e
			}
			files = files[1:]
		}
	}
	return path, nil
}
