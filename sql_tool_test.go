package agentflow

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

const sqlToolRootTestDriverName = "agentflow_sql_tool_root_test"

var (
	sqlToolRootRegisterDriver sync.Once
	sqlToolRootDBSeq          atomic.Int64
)

func TestSQLToolRootConstructor(t *testing.T) {
	sqlToolRootRegisterDriver.Do(func() { sql.Register(sqlToolRootTestDriverName, sqlToolRootTestDriver{}) })
	db, err := sql.Open(sqlToolRootTestDriverName, fmt.Sprintf("sql-tool-root-%d", sqlToolRootDBSeq.Add(1)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	executor, err := NewSQLToolExecutor(SQLToolConfig{DB: db, AllowedQueries: map[string]string{"users.list": "SELECT id FROM users"}})
	if err != nil {
		t.Fatal(err)
	}
	if executor == nil {
		t.Fatal("expected executor")
	}
}

type sqlToolRootTestDriver struct{}

func (d sqlToolRootTestDriver) Open(string) (driver.Conn, error) {
	return sqlToolRootTestConn{}, nil
}

type sqlToolRootTestConn struct{}

func (c sqlToolRootTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported")
}
func (c sqlToolRootTestConn) Close() error { return nil }
func (c sqlToolRootTestConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}
