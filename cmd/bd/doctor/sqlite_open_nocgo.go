//go:build !cgo

package doctor

// sqliteConnString returns an empty connection string when CGO is not available.
// The SQLite driver requires CGO; callers will get an error from sql.Open.
func sqliteConnString(path string, readOnly bool) string {
	return ""
}
