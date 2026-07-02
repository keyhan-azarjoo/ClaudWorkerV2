// Package backup provides deterministic tar.gz backup/restore of the engine home's DURABLE state
// (Knowledge Brain, assignments, leases). Transient state (worktrees, artifacts, temp) is excluded —
// V2 recovers durable state only. Uses stdlib only (no new dependency).
package backup

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultExcludes are transient subdirectories never worth backing up (relative to the source root).
var DefaultExcludes = []string{"worktrees", "artifacts", "repos"}

// Backup writes a gzip-compressed tar of srcDir to archivePath, skipping any path whose relative form
// starts with one of excludes (nil → DefaultExcludes). Deterministic: entries are sorted.
func Backup(srcDir, archivePath string, excludes []string) error {
	if excludes == nil {
		excludes = DefaultExcludes
	}
	info, err := os.Stat(srcDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("backup: source %q is not a directory", srcDir)
	}

	var files []string
	err = filepath.Walk(srcDir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(srcDir, p)
		if rel == "." {
			return nil
		}
		if excluded(rel, excludes) {
			if fi.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if fi.Mode().IsRegular() {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Strings(files)

	tmp := archivePath + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	for _, rel := range files {
		if err := addFile(tw, srcDir, rel); err != nil {
			tw.Close()
			gz.Close()
			out.Close()
			os.Remove(tmp)
			return err
		}
	}
	if err := tw.Close(); err != nil {
		out.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, archivePath)
}

func addFile(tw *tar.Writer, root, rel string) error {
	full := filepath.Join(root, rel)
	fi, err := os.Stat(full)
	if err != nil {
		return err
	}
	hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: 0o644, Size: fi.Size(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	f, err := os.Open(full)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

// Restore extracts a backup archive into destDir. It refuses paths that escape destDir (zip-slip
// safe). Existing files are overwritten.
func Restore(archivePath, destDir string) error {
	in, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) || filepath.IsAbs(clean) {
			return fmt.Errorf("restore: unsafe path %q", hdr.Name)
		}
		target := filepath.Join(destDir, clean)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(os.PathSeparator)) {
			return fmt.Errorf("restore: path escapes dest %q", hdr.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		f, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return err
		}
		f.Close()
	}
}

func excluded(rel string, excludes []string) bool {
	first := rel
	if i := strings.IndexRune(rel, filepath.Separator); i >= 0 {
		first = rel[:i]
	}
	for _, e := range excludes {
		if first == e {
			return true
		}
	}
	return false
}
