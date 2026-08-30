package authn

import (
	"io/fs"
	"os"
	"time"

	"github.com/pquerna/otp/totp"
)

func statFile(path string) (fs.FileInfo, error) { return os.Stat(path) }

func currentCode(secret string) (string, error) {
	return totp.GenerateCode(secret, time.Now().UTC())
}
