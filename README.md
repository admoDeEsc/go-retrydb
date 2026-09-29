# go-retrydb — driver `pq-retry` (HISTÓRICO / DE REFERENCIA)

Driver `database/sql` que envuelve a `lib/pq` y reintenta de forma transparente
el error transitorio de prepared statement de PgBouncer (SQLSTATE `26000` /
`08P01`) en modo *transaction pooling*.

## Estado actual

**Ningún microservicio del stack consume este módulo hoy.** Todos los servicios
Go abren la base directamente con **`pgx/v5` (stdlib) en modo
`QueryExecModeSimpleProtocol`**, que es *PgBouncer-safe*: al no usar prepared
statements en el protocolo, el error transitorio `26000`/`08P01` que `pq-retry`
reintentaba simplemente **no ocurre**. Por eso el driver dejó de ser necesario.

El module path canónico es `github.com/admoDeEsc/go-retrydb` (coherente con la
organización `admoDeEsc/*` del monorepo). El módulo:

- **No** está incluido en el `go.work` del monorepo.
- **No** es dependencia (`require`) de ningún servicio.
- Se conserva como **referencia** de la técnica de reintento y por su suite de
  tests (`retrydb_test.go`).

Para el enfoque de acceso a datos vigente, ver la sección "Flujo de desarrollo
Go" del `README.md` de la raíz del proyecto.

## Qué hace el driver (referencia técnica)

Registra un driver `database/sql` llamado `pq-retry` que envuelve a `lib/pq` y
convierte el error transitorio de prepared statement (que ocurre cuando PgBouncer
reasigna la conexión de servidor entre Parse y Bind) en `driver.ErrBadConn`, de
modo que `database/sql` descarta la conexión y reintenta en una fresca. El fallo
es pre-ejecución (fase Parse/Bind), por lo que el reintento es seguro e
idempotente. Cobertura: `Conn.Query/Exec` de sentencia suelta y `Stmt.Query/Exec`
de prepared statements cacheados por el pool. Detalle completo en el encabezado de
`retrydb.go`.

## Verificación

`orquestaDeDIC/scripts/check_retrydb.sh` es un guardaraíl de CI: confirma que
ningún servicio reintroduzca una copia local `internal/retrydb/`.
