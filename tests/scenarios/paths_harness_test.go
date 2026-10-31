package scenarios

import (
	"path/filepath"
	"runtime"
)

// Root is the folder of the repository.
var Root = func() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}()

// Testdata is the path of a file of the harness's testdata.
func Testdata(name string) string {
	return filepath.Join(Root, "tests", "scenarios", "testdata", name)
}
