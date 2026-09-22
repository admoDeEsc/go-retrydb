package retrydb

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"github.com/lib/pq"
)

func TestDriverRegistered(t *testing.T) {
	// El init() debe haber registrado "pq-retry". sql.Open no conecta aún, solo
	// valida que el driver exista.
	if _, err := sql.Open("pq-retry", "postgres://user:pass@localhost:5432/db?sslmode=disable"); err != nil {
		t.Fatalf("sql.Open(pq-retry) falló: %v", err)
	}
	found := false
	for _, d := range sql.Drivers() {
		if d == "pq-retry" {
			found = true
		}
	}
	if !found {
		t.Fatal("driver pq-retry no está registrado")
	}
}

func TestIsRetryable_PgBouncerCodes(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"26000 prepared stmt does not exist", &pq.Error{Code: "26000"}, true},
		{"08P01 protocol violation", &pq.Error{Code: "08P01"}, true},
		{"otro código pq (23505 unique)", &pq.Error{Code: "23505"}, false},
		{"22P02 invalid input (dato, NO reintentar)", &pq.Error{Code: "22P02"}, false},
		{"mensaje prepared statement does not exist", errors.New(`pq: prepared statement "s1" does not exist`), true},
		{"mensaje prepared statement requires", errors.New(`bind message supplies ... prepared statement requires`), true},
		{"error genérico", errors.New("connection refused"), false},
	}
	for _, c := range cases {
		if got := isRetryable(c.err); got != c.want {
			t.Errorf("%s: isRetryable = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestRetryStmt_ConvertsToBadConn verifica que el Stmt envuelto convierte el
// error transitorio de PgBouncer en driver.ErrBadConn (para que database/sql
// reintente en una conexión fresca) y que NO toca errores de datos ni el éxito.
func TestRetryStmt_ConvertsToBadConn(t *testing.T) {
	rs := &retryStmt{Stmt: &fakeStmt{}}

	// Error transitorio de PgBouncer → ErrBadConn.
	fs := &fakeStmt{execErr: &pq.Error{Code: "26000"}}
	rs.Stmt = fs
	if _, err := rs.Exec(nil); err != driver.ErrBadConn {
		t.Errorf("Exec con 26000: err = %v, want driver.ErrBadConn", err)
	}
	fs.queryErr = &pq.Error{Code: "08P01"}
	if _, err := rs.Query(nil); err != driver.ErrBadConn {
		t.Errorf("Query con 08P01: err = %v, want driver.ErrBadConn", err)
	}

	// Error de DATOS (23505) → se propaga tal cual, NO ErrBadConn.
	dataErr := &pq.Error{Code: "23505"}
	rs.Stmt = &fakeStmt{execErr: dataErr}
	if _, err := rs.Exec(nil); err != dataErr {
		t.Errorf("Exec con 23505: err = %v, want el error original", err)
	}

	// Éxito → sin error.
	rs.Stmt = &fakeStmt{}
	if _, err := rs.Exec(nil); err != nil {
		t.Errorf("Exec OK: err = %v, want nil", err)
	}
}

// fakeStmt implementa driver.Stmt para las pruebas (sin contexto, camino legacy).
type fakeStmt struct {
	execErr  error
	queryErr error
}

func (f *fakeStmt) Close() error  { return nil }
func (f *fakeStmt) NumInput() int { return -1 }
func (f *fakeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return nil, f.execErr
}
func (f *fakeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return nil, f.queryErr
}
