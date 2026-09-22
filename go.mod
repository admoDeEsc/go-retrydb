// Módulo CANÓNICO del driver database/sql "pq-retry".
//
// Esta es la FUENTE ÚNICA DE VERDAD de internal/retrydb. El archivo
// retrydb.go de este módulo se propaga a los 20 servicios Go mediante
// orquestaDeDIC/scripts/sync_retrydb.sh (cada servicio conserva su copia dentro
// de su propio contexto de build de Docker, por eso NO se importa como módulo
// remoto todavía).
//
// Para modificar el driver: editar SOLO este archivo y correr el script de
// sincronización. NO editar las copias en <svc>/internal/retrydb/ a mano.
module github.com/admoDeEsc/go-retrydb

go 1.22

require github.com/lib/pq v1.12.3
