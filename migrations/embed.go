package migrations

import "embed"

// Files содержит встроенные SQL миграции, расположенные в каталоге migrations/.
//
//go:embed *.sql
var Files embed.FS
