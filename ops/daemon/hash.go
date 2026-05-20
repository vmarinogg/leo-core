package daemon

import (
	"crypto/sha256"
	"fmt"

	"github.com/momhq/mom/shared/pathutil"
)

// ProjectHash returns a short deterministic hash of the project path.
// Used to make service names unique per project.
func ProjectHash(projectDir string) string {
	h := sha256.Sum256([]byte(pathutil.CanonicalDir(projectDir)))
	return fmt.Sprintf("%x", h[:6]) // 12 hex chars
}
