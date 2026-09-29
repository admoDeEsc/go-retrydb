// Módulo del driver database/sql "pq-retry" (github.com/admoDeEsc/go-retrydb).
//
// ESTADO: HISTÓRICO / DE REFERENCIA. Ningún microservicio del stack lo consume
// hoy. Todos los servicios Go abren la base con pgx/v5 stdlib en modo
// QueryExecModeSimpleProtocol (PgBouncer-safe: sin prepared statements en el
// protocolo, por lo que el error transitorio 26000/08P01 que este driver
// reintentaba ya no aplica). Ver el README raíz del proyecto ("Flujo de
// desarrollo Go") para el enfoque vigente de acceso a datos.
//
// Se conserva como referencia de la técnica de reintento y por su cobertura de
// tests (retrydb_test.go). No forma parte del go.work del monorepo ni es
// dependencia de ningún build. Si en el futuro se readoptara, este module path
// (github.com/admoDeEsc/go-retrydb, coherente con la organización admoDeEsc/*)
// es el canónico.
module github.com/admoDeEsc/go-retrydb

go 1.22

require github.com/lib/pq v1.12.3
