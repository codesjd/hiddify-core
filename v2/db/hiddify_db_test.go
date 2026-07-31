package db

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// chdirToFreshDataDir points the process at a scratch directory for a test's
// DB access. It deliberately does NOT use t.TempDir(): getOrOpenDB caches the
// LevelDB handle for the rest of the process (by design — see hiddify_db.go),
// so the underlying .db file is still open when the test ends, and on
// Windows t.TempDir()'s automatic RemoveAll cleanup fails to delete an
// open file. Using a plain, uncleaned os.MkdirTemp dir sidesteps that.
func chdirToFreshDataDir(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("", "hiddify-db-test-*")
	require.NoError(t, err)
	t.Chdir(dir)
}

type cacheTestEntity struct {
	Id    string
	Value string
}

// TestTable_SharedHandle_ReadWriteRoundTrip exercises the cached-handle path
// added by getOrOpenDB: a write followed by a read on the same table name
// must see the write, proving the cache doesn't lose or stale writes across
// calls that now share one open handle instead of open/close per call.
func TestTable_SharedHandle_ReadWriteRoundTrip(t *testing.T) {
	chdirToFreshDataDir(t)

	tbl := GetTable[cacheTestEntity]()

	require.NoError(t, tbl.UpdateInsert(&cacheTestEntity{Id: "a", Value: "hello"}))

	got, err := tbl.Get("a")
	require.NoError(t, err)
	require.Equal(t, "hello", got.Value)
}

// TestTable_SharedHandle_DeleteThenGet proves deletes and reads stay
// consistent with each other through the shared cached handle.
func TestTable_SharedHandle_DeleteThenGet(t *testing.T) {
	chdirToFreshDataDir(t)

	tbl := GetTable[cacheTestEntity]()

	require.NoError(t, tbl.UpdateInsert(&cacheTestEntity{Id: "b", Value: "world"}))

	_, err := tbl.Get("b")
	require.NoError(t, err)

	require.NoError(t, tbl.Delete("b"))

	_, err = tbl.Get("b")
	require.Error(t, err)
}
