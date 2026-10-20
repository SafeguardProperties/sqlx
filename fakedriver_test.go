package sqlx

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"testing"
)

// fakeDriver is a minimal in-memory driver.Driver for tests that need no
// external database. Each query string maps to a fixed result set registered
// with setFakeResult; query args are ignored.
type fakeDriver struct{}

type fakeResult struct {
	columns []string
	rows    [][]driver.Value
}

var (
	fakeMu      sync.RWMutex
	fakeResults = map[string]*fakeResult{}
)

func init() {
	sql.Register("sqlxfake", fakeDriver{})
}

func setFakeResult(query string, columns []string, rows ...[]driver.Value) {
	fakeMu.Lock()
	defer fakeMu.Unlock()
	fakeResults[query] = &fakeResult{columns: columns, rows: rows}
}

func openFake(t testing.TB) *DB {
	t.Helper()
	db, err := Open("sqlxfake", "")
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func (fakeDriver) Open(string) (driver.Conn, error) { return fakeConn{}, nil }

type fakeConn struct{}

func (fakeConn) Prepare(query string) (driver.Stmt, error) { return fakeStmt{query: query}, nil }
func (fakeConn) Close() error                              { return nil }
func (fakeConn) Begin() (driver.Tx, error)                 { return fakeTx{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeStmt struct{ query string }

func (fakeStmt) Close() error  { return nil }
func (fakeStmt) NumInput() int { return -1 }
func (fakeStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}
func (s fakeStmt) Query([]driver.Value) (driver.Rows, error) {
	fakeMu.RLock()
	res, ok := fakeResults[s.query]
	fakeMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("fake driver: no result for query %q", s.query)
	}
	return &fakeRows{res: res}, nil
}

type fakeRows struct {
	res *fakeResult
	pos int
}

func (r *fakeRows) Columns() []string { return r.res.columns }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.res.rows) {
		return io.EOF
	}
	copy(dest, r.res.rows[r.pos])
	r.pos++
	return nil
}
