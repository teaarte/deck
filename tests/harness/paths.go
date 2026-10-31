package harness

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
)

// Root is the folder of the repository.
var Root = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}()

// Testdata is the path of a file of the harness's testdata.
func Testdata(name string) string {
	return filepath.Join(Root, "tests", "harness", "testdata", name)
}

// WriteProgram writes an executable file; no fork of a test beside it inherits the file open for writing, which
// would leave the program busy to start on Linux.
func WriteProgram(path string, text []byte) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, text, 0o755)
}
