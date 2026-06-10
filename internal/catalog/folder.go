package catalog

import (
	"fmt"
	"path/filepath"
	"strings"
)

// buildRootLabels assigns each Shared-folder root a stable, human label: the
// root's basename, disambiguated with a numeric suffix (by config order) when
// two roots share a basename — e.g. ["/a/media","/b/media"] -> "media","media (2)".
func buildRootLabels(roots []string) map[string]string {
	labels := make(map[string]string, len(roots))
	seen := make(map[string]int)
	for _, root := range roots {
		base := filepath.Base(filepath.Clean(root))
		seen[base]++
		if seen[base] == 1 {
			labels[root] = base
		} else {
			labels[root] = fmt.Sprintf("%s (%d)", base, seen[base])
		}
	}
	return labels
}

// folderFor reports which Shared folder a Title's path belongs to (its label) and
// the Title's directory relative to that root, forward-slashed ("" at root level).
// A path under no root yields ("", "") — impossible by construction, since the
// scanner only indexes files found under a root. First matching root wins.
func folderFor(path string, roots []string, labels map[string]string) (rootLabel, relDir string) {
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		dir := filepath.Dir(rel)
		if dir == "." {
			dir = ""
		}
		return labels[root], filepath.ToSlash(dir)
	}
	return "", ""
}
