// Package claim reserves a task's target paths in the download root so that a
// download never writes over an unrelated file of the same name.
package claim

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gofrs/flock"
	"github.com/qiuxsgit/p2pget/internal/fsutil"
)

// StateDirName is the hidden directory in the download root that holds task state.
const StateDirName = ".p2pget"

const markerName = "claimed"

// ConflictError lists target paths that already exist and do not belong to the task.
type ConflictError struct {
	Paths []string
}

func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString("以下路径已存在，为避免覆盖，未开始下载：")
	for _, p := range e.Paths {
		b.WriteString("\n  ")
		b.WriteString(p)
	}
	b.WriteString("\n请移走或改名后重试。")
	return b.String()
}

// Claim reserves targets (paths under root) for the task whose state lives in stateDir.
// A task that has already claimed its targets resumes without checks. Otherwise any
// existing target, or an existing non-directory where a parent directory is needed,
// is a conflict and nothing is claimed.
func Claim(root, stateDir string, targets []string) error {
	marker := filepath.Join(stateDir, markerName)
	if _, err := os.Stat(marker); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lockDir := filepath.Join(root, StateDirName)
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return err
	}
	// Serialises check-and-claim across processes downloading different tasks.
	lock := flock.New(filepath.Join(lockDir, "claim.lock"))
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Close()

	var conflicts []string
	for _, target := range targets {
		rel, err := filepath.Rel(root, target)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("目标路径不在下载目录内：%s", target)
		}
		if first, _, _ := strings.Cut(rel, string(os.PathSeparator)); strings.EqualFold(first, StateDirName) {
			return fmt.Errorf("目标路径与程序状态目录冲突：%s", target)
		}
		if p, err := conflictOn(root, rel); err != nil {
			return err
		} else if p != "" && !slices.Contains(conflicts, p) {
			conflicts = append(conflicts, p)
		}
	}
	if len(conflicts) > 0 {
		return &ConflictError{Paths: conflicts}
	}
	return fsutil.AtomicWrite(marker, []byte(strings.Join(targets, "\n")+"\n"))
}

// conflictOn returns the first existing path that blocks writing root/rel, or "".
func conflictOn(root, rel string) (string, error) {
	parts := strings.Split(rel, string(os.PathSeparator))
	path := root
	for i, part := range parts {
		path = filepath.Join(path, part)
		stat, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		if err != nil {
			return "", err
		}
		if i == len(parts)-1 || !stat.IsDir() {
			return path, nil
		}
	}
	return "", nil
}
