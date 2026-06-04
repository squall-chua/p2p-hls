package app

import (
	"context"
	"fmt"
	"os"

	"github.com/squall-chua/p2p-hls/internal/identity"
	"github.com/squall-chua/p2p-hls/internal/library"
)

// Download streams the original file from host to destPath, then verifies its
// BLAKE3 hash equals the Content ID (rejecting and deleting on mismatch).
func (n *Node) Download(ctx context.Context, host identity.NodeID, contentID, destPath string) error {
	sess, err := n.session(ctx, host)
	if err != nil {
		return err
	}
	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if derr := sess.DownloadTo(ctx, contentID, f); derr != nil {
		f.Close()
		os.Remove(tmp)
		return derr
	}
	if cerr := f.Close(); cerr != nil {
		os.Remove(tmp)
		return cerr
	}

	got, err := library.HashFile(tmp)
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if got != contentID {
		os.Remove(tmp)
		return fmt.Errorf("app: download integrity check failed (got %s, want %s)", got, contentID)
	}
	if rerr := os.Rename(tmp, destPath); rerr != nil {
		os.Remove(tmp)
		return rerr
	}
	return nil
}
