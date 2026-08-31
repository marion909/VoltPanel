//go:build !unix

package agent

import "os"

func diskBlocks(os.FileInfo) (int64, uint64, bool) { return 0, 0, false }
