// Package migrations embeds the ordered SQL migration files so the migrate
// command ships them inside its binary.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
