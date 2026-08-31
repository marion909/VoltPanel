//go:build !linux

package agent

// pwdLockHolder gibt es nur unter Linux. Der Agent läuft ohnehin nur dort;
// diese Fassung hält lediglich die Tests auf anderen Systemen am Laufen.
func pwdLockHolder() string { return "" }

// PwdLockHolder ist die Fassung für Aufrufer außerhalb des Pakets — `volt
// doctor` prüft dieselbe Sperre.
func PwdLockHolder() string { return pwdLockHolder() }
