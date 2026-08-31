//go:build !unix

package agent

import "os"

func (t *terminal) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.closed = true

	if t.ln != nil {
		t.ln.Close()
	}
	_ = os.Remove(t.socket)
	if t.ptmx != nil {
		t.ptmx.Close()
	}
}
