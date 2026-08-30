//go:build !unix

package agent

import "os"

func ownerNames(os.FileInfo) (string, string) { return "", "" }
