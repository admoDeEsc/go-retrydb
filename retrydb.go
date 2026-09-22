// Package retrydb registra un driver database/sql llamado "pq-retry" que
// envuelve al driver de lib/pq y REINTENTA de forma transparente las
// operaciones que fallan con el error transitorio de prepared statement
// (SQLSTATE 26000 "prepared statement does not exist" / 08P01) que ocurre con
// lib/pq detrás de PgBouncer en modo transaction pooling.
//
// El error solo afecta al statement (protocolo), NO a los datos: PgBouncer
// reasignó la conexión de servidor entre Parse y Bind, así que el statement
// preparado ya no existe en esa conexión. El fallo ocurre en la fase Parse/Bind
// (ANTES de ejecutar), por lo que reintentar es seguro e idempotente (no hay
// riesgo de doble efecto): son sentencias sueltas, no dentro de una transacción
// del cliente.
//
// COBERTURA (endurecida):
//   - Query/Exec de sentencia suelta (Conn.QueryContext/ExecContext): reintento
//     in-place con reconexión; si persiste, se degrada a driver.ErrBadConn.
//   - Prepared statements de database/sql (el pool cachea driver.Stmt): el
//     camino Stmt.Query/Exec NO pasa por Conn.Query/Exec, así que se envuelve el
//     Stmt para convertir el error transitorio en driver.ErrBadConn.
//   - driver.ErrBadConn hace que database/sql DESCARTE la conexión y REINTENTE
//     en una conexión FRESCA del pool (hasta 2 veces), cubriendo TODOS los
//     caminos sin tocar ninguna query ni repositorio. Es SEGURO aquí porque el
//     error es pre-ejecución (Parse/Bind), nunca a mitad de un fetch de filas.
//
// Uso: en lugar de sql.Open("postgres", dsn) → sql.Open("pq-retry", dsn).
// No requiere cambiar ninguna query ni la lógica de repositorio.
//
// ============================================================================
// FUENTE ÚNICA DE VERDAD: este archivo vive en el módulo canónico
// github.com/admoDeEsc/go-retrydb y se propaga a los servicios Go mediante el
// require de go.mod (tag semver). NO editar copias por-servicio a mano.
// ============================================================================
package retrydb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"strings"

	"github.com/lib/pq"
)

func init() {
	sql.Register("pq-retry", &retryDriver{base: pq.Driver{}})
}

// isRetryable detecta el error transitorio de prepared statement de PgBouncer.
// Solo cubre errores de PROTOCOLO en la fase Parse/Bind (pre-ejecución), nunca
// errores de datos (p.ej. 22P02 invalid input syntax, 23505 unique_violation),
// para no reintentar operaciones que sí tocaron datos.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if pqErr, ok := err.(*pq.Error); ok {
		// 26000: prepared statement does not exist
		// 08P01: protocol violation (bind message ... prepared statement ...)
		if pqErr.Code == "26000" || pqErr.Code == "08P01" {
			return true
		}
		// Cualquier otro código pq es un error real: NO reintentar.
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "prepared statement") &&
		(strings.Contains(msg, "does not exist") || strings.Contains(msg, "requires"))
}

type retryDriver struct{ base pq.Driver }

func (d *retryDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &retryConn{Conn: c, dsn: name}, nil
}

// retryConn envuelve la conexión de lib/pq. Implementa las interfaces de
// contexto (Queryer/Execer) para interceptar y reintentar, y envuelve los
// Stmt preparados para cubrir el camino del pool de database/sql.
type retryConn struct {
	driver.Conn
	dsn string
}

func (c *retryConn) reopen() error {
	// Cierra la conexión rota y abre una nueva del mismo driver.
	_ = c.Conn.Close()
	nc, err := (pq.Driver{}).Open(c.dsn)
	if err != nil {
		return err
	}
	c.Conn = nc
	return nil
}

func (c *retryConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	q, ok := c.Conn.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	rows, err := q.QueryContext(ctx, query, args)
	if isRetryable(err) {
		// Fast path: reconectar in-place y reintentar UNA vez sobre esta conexión.
		if rerr := c.reopen(); rerr == nil {
			if q2, ok := c.Conn.(driver.QueryerContext); ok {
				rows2, err2 := q2.QueryContext(ctx, query, args)
				if !isRetryable(err2) {
					return rows2, err2
				}
			}
		}
		// Red de seguridad: si sigue fallando por el mismo error de protocolo,
		// degradar a ErrBadConn para que database/sql descarte la conexión y
		// reintente en una FRESCA del pool. Seguro: el error es pre-ejecución.
		return nil, driver.ErrBadConn
	}
	return rows, err
}

func (c *retryConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	e, ok := c.Conn.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	res, err := e.ExecContext(ctx, query, args)
	if isRetryable(err) {
		if rerr := c.reopen(); rerr == nil {
			if e2, ok := c.Conn.(driver.ExecerContext); ok {
				res2, err2 := e2.ExecContext(ctx, query, args)
				if !isRetryable(err2) {
					return res2, err2
				}
			}
		}
		return nil, driver.ErrBadConn
	}
	return res, err
}

// Prepare/PrepareContext: envuelven el Stmt subyacente para cubrir el camino del
// pool de database/sql (que cachea driver.Stmt y ejecuta por Stmt.Query/Exec,
// SIN pasar por Conn.Query/Exec). Ese camino era la GRIETA: el error transitorio
// de PgBouncer surgía en Stmt.Exec/Query y no se reintentaba. Al devolver
// ErrBadConn desde el Stmt, database/sql descarta la conexión y re-prepara en
// una conexión fresca.
func (c *retryConn) Prepare(query string) (driver.Stmt, error) {
	s, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &retryStmt{Stmt: s}, nil
}

func (c *retryConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if p, ok := c.Conn.(driver.ConnPrepareContext); ok {
		s, err := p.PrepareContext(ctx, query)
		if err != nil {
			return nil, err
		}
		return &retryStmt{Stmt: s}, nil
	}
	s, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &retryStmt{Stmt: s}, nil
}

// Aseguramos passthrough de las capacidades opcionales del Conn subyacente.
func (c *retryConn) Begin() (driver.Tx, error) { return c.Conn.Begin() }

func (c *retryConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.Conn.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	return c.Conn.Begin()
}

func (c *retryConn) Ping(ctx context.Context) error {
	if p, ok := c.Conn.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *retryConn) Close() error { return c.Conn.Close() }

// retryStmt envuelve un driver.Stmt. Convierte el error transitorio de PgBouncer
// (prepared statement inexistente) en driver.ErrBadConn para que database/sql
// descarte la conexión y re-prepare el statement en una conexión fresca del pool.
// Seguro: el error ocurre en Parse/Bind (pre-ejecución), sin riesgo de doble
// efecto. Errores de datos (22P02, 23505, etc.) se propagan sin tocar.
type retryStmt struct {
	driver.Stmt
}

func (s *retryStmt) toBadConn(err error) error {
	if isRetryable(err) {
		return driver.ErrBadConn
	}
	return err
}

// ExecContext / QueryContext con contexto (los usa database/sql cuando el Stmt
// subyacente los soporta). Si no, database/sql llama a los métodos legacy.
func (s *retryStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	if e, ok := s.Stmt.(driver.StmtExecContext); ok {
		res, err := e.ExecContext(ctx, args)
		return res, s.toBadConn(err)
	}
	// Fallback al camino legacy (args posicionales).
	vals, cerr := namedToValue(args)
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Stmt.Exec(vals)
	return res, s.toBadConn(err)
}

func (s *retryStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	if q, ok := s.Stmt.(driver.StmtQueryContext); ok {
		rows, err := q.QueryContext(ctx, args)
		return rows, s.toBadConn(err)
	}
	vals, cerr := namedToValue(args)
	if cerr != nil {
		return nil, cerr
	}
	rows, err := s.Stmt.Query(vals)
	return rows, s.toBadConn(err)
}

// Exec / Query legacy (sin contexto): database/sql los usa si el Stmt no soporta
// las variantes con contexto.
func (s *retryStmt) Exec(args []driver.Value) (driver.Result, error) {
	res, err := s.Stmt.Exec(args)
	return res, s.toBadConn(err)
}

func (s *retryStmt) Query(args []driver.Value) (driver.Rows, error) {
	rows, err := s.Stmt.Query(args)
	return rows, s.toBadConn(err)
}

func (s *retryStmt) NumInput() int   { return s.Stmt.NumInput() }
func (s *retryStmt) Close() error    { return s.Stmt.Close() }

// namedToValue convierte NamedValue posicional a Value para el camino legacy.
func namedToValue(named []driver.NamedValue) ([]driver.Value, error) {
	vals := make([]driver.Value, len(named))
	for i, n := range named {
		vals[i] = n.Value
	}
	return vals, nil
}

// compile-time checks
var (
	_ driver.QueryerContext     = (*retryConn)(nil)
	_ driver.ExecerContext      = (*retryConn)(nil)
	_ driver.ConnBeginTx        = (*retryConn)(nil)
	_ driver.ConnPrepareContext = (*retryConn)(nil)
	_ driver.Pinger             = (*retryConn)(nil)
	_ io.Closer                 = (*retryConn)(nil)

	_ driver.Stmt             = (*retryStmt)(nil)
	_ driver.StmtExecContext  = (*retryStmt)(nil)
	_ driver.StmtQueryContext = (*retryStmt)(nil)
)
