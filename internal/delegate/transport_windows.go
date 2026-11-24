package delegate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// pipePath names the pipe after dir: the store directory, or the socket
// directory a deployment keeps apart from it (--socket-dir).
func pipePath(dir string) (string, error) {
	path, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`\\.\pipe\wacli-send-%x`, sha256.Sum256([]byte(strings.ToLower(path)))), nil
}

func Dial(ctx context.Context, dir string) (net.Conn, error) {
	path, err := pipePath(dir)
	if err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, path)
}

func Listen(dir string) (net.Listener, error) {
	path, err := pipePath(dir)
	if err != nil {
		return nil, err
	}
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("get delegate pipe owner: %w", err)
	}
	// Do not touch legacy AF_UNIX files: Windows may refuse even Lstat on them.
	// The protected DACL grants access only to this process's Windows user.
	return winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")",
	})
}
