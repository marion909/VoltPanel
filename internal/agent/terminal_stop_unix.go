//go:build unix

package agent

import (
	"os"
	"syscall"
	"time"
)

// close beendet Shell und Terminal — erst freundlich, dann bestimmt.
//
// Das Signal geht an die ganze Prozessgruppe, nicht nur an die Shell: was sie
// gestartet hat, liefe sonst als Waise weiter und hinge an einem Terminal, das
// es nicht mehr gibt.
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
	if t.cmd == nil || t.cmd.Process == nil {
		return
	}

	group := -t.cmd.Process.Pid
	_ = syscall.Kill(group, syscall.SIGHUP)

	done := make(chan struct{})
	go func() {
		_, _ = t.cmd.Process.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = syscall.Kill(group, syscall.SIGKILL)
		<-done
	}
}
